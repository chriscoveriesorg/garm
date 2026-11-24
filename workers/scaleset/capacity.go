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
	"slices"
	"strings"
	"sync"

	"github.com/cloudbase/garm/config"
	"github.com/cloudbase/garm/params"
)

const unknownAssigned = -1

var errCapacityChanged = errors.New("scale set advertisement changed")

type capacityRow struct {
	name     string
	group    string
	tags     []string
	maximum  uint
	enabled  bool
	assigned int
}

// capacityPolicy is shared by the listeners of one forge entity. It joins
// database set identity to the latest assigned count from each live session.
type capacityPolicy struct {
	mu          sync.Mutex
	preferences map[uint]config.ScaleSetPreference
	sets        map[uint]capacityRow
	changes     chan struct{}
}

func newCapacityPolicy(preferences map[uint]config.ScaleSetPreference) *capacityPolicy {
	if len(preferences) == 0 {
		return nil
	}
	return &capacityPolicy{
		preferences: preferences,
		sets:        make(map[uint]capacityRow),
		changes:     make(chan struct{}),
	}
}

func (p *capacityPolicy) configured(id uint) bool {
	return p != nil && p.preferences[id].ID != 0
}

// signalLocked wakes every affected listener. Each listener compares its
// effective capacity before it cancels an active poll.
func (p *capacityPolicy) signalLocked() {
	close(p.changes)
	p.changes = make(chan struct{})
}

func (p *capacityPolicy) update(set params.ScaleSet) {
	if !p.configured(set.ID) {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	group := strings.ToLower(set.GitHubRunnerGroup)
	if group == "" {
		group = "default"
	}
	tags := make([]string, 0, len(set.Tags))
	for _, tag := range set.Tags {
		tags = append(tags, strings.ToLower(tag.Name))
	}
	slices.Sort(tags)
	tags = slices.Compact(tags)
	row := capacityRow{
		name: set.Name, group: group, tags: tags, maximum: set.MaxRunners,
		enabled: set.Enabled, assigned: unknownAssigned,
	}
	previous, found := p.sets[set.ID]
	sameRoute := found && previous.enabled == row.enabled && previous.name == row.name &&
		previous.group == row.group && slices.Equal(previous.tags, row.tags)
	if sameRoute {
		if previous.maximum == row.maximum {
			return
		}
		row.assigned = previous.assigned
	}
	p.sets[set.ID] = row
	p.signalLocked()
}

func (p *capacityPolicy) remove(id uint) {
	if !p.configured(id) {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.sets, id)
	p.signalLocked()
}

func (p *capacityPolicy) observe(id uint, stats *params.RunnerScaleSetStatistic) {
	if !p.configured(id) {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	row, found := p.sets[id]
	if !found {
		return
	}
	assigned := unknownAssigned
	if stats != nil && stats.TotalAssignedJobs >= 0 {
		assigned = stats.TotalAssignedJobs
	}
	if row.assigned != assigned {
		row.assigned = assigned
		p.sets[id] = row
		p.signalLocked()
	}
}

// coversTags requires every overflow label to be available on the preferred
// set. Additional preferred labels do not make any overflow job ineligible.
func coversTags(preferred, overflow []string) bool {
	for _, tag := range overflow {
		if _, found := slices.BinarySearch(preferred, tag); !found {
			return false
		}
	}
	return true
}

func (p *capacityPolicy) advertisement(id uint, name string, maximum uint) (uint, <-chan struct{}) {
	if !p.configured(id) {
		return maximum, nil
	}
	preference := p.preferences[id]
	if preference.Name != name || !preference.LabelRoutedOnly {
		return maximum, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	mine, found := p.sets[id]
	if !found || !mine.enabled || mine.name != name {
		return maximum, p.changes
	}
	maximum = mine.maximum
	if mine.assigned == unknownAssigned || len(mine.tags) == 0 {
		return maximum, p.changes
	}
	freePreferred := false
	for preferredID, preferred := range p.sets {
		rank := p.preferences[preferredID]
		if !preferred.enabled || rank.Name != preferred.name ||
			rank.Priority >= preference.Priority ||
			preferred.group != mine.group || !coversTags(preferred.tags, mine.tags) {
			continue
		}
		if preferred.assigned == unknownAssigned {
			return maximum, p.changes
		}
		if uint(preferred.assigned) < preferred.maximum {
			freePreferred = true
		}
	}
	if freePreferred {
		return min(maximum, uint(mine.assigned)), p.changes
	}
	return maximum, p.changes
}

// pollWithCapacity cancels just the request when the header should change.
// A successful response must still be processed if it races cancellation.
func pollWithCapacity(
	ctx context.Context,
	advertisement func() (uint, <-chan struct{}),
	poll func(context.Context, uint) (params.RunnerScaleSetMessage, error),
) (params.RunnerScaleSetMessage, uint, error) {
	capacity, changed := advertisement()
	if changed == nil {
		msg, err := poll(ctx, capacity)
		return msg, capacity, err
	}

	pollCtx, cancel := context.WithCancelCause(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-pollCtx.Done():
				return
			case <-changed:
				next, wake := advertisement()
				if next != capacity {
					cancel(errCapacityChanged)
					return
				}
				changed = wake
			}
		}
	}()
	msg, err := poll(pollCtx, capacity)
	cancel(nil)
	<-done
	if errors.Is(err, context.Canceled) && errors.Is(context.Cause(pollCtx), errCapacityChanged) {
		return msg, capacity, errCapacityChanged
	}
	return msg, capacity, err
}
