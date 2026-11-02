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

package postgresdata

import "time"

// MonotonicUpdateTime truncates now and prev to microsecond precision in UTC and returns
// prev.Add(time.Microsecond) when the truncated now is not strictly after the truncated prev.
func MonotonicUpdateTime(now, prev time.Time) time.Time {
	now = now.UTC().Truncate(time.Microsecond)
	prev = prev.UTC().Truncate(time.Microsecond)
	if !now.After(prev) {
		return prev.Add(time.Microsecond)
	}
	return now
}
