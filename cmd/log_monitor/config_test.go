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
package main

import (
	"flag"
	"reflect"
	"testing"
	"time"
)

func TestDefaultPort(t *testing.T) {
	f := flag.Lookup("port")
	if f == nil {
		t.Fatal("Flag 'port' not found")
	}

	if f.DefValue != "8080" {
		t.Errorf("Default value for flag 'port' = %q, want %q", f.DefValue, "8080")
	}
}

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name        string
		inputConfig Config
		wantConfig  *Config
		expectError bool
	}{
		{
			name: "Valid config",
			inputConfig: Config{
				ProjectID:            "test-project",
				SubscriptionID:       "test-subscription",
				Port:                 "8080",
				BatchSize:            100,
				BatchInterval:        5 * time.Second,
				SpannerDatabaseName:  "test-db",
				QuotaProjectID:       "xmc-test-project-1",
				LogKeyword:           "XMC LOG:",
				MaxConcurrentUpdates: 10,
			},
			wantConfig: &Config{
				ProjectID:            "test-project",
				SubscriptionID:       "test-subscription",
				Port:                 "8080",
				BatchSize:            100,
				BatchInterval:        5 * time.Second,
				SpannerDatabaseName:  "test-db",
				QuotaProjectID:       "xmc-test-project-1",
				LogKeyword:           "XMC LOG:",
				MaxConcurrentUpdates: 10,
			},
			expectError: false,
		},
		{
			name: "Missing ProjectID",
			inputConfig: Config{
				ProjectID:           "",
				SubscriptionID:      "test-subscription",
				BatchSize:           100,
				BatchInterval:       5 * time.Second,
				SpannerDatabaseName: "test-db",
				QuotaProjectID:      "xmc-test-project-1",
			},
			wantConfig:  nil,
			expectError: true,
		},
		{
			name: "Missing SubscriptionID",
			inputConfig: Config{
				ProjectID:           "test-project",
				SubscriptionID:      "",
				BatchSize:           100,
				BatchInterval:       5 * time.Second,
				SpannerDatabaseName: "test-db",
				QuotaProjectID:      "xmc-test-project-1",
			},
			wantConfig:  nil,
			expectError: true,
		},
		{
			name: "Invalid BatchSize",
			inputConfig: Config{
				ProjectID:           "test-project",
				SubscriptionID:      "test-subscription",
				BatchSize:           0,
				BatchInterval:       5 * time.Second,
				SpannerDatabaseName: "test-db",
				QuotaProjectID:      "xmc-test-project-1",
			},
			wantConfig:  nil,
			expectError: true,
		},
		{
			name: "Invalid BatchInterval",
			inputConfig: Config{
				ProjectID:           "test-project",
				SubscriptionID:      "test-subscription",
				BatchSize:           100,
				BatchInterval:       0,
				SpannerDatabaseName: "test-db",
				QuotaProjectID:      "xmc-test-project-1",
			},
			wantConfig:  nil,
			expectError: true,
		},
		{
			name: "Missing SpannerDatabaseName",
			inputConfig: Config{
				ProjectID:           "test-project",
				SubscriptionID:      "test-subscription",
				Port:                "8080",
				BatchSize:           100,
				BatchInterval:       5 * time.Second,
				SpannerDatabaseName: "",
				QuotaProjectID:      "xmc-test-project-1",
			},
			wantConfig:  nil,
			expectError: true,
		},
		{
			name: "Missing QuotaProjectID",
			inputConfig: Config{
				ProjectID:            "test-project",
				SubscriptionID:       "test-subscription",
				Port:                 "8080",
				BatchSize:            100,
				BatchInterval:        5 * time.Second,
				SpannerDatabaseName:  "test-db",
				QuotaProjectID:       "",
				MaxConcurrentUpdates: 10,
			},
			wantConfig:  nil,
			expectError: true,
		},
		{
			name: "Invalid MaxConcurrentUpdates",
			inputConfig: Config{
				ProjectID:            "test-project",
				SubscriptionID:       "test-subscription",
				Port:                 "8080",
				BatchSize:            100,
				BatchInterval:        5 * time.Second,
				SpannerDatabaseName:  "test-db",
				QuotaProjectID:       "xmc-test-project-1",
				LogKeyword:           "XMC LOG:",
				MaxConcurrentUpdates: 0,
			},
			wantConfig:  nil,
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Simulate flag parsing result
			projectID = tc.inputConfig.ProjectID
			subscriptionID = tc.inputConfig.SubscriptionID
			port = tc.inputConfig.Port
			batchSize = tc.inputConfig.BatchSize
			batchInterval = tc.inputConfig.BatchInterval
			spannerDatabaseName = tc.inputConfig.SpannerDatabaseName
			quotaProjectID = tc.inputConfig.QuotaProjectID
			logKeyword = tc.inputConfig.LogKeyword
			maxConcurrentUpdates = tc.inputConfig.MaxConcurrentUpdates

			got, err := LoadConfig()

			if tc.expectError {
				if err == nil {
					t.Errorf("LoadConfig() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("LoadConfig() unexpected error: %v", err)
				return
			}

			if !reflect.DeepEqual(tc.wantConfig, got) {
				t.Errorf("LoadConfig() mismatch.\nWant: %+v\nGot: %+v", tc.wantConfig, got)
			}
		})
	}
}
