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

package offsetpagetoken

import (
	"encoding/base64"
	"errors"
	"fmt"
	"testing"

	"xmanager_cloud/internal/storage"
)

func TestClampPageSizeAndPageToken(t *testing.T) {
	clampCases := []struct {
		in   int32
		want int32
	}{
		{in: 0, want: DefaultPageSize},
		{in: -5, want: DefaultPageSize},
		{in: 25, want: 25},
		{in: 5000, want: MaxPageSize},
	}
	for _, tc := range clampCases {
		t.Run(fmt.Sprint(tc.in), func(t *testing.T) {
			if got := ClampPageSize(tc.in); got != tc.want {
				t.Errorf("ClampPageSize(%d) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}

	tokenCases := []struct {
		name    string
		token   string
		want    int64
		wantErr bool
	}{
		{name: "empty", token: "", want: 0},
		{name: "padded url encoding", token: Encode(12), want: 12},
		{name: "unpadded raw url encoding", token: base64.RawURLEncoding.EncodeToString([]byte("5")), want: 5},
		{name: "negative offset", token: Encode(-1), wantErr: true},
		{name: "non-numeric offset", token: base64.URLEncoding.EncodeToString([]byte("abc")), wantErr: true},
		{name: "corrupt base64", token: "!!!", wantErr: true},
	}
	for _, tc := range tokenCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decode(tc.token)
			if tc.wantErr {
				if !errors.Is(err, storage.ErrInvalidPageToken) {
					t.Errorf("Decode(%q) = %v, want ErrInvalidPageToken", tc.token, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Errorf("Decode(%q) = (%d, %v), want (%d, nil)", tc.token, got, err, tc.want)
			}
		})
	}
}
