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

package oauth2

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestEntraTenant(t *testing.T) {
	type result struct {
		Tenant string
		OK     bool
	}
	tests := []struct {
		url  string
		want result
	}{
		{url: "https://login.microsoftonline.com/Contoso.onmicrosoft.com/oauth2/v2.0/authorize", want: result{"contoso.onmicrosoft.com", true}},
		{url: "https://LOGIN.microsoftonline.us/9188040d-6c67-4c5b-b112-36a304b66dad/v2.0", want: result{"9188040d-6c67-4c5b-b112-36a304b66dad", true}},
		{url: "https://login.microsoftonline.com", want: result{"", true}},
		{url: "https://login.microsoft.com/contoso.com/oauth2/v2.0/authorize", want: result{"contoso.com", true}},
		{url: "https://login.windows.net/contoso.com/oauth2/authorize", want: result{"contoso.com", true}},
		{url: "https://sts.windows.net/9188040d-6c67-4c5b-b112-36a304b66dad/", want: result{"9188040d-6c67-4c5b-b112-36a304b66dad", true}},
		{url: "https://accounts.google.com/o/oauth2/v2/auth"},
		{url: "https://dex.example.com/auth"},
		{url: "://bad"},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			tenant, ok := entraTenant(tt.url)
			if diff := cmp.Diff(tt.want, result{tenant, ok}); diff != "" {
				t.Errorf("entraTenant(%q) mismatch (-want +got):\n%s", tt.url, diff)
			}
		})
	}
}

func TestValidateEntraAuthority(t *testing.T) {
	tests := []struct {
		url     string
		wantErr bool
	}{
		{url: "https://login.microsoftonline.com/contoso.onmicrosoft.com/oauth2/v2.0/authorize"},
		{url: "https://dex.example.com/auth"},
		{url: ""},
		{url: "https://login.microsoftonline.com/common/oauth2/v2.0/authorize", wantErr: true},
		{url: "https://login.microsoftonline.com/Organizations/oauth2/v2.0/authorize", wantErr: true},
		{url: "https://login.microsoftonline.com/consumers/oauth2/v2.0/authorize", wantErr: true},
		{url: "https://login.microsoft.com/common/oauth2/v2.0/authorize", wantErr: true},
		{url: "https://login.windows.net/organizations/oauth2/authorize", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			if err := validateEntraAuthority(tt.url); (err != nil) != tt.wantErr {
				t.Errorf("validateEntraAuthority(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}
