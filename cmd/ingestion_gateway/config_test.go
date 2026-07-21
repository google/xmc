// Copyright 2026 Google LLC
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
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseBrokers(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "Single broker",
			input:    "localhost:9092",
			expected: []string{"localhost:9092"},
		},
		{
			name:     "Multiple brokers with whitespace",
			input:    " broker1:9092, broker2:9092 ,broker3:9092 ",
			expected: []string{"broker1:9092", "broker2:9092", "broker3:9092"},
		},
		{
			name:     "Empty string",
			input:    "",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseBrokers(tt.input)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("parseBrokers(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name: "Valid default config",
			cfg: Config{
				Port:            "8080",
				RedpandaBrokers: []string{"localhost:9092"},
				TopicName:       "xmc-metrics",
				MinPartitions:   128,
				MinReplicas:     3,
			},
			wantErr: false,
		},
		{
			name: "Invalid port",
			cfg: Config{
				Port:            "invalid",
				RedpandaBrokers: []string{"localhost:9092"},
				TopicName:       "xmc-metrics",
				MinPartitions:   128,
				MinReplicas:     3,
			},
			wantErr: true,
		},
		{
			name: "Empty brokers",
			cfg: Config{
				Port:            "8080",
				RedpandaBrokers: nil,
				TopicName:       "xmc-metrics",
				MinPartitions:   128,
				MinReplicas:     3,
			},
			wantErr: true,
		},
		{
			name: "Empty topic name",
			cfg: Config{
				Port:            "8080",
				RedpandaBrokers: []string{"localhost:9092"},
				TopicName:       "",
				MinPartitions:   128,
				MinReplicas:     3,
			},
			wantErr: true,
		},
		{
			name: "Invalid min_partitions",
			cfg: Config{
				Port:            "8080",
				RedpandaBrokers: []string{"localhost:9092"},
				TopicName:       "xmc-metrics",
				MinPartitions:   0,
				MinReplicas:     3,
			},
			wantErr: true,
		},
		{
			name: "Invalid min_replicas",
			cfg: Config{
				Port:            "8080",
				RedpandaBrokers: []string{"localhost:9092"},
				TopicName:       "xmc-metrics",
				MinPartitions:   128,
				MinReplicas:     0,
			},
			wantErr: true,
		},
		{
			name: "SASL enabled without user",
			cfg: Config{
				Port:            "8080",
				RedpandaBrokers: []string{"localhost:9092"},
				TopicName:       "xmc-metrics",
				MinPartitions:   128,
				MinReplicas:     3,
				SASLEnabled:     true,
				SASLUser:        "",
				SASLPassword:    "secret",
			},
			wantErr: true,
		},
		{
			name: "SASL enabled valid credentials",
			cfg: Config{
				Port:            "8080",
				RedpandaBrokers: []string{"localhost:9092"},
				TopicName:       "xmc-metrics",
				MinPartitions:   128,
				MinReplicas:     3,
				SASLEnabled:     true,
				SASLMechanism:   "SCRAM-SHA-256",
				SASLUser:        "user",
				SASLPassword:    "secret",
			},
			wantErr: false,
		},
		{
			name: "SASL enabled invalid mechanism",
			cfg: Config{
				Port:            "8080",
				RedpandaBrokers: []string{"localhost:9092"},
				TopicName:       "xmc-metrics",
				MinPartitions:   128,
				MinReplicas:     3,
				SASLEnabled:     true,
				SASLMechanism:   "GSSAPI",
				SASLUser:        "user",
				SASLPassword:    "secret",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Config.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLoadSASLCredentialsFromFile(t *testing.T) {
	tempDir := t.TempDir()
	userFile := filepath.Join(tempDir, "username")
	passFile := filepath.Join(tempDir, "password")

	if err := os.WriteFile(userFile, []byte("  testuser \n"), 0600); err != nil {
		t.Fatalf("Failed to write username file: %v", err)
	}
	if err := os.WriteFile(passFile, []byte("testpass"), 0600); err != nil {
		t.Fatalf("Failed to write password file: %v", err)
	}

	cfg := &Config{
		SASLEnabled:        true,
		SASLCredentialsDir: tempDir,
	}

	if err := loadSASLCredentials(cfg); err != nil {
		t.Fatalf("loadSASLCredentials() returned unexpected error: %v", err)
	}

	if cfg.SASLUser != "testuser" {
		t.Errorf("cfg.SASLUser = %q, want %q", cfg.SASLUser, "testuser")
	}
	if cfg.SASLPassword != "testpass" {
		t.Errorf("cfg.SASLPassword = %q, want %q", cfg.SASLPassword, "testpass")
	}
}
