// Copyright 2025 Cloudbase Solutions SRL
//
//	Licensed under the Apache License, Version 2.0 (the "License"); you may
//	not use this file except in compliance with the License. You may obtain
//	a copy of the License at
//
//	     http://www.apache.org/licenses/LICENSE-2.0
//
//	Unless required by applicable law or agreed to in writing, software
//	distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
//	WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
//	License for the specific language governing permissions and limitations
//	under the License.
package scaleset

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cloudbase/garm/params"
	"github.com/cloudbase/garm/runner/common"
	workersCommon "github.com/cloudbase/garm/workers/common"
)

// A disabled scale set's listener calls can only fail (the forge answers
// them 404), so the retry loop must leave it inert: no worker start and no
// recorded retry attempt. An enabled set still goes through the retry path.
func TestRetryFailedScaleSetsSkipsDisabledSets(t *testing.T) {
	controller := &Controller{
		ctx:       context.Background(),
		running:   true,
		providers: map[string]common.Provider{},
		backoff:   workersCommon.NewBackoff(workersCommon.DefaultBackoffConfig()),
	}

	disabled := params.ScaleSet{ID: 1, Name: "disabled", Enabled: false, ProviderName: "absent"}
	enabled := params.ScaleSet{ID: 2, Name: "enabled", Enabled: true, ProviderName: "absent"}
	controller.ScaleSets.Store(uint(1), &scaleSet{scaleSet: disabled})
	controller.ScaleSets.Store(uint(2), &scaleSet{scaleSet: enabled})

	controller.retryFailedScaleSets()

	disabledEntry, ok := controller.ScaleSets.Load(uint(1))
	require.True(t, ok)
	require.Nil(t, disabledEntry.(*scaleSet).worker, "disabled scale set must not get a worker")
	require.True(t, controller.backoff.ShouldRetry(scaleSetBackoffKey(disabled)),
		"disabled scale set must not record a retry attempt")

	enabledEntry, ok := controller.ScaleSets.Load(uint(2))
	require.True(t, ok)
	require.Nil(t, enabledEntry.(*scaleSet).worker) // provider is absent, creation failed
	require.False(t, controller.backoff.ShouldRetry(scaleSetBackoffKey(enabled)),
		"enabled scale set must record a retry attempt and back off")
}
