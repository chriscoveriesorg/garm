package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	commonParams "github.com/cloudbase/garm-provider-common/params"
	commonUtil "github.com/cloudbase/garm-provider-common/util"
	"github.com/cloudbase/garm/auth"
	"github.com/cloudbase/garm/cache"
	"github.com/cloudbase/garm/database"
	"github.com/cloudbase/garm/internal/templates"
	garmTesting "github.com/cloudbase/garm/internal/testing"
	"github.com/cloudbase/garm/params"
)

// These tool fields are projected from the GitHub org runner-downloads API,
// read on 2026-11-17. HTTP authentication/create replies below are protocol
// controls, not recordings of a live GitHub set creation.
func macOSTestDownloads(t *testing.T) []commonParams.RunnerApplicationDownload {
	t.Helper()
	body, err := os.ReadFile("../internal/testing/testdata/runner-downloads-20261117.json")
	require.NoError(t, err)
	var tools []commonParams.RunnerApplicationDownload
	require.NoError(t, json.Unmarshal(body, &tools))
	return tools
}

func TestMacOSExternalSetAdmissionAndMetadata(t *testing.T) {
	var creates atomic.Int32
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("POST /api/v3/repos/test-owner/test-repo/actions/runners/registration-token", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"token":"protocol-control"}`)
	})
	mux.HandleFunc("POST /api/v3/actions/runner-registration", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"url":%q,"token":"protocol-control"}`, srv.URL+"/pipeline")
	})
	mux.HandleFunc("POST /pipeline/_apis/runtime/runnerscalesets", func(w http.ResponseWriter, req *http.Request) {
		var set params.RunnerScaleSet
		if err := json.NewDecoder(req.Body).Decode(&set); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		set.ID = int(creates.Add(1))
		if err := json.NewEncoder(w).Encode(set); err != nil {
			t.Error(err)
		}
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	db, err := database.NewDatabase(context.Background(), garmTesting.GetTestSqliteDBConfig(t))
	require.NoError(t, err)
	ctx := garmTesting.ImpersonateAdminContext(context.Background(), db, t)
	endpoint, err := db.CreateGithubEndpoint(ctx, params.CreateGithubEndpointParams{
		Name: "macos-admission-control", APIBaseURL: srv.URL + "/api/v3/", UploadBaseURL: srv.URL + "/api/uploads/", BaseURL: srv.URL,
	})
	require.NoError(t, err)
	creds := garmTesting.CreateTestGithubCredentials(ctx, "macos-admission-control", db, t, endpoint)
	repo, err := db.CreateRepository(ctx, "test-owner", "test-repo", creds, "protocol-control", params.PoolBalancerTypeRoundRobin, false)
	require.NoError(t, err)
	entity, err := repo.GetEntity()
	require.NoError(t, err)
	r := &Runner{ctx: ctx, store: db}
	tools := macOSTestDownloads(t)
	cache.SetGithubToolsCache(entity, tools)
	defer cache.DeleteGithubToolsCache(entity.ID)

	macBody, err := templates.GetTemplateContent(commonParams.MacOS, params.GithubEndpointType)
	require.NoError(t, err)
	// This is a safety stub, not an installer, even when explicitly restored.
	stub := filepath.Join(t.TempDir(), "admission.sh")
	require.NoError(t, os.WriteFile(stub, macBody, 0o600))
	output, err := exec.Command("/bin/sh", stub).CombinedOutput()
	require.Error(t, err)
	require.Contains(t, string(output), "GARM install template is admission-only; the provider uses JIT credentials")

	for _, platform := range []struct {
		os   commonParams.OSType
		arch commonParams.OSArch
	}{
		{commonParams.MacOS, commonParams.Arm64}, {commonParams.Linux, commonParams.Amd64},
	} {
		t.Run(string(platform.os), func(t *testing.T) {
			body := macBody
			if platform.os == commonParams.Linux {
				body, err = templates.GetTemplateContent(platform.os, params.GithubEndpointType)
				require.NoError(t, err)
			}
			tpl, err := r.CreateTemplate(ctx, params.CreateTemplateParams{
				Name: "owned-" + string(platform.os), OSType: platform.os, ForgeType: params.GithubEndpointType, Data: body,
			})
			require.NoError(t, err)
			require.NotEqual(t, params.SystemUser, tpl.Owner)
			request := params.CreateScaleSetParams{
				Name: "external-" + string(platform.os), ProviderName: "external-control", MaxRunners: 1,
				Image: "macdad-tahoe-v1", Flavor: "tart-c4-m12288", OSType: platform.os, OSArch: platform.arch,
				TemplateID: &tpl.ID,
			}
			if platform.os == commonParams.Linux {
				request.Image = "claw-rust-runner-v4"
				request.Flavor = "garm-rust-root20-m7-cache"
			}
			if platform.os == commonParams.MacOS {
				before := creates.Load()
				for _, fault := range []string{"wrong-os", "wrong-arch", "no-explicit-template"} {
					bad := request
					switch fault {
					case "wrong-os":
						bad.OSType = commonParams.Linux
					case "wrong-arch":
						bad.OSArch = commonParams.OSArch("unknown")
					case "no-explicit-template":
						bad.TemplateID = nil
					}
					_, err := r.CreateEntityScaleSet(ctx, entity.EntityType, entity.ID, bad)
					require.Error(t, err, fault)
				}
				require.Equal(t, before, creates.Load(), "refusals precede GitHub writes")
			}
			set, err := r.CreateEntityScaleSet(ctx, entity.EntityType, entity.ID, request)
			require.NoError(t, err)
			require.Equal(t, request.OSType, set.OSType)
			require.Equal(t, request.OSArch, set.OSArch)
			require.Equal(t, tpl.ID, set.TemplateID)
			require.Equal(t, request.ProviderName, set.ProviderName)
			instance, err := db.CreateScaleSetInstance(ctx, set.ID, params.CreateInstanceParams{
				Name: "garm-" + string(platform.os), OSType: set.OSType, OSArch: set.OSArch,
			})
			require.NoError(t, err)
			ic := auth.SetInstanceParams(context.Background(), instance)
			ic = auth.SetInstanceRunnerStatus(ic, params.RunnerPending)
			ic = auth.SetInstanceEntity(ic, entity)
			ic = auth.SetInstanceAuthToken(ic, "protocol-control")
			selected, err := r.getRunnerInstallTemplateContext(instance, entity, "protocol-control", nil)
			require.NoError(t, err)
			want, err := commonUtil.GetTools(platform.os, platform.arch, tools)
			require.NoError(t, err)
			require.Equal(t, want.GetFilename(), selected.FileName)
			require.Equal(t, want.GetDownloadURL(), selected.DownloadURL)
			if platform.os == commonParams.MacOS {
				require.Equal(t, "osx", want.GetOS())
				require.Equal(t, "arm64", want.GetArchitecture())
			}
			script, err := r.GetRunnerInstallScript(ic)
			require.NoError(t, err)
			if platform.os == commonParams.MacOS {
				require.Equal(t, macBody, script)
			} else {
				require.NotEmpty(t, script)
			}
		})
	}
	_, err = commonUtil.GetTools(commonParams.OSType("unknown"), commonParams.Arm64, tools)
	require.Error(t, err)
	_, err = commonUtil.GetTools(commonParams.MacOS, commonParams.OSArch("unknown"), tools)
	require.Error(t, err)
	_, err = templates.GetTemplateContent(commonParams.MacOS, params.GiteaEndpointType)
	require.Error(t, err)
	require.Equal(t, "macos", string(commonParams.MacOS))
}
