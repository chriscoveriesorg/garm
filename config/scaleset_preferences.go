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
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// ScaleSetPreference identifies a database row and ranks it within sets that
// share a GitHub runner group. Smaller numbers come first.
type ScaleSetPreference struct {
	ID       uint   `json:"id"`
	Name     string `json:"name"`
	Priority uint   `json:"priority"`
	// LabelRoutedOnly attests that no workflow selects this set by name.
	LabelRoutedOnly bool `json:"label_routed_only"`
}

// ReadScaleSetPreferences reads the controller's optional, Cifast-owned
// preference file. The server can continue with ordinary capacity on error.
func ReadScaleSetPreferences(path string) (map[uint]ScaleSetPreference, error) {
	if path == "" {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	const maximum = 64 * 1024
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maximum {
		return nil, fmt.Errorf("scale set preference file exceeds %d bytes", maximum)
	}
	var document struct {
		ScaleSets []ScaleSetPreference `json:"scale_sets"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("trailing data in scale set preferences")
	}

	result := make(map[uint]ScaleSetPreference, len(document.ScaleSets))
	names := make(map[string]bool, len(document.ScaleSets))
	for _, row := range document.ScaleSets {
		if row.ID == 0 || row.Name == "" || row.Priority == 0 || result[row.ID].ID != 0 || names[row.Name] {
			return nil, fmt.Errorf("invalid or duplicate scale set preference identity")
		}
		result[row.ID] = row
		names[row.Name] = true
	}
	return result, nil
}
