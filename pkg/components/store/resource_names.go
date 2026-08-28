// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package store

import (
	"strings"
)

const (
	componentPrefix = "components/"
	maxNameLength   = 63
)

// CanonicalComponentName strips whitespace, leading/trailing slashes, and the AIP-122
// "components/" resource prefix to return the canonical resource ID.
func CanonicalComponentName(name string) string {
	name = strings.Trim(strings.TrimSpace(name), "/")
	canonical := strings.TrimPrefix(name, componentPrefix)
	return strings.Trim(canonical, "/")
}

// FullComponentName formats a canonical or relative name into AIP-122
// resource name format "components/{name}". Returns "" if the input name is empty.
func FullComponentName(name string) string {
	canonical := CanonicalComponentName(name)
	if canonical == "" {
		return ""
	}
	return componentPrefix + canonical
}

// IsValidComponentName validates that a component name conforms to DNS-1123 / AIP-122
// identifier format (lowercase alphanumeric and hyphens, 1-63 chars, start/end with alphanumeric).
func IsValidComponentName(name string) bool {
	if len(name) < 1 || len(name) > maxNameLength {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			continue
		}
		if c == '-' {
			if i == 0 || i == len(name)-1 {
				return false
			}
			continue
		}
		return false
	}
	return true
}
