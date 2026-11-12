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

package ingestiongateway

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
		{
			name: "Unsupported message bus",
			cfg: Config{
				Port:            "8080",
				MessageBus:      "rabbitmq",
				RedpandaBrokers: []string{"localhost:9092"},
				TopicName:       "xmc-metrics",
				MinPartitions:   128,
				MinReplicas:     3,
			},
			wantErr: true,
		},
		{
			name: "Valid Pub/Sub config",
			cfg: Config{
				Port:       "8080",
				MessageBus: MessageBusPubSub,
				TopicName:  "xmc-metrics",
				ProjectID:  "my-project",
			},
			wantErr: false,
		},
		{
			name: "Pub/Sub without project id",
			cfg: Config{
				Port:       "8080",
				MessageBus: MessageBusPubSub,
				TopicName:  "xmc-metrics",
			},
			wantErr: true,
		},
		{
			name: "Pub/Sub ignores broker topology settings",
			cfg: Config{
				Port:          "8080",
				MessageBus:    MessageBusPubSub,
				TopicName:     "xmc-metrics",
				ProjectID:     "my-project",
				MinPartitions: 0,
				MinReplicas:   0,
			},
			wantErr: false,
		},
		{
			name: "Pub/Sub rejects SASL",
			cfg: Config{
				Port:          "8080",
				MessageBus:    MessageBusPubSub,
				TopicName:     "xmc-metrics",
				ProjectID:     "my-project",
				SASLEnabled:   true,
				SASLMechanism: "SCRAM-SHA-256",
				SASLUser:      "user",
				SASLPassword:  "secret",
			},
			wantErr: true,
		},
		{
			name: "Pub/Sub rejects TLS",
			cfg: Config{
				Port:       "8080",
				MessageBus: MessageBusPubSub,
				TopicName:  "xmc-metrics",
				ProjectID:  "my-project",
				TLSEnabled: true,
			},
			wantErr: true,
		},
		{
			name: "Redpanda without project id is valid",
			cfg: Config{
				Port:            "8080",
				MessageBus:      MessageBusRedpanda,
				RedpandaBrokers: []string{"localhost:9092"},
				TopicName:       "xmc-metrics",
				MinPartitions:   128,
				MinReplicas:     3,
			},
			wantErr: false,
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

func TestRegisterFlags_MessageBusDefault(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	f := RegisterFlags(fs)

	if err := fs.Parse(nil); err != nil {
		t.Fatalf("failed to parse an empty flag set: %v", err)
	}
	// The flag default is empty on purpose: LoadConfig needs to tell "unset" from "explicitly
	// redpanda" to give an explicit flag precedence over MESSAGE_BUS. The redpanda default is
	// applied in LoadConfig, which TestLoadConfig_MessageBus covers.
	if f.MessageBus != "" {
		t.Errorf("default -message_bus = %q, want %q", f.MessageBus, "")
	}

	cfg, err := LoadConfig(f)
	if err != nil {
		t.Fatalf("LoadConfig() unexpected error: %v", err)
	}
	if cfg.MessageBus != MessageBusRedpanda {
		t.Errorf("effective message bus with no flag and no environment = %q, want %q", cfg.MessageBus, MessageBusRedpanda)
	}
}

func TestLoadConfig_MessageBus(t *testing.T) {
	tests := []struct {
		name  string
		flags *Flags
		env   map[string]string
		want  string
	}{
		{
			name:  "Defaults to redpanda",
			flags: &Flags{ProjectID: "my-project"},
			want:  MessageBusRedpanda,
		},
		{
			name:  "Flag selects pubsub",
			flags: &Flags{MessageBus: MessageBusPubSub, ProjectID: "my-project"},
			want:  MessageBusPubSub,
		},
		{
			name:  "Value is normalized",
			flags: &Flags{MessageBus: "  PubSub  ", ProjectID: "my-project"},
			want:  MessageBusPubSub,
		},
		{
			name:  "MESSAGE_BUS overrides the default",
			flags: &Flags{ProjectID: "my-project"},
			env:   map[string]string{"MESSAGE_BUS": MessageBusPubSub},
			want:  MessageBusPubSub,
		},
		{
			// The whole reason -message_bus defaults to "" rather than to redpanda: an operator who
			// spells out the default must not be silently redirected to another bus by the
			// environment. Publishing to the wrong destination is not a recoverable misconfiguration.
			name:  "Explicit redpanda flag wins over MESSAGE_BUS",
			flags: &Flags{MessageBus: MessageBusRedpanda, ProjectID: "my-project"},
			env:   map[string]string{"MESSAGE_BUS": MessageBusPubSub},
			want:  MessageBusRedpanda,
		},
		{
			name:  "Explicit pubsub flag wins over MESSAGE_BUS",
			flags: &Flags{MessageBus: MessageBusPubSub, ProjectID: "my-project"},
			env:   map[string]string{"MESSAGE_BUS": "rabbitmq"},
			want:  MessageBusPubSub,
		},
		{
			name:  "Blank MESSAGE_BUS falls through to the default",
			flags: &Flags{ProjectID: "my-project"},
			env:   map[string]string{"MESSAGE_BUS": "   "},
			want:  MessageBusRedpanda,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			cfg, err := LoadConfig(tt.flags)
			if err != nil {
				t.Fatalf("LoadConfig() unexpected error: %v", err)
			}
			if cfg.MessageBus != tt.want {
				t.Errorf("MessageBus = %q, want %q", cfg.MessageBus, tt.want)
			}
		})
	}
}

// TestLoadConfig_PubSubSkipsSASLSecretFiles asserts a stray sasl_enabled in Pub/Sub mode is reported
// as an unsupported setting rather than failing earlier on an unreadable credentials directory.
func TestLoadConfig_PubSubSkipsSASLSecretFiles(t *testing.T) {
	_, err := LoadConfig(&Flags{
		MessageBus:         MessageBusPubSub,
		ProjectID:          "my-project",
		SASLEnabled:        true,
		SASLCredentialsDir: filepath.Join(t.TempDir(), "does-not-exist"),
	})
	if err == nil {
		t.Fatalf("expected LoadConfig to reject sasl_enabled in Pub/Sub mode")
	}
	if !strings.Contains(err.Error(), "sasl_enabled is not supported") {
		t.Errorf("LoadConfig() error = %v, want it to reject sasl_enabled", err)
	}
}

func TestLoadConfig_UnsupportedMessageBus(t *testing.T) {
	if _, err := LoadConfig(&Flags{MessageBus: "rabbitmq"}); err == nil {
		t.Fatalf("expected LoadConfig to reject an unsupported message bus")
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

func TestGetEnvOrDefault(t *testing.T) {
	t.Setenv("TEST_KEY_1", "env_val_1")
	t.Setenv("TEST_KEY_2", "env_val_2")

	if got := getEnvOrDefault("custom_flag", "default_flag", "TEST_KEY_1"); got != "custom_flag" {
		t.Errorf("getEnvOrDefault() = %q, want %q", got, "custom_flag")
	}
	if got := getEnvOrDefault("default_flag", "default_flag", "TEST_KEY_1"); got != "env_val_1" {
		t.Errorf("getEnvOrDefault() = %q, want %q", got, "env_val_1")
	}
	if got := getEnvOrDefault("default_flag", "default_flag", "NON_EXISTENT", "TEST_KEY_2"); got != "env_val_2" {
		t.Errorf("getEnvOrDefault() = %q, want %q", got, "env_val_2")
	}
	if got := getEnvOrDefault("default_flag", "default_flag", "NON_EXISTENT"); got != "default_flag" {
		t.Errorf("getEnvOrDefault() = %q, want %q", got, "default_flag")
	}
}

func TestLoadConfig_ComponentsFlagsAndEnv(t *testing.T) {
	t.Run("defaults when flags and env are unset", func(t *testing.T) {
		cfg, err := LoadConfig(&Flags{})
		if err != nil {
			t.Fatalf("LoadConfig() unexpected error: %v", err)
		}
		if cfg.ComponentsNamespace != "xmc" {
			t.Errorf("ComponentsNamespace = %q, want %q", cfg.ComponentsNamespace, "xmc")
		}
	})

	t.Run("env variables configure ComponentsNamespace", func(t *testing.T) {
		t.Setenv("COMPONENTS_NAMESPACE", "custom-ns")
		cfg, err := LoadConfig(&Flags{})
		if err != nil {
			t.Fatalf("LoadConfig() unexpected error: %v", err)
		}
		if cfg.ComponentsNamespace != "custom-ns" {
			t.Errorf("ComponentsNamespace = %q, want %q", cfg.ComponentsNamespace, "custom-ns")
		}
	})

	t.Run("flags take precedence over defaults and deprecated -enable_components parses cleanly", func(t *testing.T) {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		flags := RegisterFlags(fs)
		if err := fs.Parse([]string{"-enable_components=false", "-components_namespace=flag-ns"}); err != nil {
			t.Fatalf("fs.Parse() with -enable_components=false failed: %v", err)
		}
		cfg, err := LoadConfig(flags)
		if err != nil {
			t.Fatalf("LoadConfig() unexpected error: %v", err)
		}
		if cfg.ComponentsNamespace != "flag-ns" {
			t.Errorf("ComponentsNamespace = %q, want %q", cfg.ComponentsNamespace, "flag-ns")
		}
	})
}

func TestConfigUsesRedpanda(t *testing.T) {
	var nilCfg *Config
	if nilCfg.UsesRedpanda() {
		t.Error("(*Config)(nil).UsesRedpanda() = true, want false")
	}
	for _, tc := range []struct {
		name string
		bus  string
		want bool
	}{
		{"empty defaults to redpanda", "", true},
		{"whitespace defaults to redpanda", "   ", true},
		{"explicit redpanda case-insensitive", "  RedPanda ", true},
		{"pubsub returns false", MessageBusPubSub, false},
		{"unsupported bus returns false", "rabbitmq", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{MessageBus: tc.bus}
			if got := cfg.UsesRedpanda(); got != tc.want {
				t.Errorf("(&Config{MessageBus: %q}).UsesRedpanda() = %v, want %v", tc.bus, got, tc.want)
			}
		})
	}
}
