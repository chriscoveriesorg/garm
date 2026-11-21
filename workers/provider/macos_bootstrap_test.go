//go:build testing

package provider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	commonParams "github.com/cloudbase/garm-provider-common/params"
	"github.com/cloudbase/garm/auth"
	"github.com/cloudbase/garm/cache"
	"github.com/cloudbase/garm/config"
	"github.com/cloudbase/garm/params"
	external "github.com/cloudbase/garm/runner/providers/v0.1.0"
)

type bootstrapTokenControl struct{}

func (bootstrapTokenControl) NewInstanceJWTToken(_ params.Instance, _ params.ForgeEntity, _ uint) (string, error) {
	return "protocol-control", nil
}
func (bootstrapTokenControl) NewAgentJWTToken(_ params.Instance, _ params.ForgeEntity) (string, error) {
	return "protocol-control", nil
}

type bootstrapStoreControl struct {
	storeStub
	entity params.ForgeEntity
}

func (b *bootstrapStoreControl) InstanceTokenGetter() auth.InstanceTokenGetter {
	return bootstrapTokenControl{}
}
func (b *bootstrapStoreControl) GetGithubEntity(_ context.Context, _ params.ForgeEntity) (params.ForgeEntity, error) {
	return b.entity, nil
}

func TestMacOSAndLinuxExternalBootstrap(t *testing.T) {
	// Tools are a field projection of the real GitHub downloads listing read
	// 2026-11-17; OS transitions and the temporary provider are directed controls.
	body, err := os.ReadFile("../../internal/testing/testdata/runner-downloads-20261117.json")
	require.NoError(t, err)
	var tools []commonParams.RunnerApplicationDownload
	require.NoError(t, json.Unmarshal(body, &tools))
	for _, osType := range []commonParams.OSType{commonParams.MacOS, commonParams.Linux} {
		t.Run(string(osType), func(t *testing.T) {
			tmp := t.TempDir()
			capture := filepath.Join(tmp, "bootstrap.json")
			t.Setenv("BOOTSTRAP_CONTROL_CAPTURE", capture)
			name := "garm-" + string(osType)
			// This subprocess records the actual v0.1.0 provider stdin before
			// returning a protocol result. It does not simulate a native VM boot.
			program := "#!/bin/sh\nset -eu\ncat > \"$BOOTSTRAP_CONTROL_CAPTURE\"\nprintf '%s\\n' '{\"provider_id\":\"" + name + "\",\"name\":\"" + name + "\",\"status\":\"running\"}'\n"
			executable := filepath.Join(tmp, "provider")
			require.NoError(t, os.WriteFile(executable, []byte(program), 0o700))
			provider, err := external.NewProvider(context.Background(), &config.Provider{
				Name: "external-control", ProviderType: params.ExternalProvider,
				External: config.External{ProviderExecutable: executable, ExecTimeoutSeconds: 5, EnvironmentVariables: []string{"BOOTSTRAP_CONTROL_CAPTURE"}},
			}, "controller-control")
			require.NoError(t, err)
			entity := params.ForgeEntity{ID: "bootstrap-control", Owner: "chriscoveriesorg", EntityType: params.ForgeEntityTypeOrganization}
			helper := &bootstrapStoreControl{entity: entity}
			set := params.ScaleSet{ID: 25, OrgID: entity.ID, OSType: osType, OSArch: commonParams.Arm64, TemplateID: 6,
				Image: "macdad-tahoe-v1", Flavor: "tart-c4-m12288", RunnerBootstrapTimeout: 20}
			if osType == commonParams.Linux {
				set.OSArch = commonParams.Amd64
				set.Image = "claw-rust-runner-v4"
				set.Flavor = "garm-rust-root20-m7-cache"
			}
			cache.SetGithubToolsCache(entity, tools)
			defer cache.DeleteGithubToolsCache(entity.ID)
			instance := params.Instance{Name: name, OSType: set.OSType, OSArch: set.OSArch, MetadataURL: "http://metadata-control", CallbackURL: "http://callback-control"}
			manager, err := newInstanceManager(context.Background(), instance, set, provider, helper)
			require.NoError(t, err)
			require.NoError(t, manager.handleCreateInstanceInProvider(instance))
			raw, err := os.ReadFile(capture)
			require.NoError(t, err)
			var got commonParams.BootstrapInstance
			require.NoError(t, json.Unmarshal(raw, &got))
			require.Equal(t, set.OSType, got.OSType)
			require.Equal(t, set.OSArch, got.OSArch)
			require.Equal(t, set.Image, got.Image)
			require.Equal(t, set.Flavor, got.Flavor)
			require.Equal(t, name, got.Name)
			require.Equal(t, tools, got.Tools)
			require.True(t, got.JitConfigEnabled)
			require.Equal(t, "protocol-control", got.InstanceToken)
			require.Equal(t, instance.MetadataURL, got.MetadataURL)
			require.Equal(t, manager.pseudoPoolID(), got.PoolID)
		})
	}
}
