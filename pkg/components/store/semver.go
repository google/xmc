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
	"xmanager_cloud/pkg/components/semver"
)

// Version represents a parsed Semantic Version 2.0.0 structure.
// Aliased from pkg/components/semver.Version for backward compatibility.
type Version = semver.Version

// Parse delegates to semver.Parse.
func Parse(v string) (*Version, error) {
	return semver.Parse(v)
}

// Compare delegates to semver.Compare.
func Compare(v1, v2 string) (int, error) {
	return semver.Compare(v1, v2)
}

// IsPatchUpdate delegates to semver.IsPatchUpdate.
func IsPatchUpdate(current, target string) (bool, error) {
	return semver.IsPatchUpdate(current, target)
}

// SatisfiesMinVersion delegates to semver.SatisfiesMinVersion.
func SatisfiesMinVersion(version, minVersion string) (bool, error) {
	return semver.SatisfiesMinVersion(version, minVersion)
}
