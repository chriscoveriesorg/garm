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
	"errors"
	"testing"
	"time"

	"github.com/cloudbase/garm/config"
	"github.com/cloudbase/garm/params"
)

func preferenceSets() (params.ScaleSet, params.ScaleSet, params.ScaleSet) {
	labels := []string{"self-hosted", "Linux", "X64", "own-ci", "spiderwick", "bosn", "rust"}
	tags := make([]params.Tag, 0, len(labels))
	for _, name := range labels {
		tags = append(tags, params.Tag{Name: name})
	}
	clawTags := append(tags[:len(tags):len(tags)],
		params.Tag{Name: "avx512"}, params.Tag{Name: "claw"}, params.Tag{Name: "claw-rust"})
	// These names, IDs, maxima and labels come from the 23 Nov controller census.
	return params.ScaleSet{ID: 26, Name: "webclip-org", Enabled: true, MaxRunners: 2, Tags: tags},
		params.ScaleSet{ID: 25, Name: "claw-org", Enabled: true, MaxRunners: 2, Tags: clawTags},
		params.ScaleSet{ID: 33, Name: "ci7550-shared", Enabled: true, MaxRunners: 1, Tags: tags}
}

func assigned(count int) *params.RunnerScaleSetStatistic {
	return &params.RunnerScaleSetStatistic{TotalAssignedJobs: count}
}

func TestCapacityAdvertisement(t *testing.T) {
	preferred, second, overflow := preferenceSets()
	p := newCapacityPolicy(map[uint]config.ScaleSetPreference{
		26: {ID: 26, Name: "webclip-org", Priority: 1},
		25: {ID: 25, Name: "claw-org", Priority: 2, LabelRoutedOnly: true},
		33: {ID: 33, Name: "ci7550-shared", Priority: 3, LabelRoutedOnly: true},
	})
	for _, set := range []params.ScaleSet{preferred, second, overflow} {
		p.update(set)
	}
	check := func(set params.ScaleSet, want uint) {
		t.Helper()
		got, _ := p.advertisement(set.ID, set.Name, set.MaxRunners)
		if got != want {
			t.Fatalf("%s advertised %d, want %d", set.Name, got, want)
		}
	}

	check(overflow, 1) // Every count is unknown.
	p.observe(preferred.ID, assigned(0))
	p.observe(overflow.ID, assigned(0))
	check(overflow, 1) // The second eligible set is still unknown.
	p.observe(second.ID, assigned(1))
	check(preferred, 2)
	check(second, 2)   // Webclip cannot run all Claw jobs.
	check(overflow, 0) // Idle overflow is frozen only with covered labels above it.
	p.observe(overflow.ID, assigned(1))
	check(overflow, 1)
	p.observe(overflow.ID, assigned(0))
	p.observe(preferred.ID, assigned(2))
	p.observe(second.ID, assigned(2))
	check(overflow, 1)
	p.observe(second.ID, assigned(1))
	check(overflow, 0)
	p.observe(second.ID, nil) // Lost session or incomplete statistics.
	check(overflow, 1)
	p.observe(second.ID, assigned(1))
	p.observe(overflow.ID, nil)
	check(overflow, 1)
	p.observe(overflow.ID, assigned(0))
	second.Enabled = false
	p.update(second)
	check(overflow, 1)
	preferred.Name = "claw-org"
	p.update(preferred)
	p.observe(preferred.ID, assigned(0))
	check(overflow, 1) // The known ID and configured name must agree.
	check(preferred, 2)
	check(params.ScaleSet{ID: 30, Name: "macdad-vm", MaxRunners: 1}, 1)
}

func TestCapacityPreferenceRespectsTheEntireRoute(t *testing.T) {
	preferred, _, overflow := preferenceSets()
	p := newCapacityPolicy(map[uint]config.ScaleSetPreference{
		26: {ID: 26, Name: "webclip-org", Priority: 1},
		33: {ID: 33, Name: "ci7550-shared", Priority: 3, LabelRoutedOnly: true},
	})
	p.update(overflow)
	p.observe(overflow.ID, assigned(0))
	check := func(want uint) {
		t.Helper()
		p.update(preferred)
		p.observe(preferred.ID, assigned(0))
		got, _ := p.advertisement(overflow.ID, overflow.Name, overflow.MaxRunners)
		if got != want {
			t.Fatalf("advertised %d, want %d", got, want)
		}
	}
	check(0)
	preferred.GitHubRunnerGroup = "Other"
	check(1)
	preferred.GitHubRunnerGroup = "Default"
	check(0)
	preferred.Tags = preferred.Tags[:len(preferred.Tags)-1]
	check(1)
}

func TestCapacityKeepsNameRoutedSetsOpen(t *testing.T) {
	preferred, _, overflow := preferenceSets()
	p := newCapacityPolicy(map[uint]config.ScaleSetPreference{
		26: {ID: 26, Name: "webclip-org", Priority: 1},
		33: {ID: 33, Name: "ci7550-shared", Priority: 3},
	})
	p.update(preferred)
	p.update(overflow)
	p.observe(preferred.ID, assigned(0))
	p.observe(overflow.ID, assigned(0))
	if limit, changes := p.advertisement(overflow.ID, overflow.Name, overflow.MaxRunners); limit != 1 || changes != nil {
		t.Fatalf("set without a label-only attestation advertised %d", limit)
	}
}

func TestCapacityChangeEndsOnlyTheCurrentPoll(t *testing.T) {
	preferred, _, overflow := preferenceSets()
	p := newCapacityPolicy(map[uint]config.ScaleSetPreference{
		26: {ID: 26, Name: "webclip-org", Priority: 1},
		33: {ID: 33, Name: "ci7550-shared", Priority: 3, LabelRoutedOnly: true},
	})
	p.update(preferred)
	p.update(overflow)
	p.observe(preferred.ID, assigned(0))
	p.observe(overflow.ID, assigned(0))
	advertise := func() (uint, <-chan struct{}) {
		return p.advertisement(overflow.ID, overflow.Name, overflow.MaxRunners)
	}
	for _, messageWins := range []bool{false, true} {
		p.observe(preferred.ID, assigned(0))
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		msg, limit, err := pollWithCapacity(ctx, advertise, func(pollCtx context.Context, _ uint) (params.RunnerScaleSetMessage, error) {
			p.observe(preferred.ID, assigned(2))
			<-pollCtx.Done()
			if messageWins {
				return params.RunnerScaleSetMessage{MessageID: 1}, nil
			}
			return params.RunnerScaleSetMessage{}, pollCtx.Err()
		})
		parentErr := ctx.Err()
		cancel()
		if limit != 0 || parentErr != nil {
			t.Fatalf("poll cap %d, parent context %v", limit, parentErr)
		}
		if messageWins {
			if msg.MessageID != 1 || err != nil {
				t.Fatalf("successful response lost: %#v, %v", msg, err)
			}
		} else if !errors.Is(err, errCapacityChanged) {
			t.Fatalf("wrong request cancellation: %v", err)
		}
		if limit, _ := advertise(); limit != 1 {
			t.Fatalf("next poll advertised %d, want full overflow capacity", limit)
		}
	}
}
