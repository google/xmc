// Copyright 2025 Google LLC
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
package validator

import (
	"fmt"

	"github.com/google/xmc/internal/grpcerr"
)

const (
	// defaultMaxPageSize is the maximum page size for list requests.
	defaultMaxPageSize = 1000
	// maxPageTokenLen is the maximum allowed length of a page token string.
	// Multi-field base64-encoded JSON cursors (e.g. Artifacts with bit_reversed_xid,
	// RFC3339Nano timestamps, and display_name) can exceed 256 characters.
	maxPageTokenLen = 1024
)

// ValidateAIPCompliantList checks if a list request's parameters are IAP compliant.
// See https://google.aip.dev/158 for more details on pagination and https://google.aip.dev/160 for more details on filtering and ordering.
func ValidateAIPCompliantList(pageSize int32, filter string, orderBy string, pageToken string) error {
	return validateAIPCompliantList(pageSize, filter, orderBy, pageToken, defaultMaxPageSize)
}

// ValidateAIPCompliantListWithMaxPageSize checks if a list request's parameters are IAP compliant with a custom max page size.
// See https://google.aip.dev/158 for more details on pagination and https://google.aip.dev/160 for more details on filtering and ordering.
func ValidateAIPCompliantListWithMaxPageSize(pageSize int32, filter string, orderBy string, pageToken string, maxPageSize int32) error {
	return validateAIPCompliantList(pageSize, filter, orderBy, pageToken, maxPageSize)
}

func validateAIPCompliantList(pageSize int32, filter string, orderBy string, pageToken string, maxPageSize int32) error {
	if pageSize < 0 {
		return fmt.Errorf("page_size cannot be negative %d: %w", pageSize, grpcerr.ErrInvalidArgument)
	}
	if pageSize > maxPageSize {
		return fmt.Errorf("page_size cannot be greater than %d: %w", maxPageSize, grpcerr.ErrInvalidArgument)
	}
	if len(pageToken) > maxPageTokenLen {
		return fmt.Errorf("page_token is too long: got %d chars, max is %d: %w", len(pageToken), maxPageTokenLen, grpcerr.ErrInvalidArgument)
	}
	return nil
}
