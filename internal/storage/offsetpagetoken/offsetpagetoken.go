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

// Package offsetpagetoken provides offset-based page token encoding, decoding,
// and page size clamping shared across storage backends.
package offsetpagetoken

import (
	"encoding/base64"
	"fmt"
	"strconv"

	"xmanager_cloud/internal/storage"
)

const (
	// DefaultPageSize is the fallback page size when a caller specifies zero or a negative value.
	DefaultPageSize = int32(100)
	// MaxPageSize caps caller-supplied page sizes for offset-paginated queries.
	MaxPageSize = int32(1000)
)

// ClampPageSize normalizes pageSize into [1, MaxPageSize], defaulting non-positive values to DefaultPageSize.
func ClampPageSize(pageSize int32) int32 {
	if pageSize <= 0 {
		return DefaultPageSize
	}
	return min(pageSize, MaxPageSize)
}

// Decode parses a URL-safe base64-encoded non-negative decimal offset token, returning 0 for an empty token.
func Decode(token string) (int64, error) {
	if token == "" {
		return 0, nil
	}
	b, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		b, err = base64.RawURLEncoding.DecodeString(token)
		if err != nil {
			return 0, fmt.Errorf("%w: %v", storage.ErrInvalidPageToken, err)
		}
	}
	offset, err := strconv.ParseInt(string(b), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", storage.ErrInvalidPageToken, err)
	}
	if offset < 0 {
		return 0, fmt.Errorf("%w: offset cannot be negative", storage.ErrInvalidPageToken)
	}
	return offset, nil
}

// Encode formats an offset as a URL-safe base64 page token.
func Encode(offset int64) string {
	return base64.URLEncoding.EncodeToString([]byte(strconv.FormatInt(offset, 10)))
}
