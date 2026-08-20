// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package tputopology

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrInvalidTopology is returned when a TPU topology or slice string cannot be parsed.
var ErrInvalidTopology = errors.New("invalid TPU topology")

// Parse calculates total TPU chips from a topology string (e.g., "2x2x4", "2x2x4:2"),
// slice name (e.g., "v5p-8", "v4-16"), or numeric string ("8").
// Returns an error if the topology format is invalid or has non-positive dimensions.
func Parse(topologyOrSlice string) (int64, error) {
	s := strings.TrimSpace(topologyOrSlice)
	if s == "" {
		return 1, fmt.Errorf("%w: empty topology string", ErrInvalidTopology)
	}

	// Remove suffix modifier if present (e.g., "2x2x4:2" -> "2x2x4")
	if idx := strings.Index(s, ":"); idx != -1 {
		s = strings.TrimSpace(s[:idx])
		if s == "" {
			return 1, fmt.Errorf("%w: empty topology string before colon", ErrInvalidTopology)
		}
	}

	sLower := strings.ToLower(s)

	// Check for topology format with 'x' (e.g., "2x2x4", "4x4")
	if strings.Contains(sLower, "x") {
		dims := strings.Split(sLower, "x")
		totalChips := int64(1)
		for _, d := range dims {
			v, err := strconv.ParseInt(strings.TrimSpace(d), 10, 64)
			if err != nil {
				return 1, fmt.Errorf("%w: invalid dimension %q: %w", ErrInvalidTopology, d, err)
			}
			if v <= 0 {
				return 1, fmt.Errorf("%w: dimension must be positive, got %d", ErrInvalidTopology, v)
			}
			totalChips *= v
		}
		return totalChips, nil
	}

	// Check if it's a numeric string (e.g., "8")
	if v, err := strconv.ParseInt(sLower, 10, 64); err == nil {
		if v <= 0 {
			return 1, fmt.Errorf("%w: numeric value must be positive, got %d", ErrInvalidTopology, v)
		}
		return v, nil
	}

	// Check for slice names ending with a number after a hyphen (e.g., "v5p-8", "v4-16")
	if idx := strings.LastIndex(sLower, "-"); idx != -1 {
		numStr := strings.TrimSpace(sLower[idx+1:])
		if v, err := strconv.ParseInt(numStr, 10, 64); err == nil && v > 0 {
			return v, nil
		}
	}

	return 1, fmt.Errorf("%w: unable to parse %q", ErrInvalidTopology, topologyOrSlice)
}

// ParseTotalChips returns the total chip count for a topology or slice string,
// returning 1 as a fallback for empty or invalid inputs.
func ParseTotalChips(topologyOrSlice string) int64 {
	chips, err := Parse(topologyOrSlice)
	if err != nil {
		return 1
	}
	return chips
}
