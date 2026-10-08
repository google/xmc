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

package hypercomputeclient

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

type mockTokenSource struct{}

func (m mockTokenSource) Token() (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: "mock-token"}, nil
}

// newTestClient builds a client whose endpoints point at the given test server.
func newTestClient(server *httptest.Server) *ControlPlaneClient {
	return &ControlPlaneClient{
		projectID:   "test-project",
		location:    "us-central1",
		baseURL:     server.URL,
		mlRunsPath:  server.URL + "/projects/test-project/locations/us-central1/machineLearningRuns",
		tokenSource: mockTokenSource{},
		httpClient:  server.Client(),
	}
}

func TestResolveEndpoint(t *testing.T) {
	tests := []struct {
		name        string
		environment string
		location    string
		wantBaseURL string
		wantPath    string
		wantLoc     string
		wantErr     string
	}{
		{
			name:        "prod defaults location",
			environment: "prod",
			location:    "",
			wantBaseURL: "https://hypercomputecluster.googleapis.com/v1alpha",
			wantPath:    "https://hypercomputecluster.googleapis.com/v1alpha/projects/p/locations/us-central1/machineLearningRuns",
			wantLoc:     "us-central1",
		},
		{
			name:        "staging honors explicit location",
			environment: "staging",
			location:    "europe-west4",
			wantBaseURL: "https://staging-hypercomputecluster.sandbox.googleapis.com/v1alpha",
			wantPath:    "https://staging-hypercomputecluster.sandbox.googleapis.com/v1alpha/projects/p/locations/europe-west4/machineLearningRuns",
			wantLoc:     "europe-west4",
		},
		{
			name:        "autopush defaults location",
			environment: "autopush",
			location:    "",
			wantBaseURL: "https://autopush-hypercomputecluster.sandbox.googleapis.com/v1alpha",
			wantPath:    "https://autopush-hypercomputecluster.sandbox.googleapis.com/v1alpha/projects/p/locations/us-central1/machineLearningRuns",
			wantLoc:     "us-central1",
		},
		{
			name:        "invalid environment errors",
			environment: "bogus",
			wantErr:     "invalid environment",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := resolveEndpoint("p", tc.environment, tc.location)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("resolveEndpoint() error = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveEndpoint() unexpected error: %v", err)
			}
			if cfg.baseURL != tc.wantBaseURL {
				t.Errorf("baseURL = %q, want %q", cfg.baseURL, tc.wantBaseURL)
			}
			if cfg.mlRunsPath != tc.wantPath {
				t.Errorf("mlRunsPath = %q, want %q", cfg.mlRunsPath, tc.wantPath)
			}
			if cfg.location != tc.wantLoc {
				t.Errorf("location = %q, want %q", cfg.location, tc.wantLoc)
			}
		})
	}
}

func TestNewControlPlaneClientInvalidEnvironment(t *testing.T) {
	// The valid-environment path calls google.DefaultTokenSource, which needs
	// ambient credentials and cannot run hermetically; URL construction is
	// covered by TestResolveEndpoint. Here we only assert the exported
	// constructor surfaces the validation error.
	_, err := NewControlPlaneClient(context.Background(), "p", "bogus", "")
	if err == nil || !strings.Contains(err.Error(), "invalid environment") {
		t.Fatalf("NewControlPlaneClient() error = %v, want containing %q", err, "invalid environment")
	}
}

func TestResolveEndpointEmptyProjectID(t *testing.T) {
	// An empty projectID must be rejected up front to avoid malformed URLs.
	if _, err := resolveEndpoint("", "prod", "us-central1"); err == nil {
		t.Fatal("resolveEndpoint() error = nil, want error for empty projectID")
	}
}
