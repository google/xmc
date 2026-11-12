// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"fmt"
	"strings"

	"xmanager_cloud/internal/storage"
)

var jobsOrderFields = map[string]storage.ActiveWorkUnitSortField{
	"author":   storage.ActiveWorkUnitSortAuthor,
	"status":   storage.ActiveWorkUnitSortStatus,
	"priority": storage.ActiveWorkUnitSortPriority,
}

// parseJobsOrderBy parses the API order_by string into a typed storage query.
func parseJobsOrderBy(value string) (storage.ActiveWorkUnitQuery, error) {
	if strings.TrimSpace(value) == "" {
		return storage.ActiveWorkUnitQuery{}, nil
	}
	parts := strings.Fields(value)
	if len(parts) > 2 {
		return storage.ActiveWorkUnitQuery{}, fmt.Errorf("order_by must name author, status, or priority with optional asc or desc")
	}
	field, ok := jobsOrderFields[strings.ToLower(parts[0])]
	if !ok {
		return storage.ActiveWorkUnitQuery{}, fmt.Errorf("unsupported order_by field %q", parts[0])
	}
	descending := false
	if len(parts) == 2 {
		switch strings.ToLower(parts[1]) {
		case "asc":
		case "desc":
			descending = true
		default:
			return storage.ActiveWorkUnitQuery{}, fmt.Errorf("unsupported order_by direction %q", parts[1])
		}
	}
	return storage.ActiveWorkUnitQuery{SortField: field, Descending: descending}, nil
}
