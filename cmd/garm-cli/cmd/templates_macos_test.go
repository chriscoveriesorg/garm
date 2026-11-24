package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	commonParams "github.com/cloudbase/garm-provider-common/params"
	"github.com/cloudbase/garm/params"
)

func TestMacOSTemplateCLIRequests(t *testing.T) {
	oldInit, oldClient, oldAuth, oldRaw := needsInit, apiCli, authToken, rawHTTPClient
	oldOS, oldForge, oldPath, oldName := templateOSType, templateForgeType, templatePath, templateName
	osFlag, forgeFlag := templateRestoreCmd.Flags().Lookup("os-type"), templateRestoreCmd.Flags().Lookup("forge-type")
	oldOSChanged, oldForgeChanged := osFlag.Changed, forgeFlag.Changed
	t.Cleanup(func() {
		needsInit, apiCli, authToken, rawHTTPClient = oldInit, oldClient, oldAuth, oldRaw
		templateOSType, templateForgeType, templatePath, templateName = oldOS, oldForge, oldPath, oldName
		osFlag.Changed, forgeFlag.Changed = oldOSChanged, oldForgeChanged
	})
	var created params.CreateTemplateParams
	var restored params.RestoreTemplateRequest
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/templates", func(w http.ResponseWriter, req *http.Request) {
		if err := json.NewDecoder(req.Body).Decode(&created); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if err := created.Validate(); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(params.Template{ID: 6, Name: created.Name, OSType: created.OSType, ForgeType: created.ForgeType, Data: created.Data}); err != nil {
			t.Error(err)
		}
	})
	mux.HandleFunc("POST /api/v1/templates/restore", func(w http.ResponseWriter, req *http.Request) {
		if err := json.NewDecoder(req.Body).Decode(&restored); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	initAPIClient(srv.URL, "protocol-control")
	needsInit = false
	templateOSType, templateForgeType, templateName = "macos", "github", "owned-macos"
	templatePath = filepath.Join(t.TempDir(), "stub.sh")
	require.NoError(t, os.WriteFile(templatePath, []byte("#!/bin/sh\nexit 1\n"), 0o600))
	require.NoError(t, templateCreateCmd.RunE(templateCreateCmd, nil))
	require.Equal(t, commonParams.MacOS, created.OSType)
	require.Equal(t, params.GithubEndpointType, created.ForgeType)
	osFlag.Changed, forgeFlag.Changed = true, true
	require.NoError(t, templateRestoreCmd.RunE(templateRestoreCmd, nil))
	require.Equal(t, commonParams.MacOS, restored.OSType)
	require.Equal(t, params.GithubEndpointType, restored.Forge)
	require.False(t, restored.RestoreAll)
	templateForgeType = "gitea"
	require.ErrorContains(t, templateCreateCmd.RunE(templateCreateCmd, nil), "require the github forge")
	require.ErrorContains(t, templateRestoreCmd.RunE(templateRestoreCmd, nil), "require the github forge")
	templateForgeType = "github"
	templateOSType = "macOS"
	require.ErrorContains(t, templateCreateCmd.RunE(templateCreateCmd, nil), "invalid OS type")
	require.ErrorContains(t, templateRestoreCmd.RunE(templateRestoreCmd, nil), "invalid OS type")
}
