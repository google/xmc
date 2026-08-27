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

package server

// clampPageSize applies the AIP-158 default and maximum page size bounds for a list collection.
// An unset (zero) page_size means "server picks", and an oversized one is capped rather than
// rejected. The storage layer requires a strictly positive page size, so every list handler must
// resolve page_size through this function before reaching it.
func clampPageSize(pageSize, defaultPageSize, maxPageSize int32) int32 {
	if pageSize <= 0 {
		return defaultPageSize
	}
	if pageSize > maxPageSize {
		return maxPageSize
	}
	return pageSize
}
