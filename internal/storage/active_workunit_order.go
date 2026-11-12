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

package storage

import (
	"fmt"
	"strings"
)

// ActiveWorkUnitOrderBy validates the display ordering independently of scheduling priority.
func ActiveWorkUnitOrderBy(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", nil
	}
	parts := strings.Fields(value)
	if len(parts) > 2 {
		return "", fmt.Errorf("order_by must name author, status, or priority with optional asc or desc")
	}
	switch parts[0] {
	case "author", "status", "priority":
	default:
		return "", fmt.Errorf("unsupported order_by field %q", parts[0])
	}
	direction := "asc"
	if len(parts) == 2 {
		direction = strings.ToLower(parts[1])
	}
	if direction != "asc" && direction != "desc" {
		return "", fmt.Errorf("unsupported order_by direction %q", direction)
	}
	return parts[0] + " " + direction, nil
}
