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
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

const (
	gcloudClientID = "32555940559.apps.googleusercontent.com"
	adcClientID    = "764086051850-6qr4p6gpi6hn506pt8ejuq83di341hur.apps.googleusercontent.com"
)

func TestParseAdditionalAudiences(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []string
		wantErr string
	}{
		{name: "unset", input: "", want: nil},
		{name: "separators only", input: " , ,, ", want: nil},
		{name: "comma and space separated", input: " a.apps, b.apps c.apps ", want: []string{"a.apps", "b.apps", "c.apps"}},
		{name: "duplicates removed in first-seen order", input: "b,a,b,a", want: []string{"b", "a"}},
		{name: "gcloud client rejected", input: "mine.apps," + gcloudClientID, wantErr: "Google Cloud SDK"},
		{name: "adc client rejected", input: adcClientID, wantErr: "application-default"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseAdditionalAudiences(tc.input)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("parseAdditionalAudiences(%q) error = %v, want error containing %q", tc.input, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAdditionalAudiences(%q) unexpected error: %v", tc.input, err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("parseAdditionalAudiences(%q) mismatch (-want +got):\n%s", tc.input, diff)
			}
		})
	}
}

func TestLoadConfigAdditionalAudiences(t *testing.T) {
	tests := []struct {
		name    string
		env     string
		want    []string
		wantErr bool
	}{
		{name: "parsed and deduplicated", env: "desktop.apps, other.apps desktop.apps", want: []string{"desktop.apps", "other.apps"}},
		{name: "shared public client fails startup", env: gcloudClientID, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MOCK_AUTH", "true")
			t.Setenv("ADDITIONAL_AUDIENCES", tc.env)

			cfg, err := LoadConfig()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("LoadConfig() succeeded with ADDITIONAL_AUDIENCES=%q, want error", tc.env)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadConfig() unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.want, cfg.AdditionalAudiences); diff != "" {
				t.Errorf("AdditionalAudiences mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAcceptedAudiences(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		base []string
		want []string
	}{
		{name: "nothing configured keeps empty client id", cfg: Config{}, want: []string{""}},
		{name: "client id only", cfg: Config{ClientID: "web"}, want: []string{"web"}},
		{
			name: "base then client id then additional",
			cfg:  Config{ClientID: "web", AdditionalAudiences: []string{"desktop", "cli"}},
			base: []string{"iap-http", "iap-grpc"},
			want: []string{"iap-http", "iap-grpc", "web", "desktop", "cli"},
		},
		{
			name: "empty base and client id passed through for fail-fast validation",
			cfg:  Config{AdditionalAudiences: []string{"desktop"}},
			base: []string{"", "iap"},
			want: []string{"", "iap", "", "desktop"},
		},
		{
			name: "additional audiences already present skipped",
			cfg:  Config{ClientID: "web", AdditionalAudiences: []string{"web", "iap", "desktop", "", "desktop"}},
			base: []string{"iap"},
			want: []string{"iap", "web", "desktop"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.cfg.AcceptedAudiences(tc.base...)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("AcceptedAudiences(%v) mismatch (-want +got):\n%s", tc.base, diff)
			}
		})
	}
}
