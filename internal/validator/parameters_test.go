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
	"testing"
)

func TestValidateIAPCompliantList(t *testing.T) {
	cases := []struct {
		name      string
		pageSize  int32
		pageToken string
		wantErr   bool
	}{
		{
			name:     "valid request",
			pageSize: 50,
		},
		{
			name:     "negative page size",
			pageSize: -1,
			wantErr:  true,
		},
		{
			name:     "page size too large",
			pageSize: defaultMaxPageSize + 1,
			wantErr:  true,
		},
		{
			name:      "page token too long",
			pageToken: string(make([]byte, maxPageTokenLen+1)),
			wantErr:   true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAIPCompliantList(tc.pageSize, "", "", tc.pageToken)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateIAPCompliantList() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
