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
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config holds the runtime configuration for the Ingestion Gateway.
type Config struct {
	Port                  string
	RedpandaBrokers       []string
	TopicName             string
	MinPartitions         int
	MinReplicas           int
	SASLEnabled           bool
	SASLMechanism         string
	SASLUser              string
	SASLPassword          string
	SASLCredentialsDir    string
	TLSEnabled            bool
	TLSCertFile           string
	TLSKeyFile            string
	TLSCaFile             string
	TLSInsecureSkipVerify bool
	ProjectID             string
}

// Flags holds the CLI flag value bindings for FlagSet registration.
type Flags struct {
	Port                  string
	RedpandaBrokers       string
	TopicName             string
	MinPartitions         int
	MinReplicas           int
	SASLEnabled           bool
	SASLMechanism         string
	SASLUser              string
	SASLPassword          string
	SASLCredentialsDir    string
	TLSEnabled            bool
	TLSCertFile           string
	TLSKeyFile            string
	TLSCaFile             string
	TLSInsecureSkipVerify bool
	ProjectID             string
}

// RegisterFlags registers the ingestion gateway CLI flags on the given FlagSet and returns a Flags struct binding.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	fs.StringVar(&f.Port, "port", "8080", "The HTTP server port to listen on.")
	fs.StringVar(&f.RedpandaBrokers, "redpanda_brokers", "localhost:9092", "Comma-separated Redpanda broker addresses.")
	fs.StringVar(&f.TopicName, "topic_name", "xmc-metrics", "The Redpanda metrics topic name.")
	fs.IntVar(&f.MinPartitions, "min_partitions", 128, "Minimum required partition count for topic topology assertion.")
	fs.IntVar(&f.MinReplicas, "min_replicas", 3, "Minimum required replica count for topic topology assertion.")
	fs.BoolVar(&f.SASLEnabled, "sasl_enabled", false, "Whether SASL authentication is enabled.")
	fs.StringVar(&f.SASLMechanism, "sasl_mechanism", "SCRAM-SHA-256", "SASL mechanism (SCRAM-SHA-256, SCRAM-SHA-512, PLAIN).")
	fs.StringVar(&f.SASLUser, "sasl_user", "", "SASL username (if omitted, loaded from sasl_credentials_dir/username).")
	fs.StringVar(&f.SASLPassword, "sasl_password", "", "SASL password (if omitted, loaded from sasl_credentials_dir/password).")
	fs.StringVar(&f.SASLCredentialsDir, "sasl_credentials_dir", "/etc/kafka/credentials", "Directory containing mounted SASL secret files.")
	fs.BoolVar(&f.TLSEnabled, "tls_enabled", false, "Whether TLS/mTLS is enabled for Redpanda connections.")
	fs.StringVar(&f.TLSCertFile, "tls_cert_file", "", "Path to TLS client certificate file for mTLS.")
	fs.StringVar(&f.TLSKeyFile, "tls_key_file", "", "Path to TLS client private key file for mTLS.")
	fs.StringVar(&f.TLSCaFile, "tls_ca_file", "", "Path to TLS CA certificate file for server verification.")
	fs.BoolVar(&f.TLSInsecureSkipVerify, "tls_insecure_skip_verify", false, "Skip TLS certificate verification (for testing only).")
	fs.StringVar(&f.ProjectID, "project_id", "", "GCP project ID for logging.")
	return f
}

// LoadConfig creates and validates a Config struct from optional Flags, environment variables, and mounted secrets.
func LoadConfig(optionalFlags ...*Flags) (*Config, error) {
	var f Flags
	if len(optionalFlags) > 0 && optionalFlags[0] != nil {
		f = *optionalFlags[0]
	}

	effectivePort := getEnvOrDefault(f.Port, "8080", "PORT")
	effectiveBrokersStr := getEnvOrDefault(f.RedpandaBrokers, "localhost:9092", "REDPANDA_BROKERS", "REDPANDA_BROKER")
	effectiveTopicName := getEnvOrDefault(f.TopicName, "xmc-metrics", "TOPIC_NAME", "REDPANDA_TOPIC")

	minParts := f.MinPartitions
	if minParts == 0 {
		minParts = 128
	}
	minReps := f.MinReplicas
	if minReps == 0 {
		minReps = 3
	}

	brokers := parseBrokers(effectiveBrokersStr)

	cfg := &Config{
		Port:                  strings.TrimSpace(effectivePort),
		RedpandaBrokers:       brokers,
		TopicName:             strings.TrimSpace(effectiveTopicName),
		MinPartitions:         minParts,
		MinReplicas:           minReps,
		SASLEnabled:           f.SASLEnabled,
		SASLMechanism:         strings.ToUpper(strings.TrimSpace(f.SASLMechanism)),
		SASLUser:              strings.TrimSpace(f.SASLUser),
		SASLPassword:          strings.TrimSpace(f.SASLPassword),
		SASLCredentialsDir:    strings.TrimSpace(f.SASLCredentialsDir),
		TLSEnabled:            f.TLSEnabled,
		TLSCertFile:           strings.TrimSpace(f.TLSCertFile),
		TLSKeyFile:            strings.TrimSpace(f.TLSKeyFile),
		TLSCaFile:             strings.TrimSpace(f.TLSCaFile),
		TLSInsecureSkipVerify: f.TLSInsecureSkipVerify,
		ProjectID:             strings.TrimSpace(f.ProjectID),
	}

	if cfg.SASLEnabled {
		if err := loadSASLCredentials(cfg); err != nil {
			return nil, fmt.Errorf("failed to load SASL credentials: %w", err)
		}
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return cfg, nil
}

// getEnvOrDefault returns flagVal if set (differs from default or non-empty), otherwise falls back to environment variables.
func getEnvOrDefault(flagVal, defaultVal string, envKeys ...string) string {
	if flagVal != defaultVal && strings.TrimSpace(flagVal) != "" {
		return flagVal
	}
	for _, envKey := range envKeys {
		if envVal := strings.TrimSpace(os.Getenv(envKey)); envVal != "" {
			return envVal
		}
	}
	return flagVal
}

func parseBrokers(brokersStr string) []string {
	if strings.TrimSpace(brokersStr) == "" {
		return nil
	}
	rawBrokers := strings.Split(brokersStr, ",")
	var brokers []string
	for _, b := range rawBrokers {
		trimmed := strings.TrimSpace(b)
		if trimmed != "" {
			brokers = append(brokers, trimmed)
		}
	}
	return brokers
}

func loadSASLCredentials(cfg *Config) error {
	if cfg.SASLUser == "" {
		user, err := readSecretFile(cfg.SASLCredentialsDir, "username")
		if err != nil {
			return err
		}
		cfg.SASLUser = user
	}
	if cfg.SASLPassword == "" {
		pass, err := readSecretFile(cfg.SASLCredentialsDir, "password")
		if err != nil {
			return err
		}
		cfg.SASLPassword = pass
	}
	return nil
}

func readSecretFile(dir, filename string) (string, error) {
	path := filepath.Join(dir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read secret file %s: %w", path, err)
	}
	return strings.TrimSpace(string(data)), nil
}

// Validate checks configuration field constraints.
func (c *Config) Validate() error {
	p, err := strconv.Atoi(c.Port)
	if err != nil || p <= 0 || p > 65535 {
		return fmt.Errorf("invalid port %q: must be between 1 and 65535", c.Port)
	}
	if len(c.RedpandaBrokers) == 0 {
		return fmt.Errorf("redpanda_brokers cannot be empty")
	}
	if strings.TrimSpace(c.TopicName) == "" {
		return fmt.Errorf("topic_name cannot be empty")
	}
	if c.MinPartitions <= 0 {
		return fmt.Errorf("min_partitions must be > 0")
	}
	if c.MinReplicas <= 0 {
		return fmt.Errorf("min_replicas must be > 0")
	}
	if c.SASLEnabled {
		mech := strings.ToUpper(strings.TrimSpace(c.SASLMechanism))
		switch mech {
		case "SCRAM-SHA-256", "SCRAM-SHA-512", "PLAIN":
			// Supported SASL mechanisms
		default:
			return fmt.Errorf("unsupported sasl_mechanism %q: must be one of SCRAM-SHA-256, SCRAM-SHA-512, PLAIN", c.SASLMechanism)
		}
		if c.SASLUser == "" {
			return fmt.Errorf("sasl_user is required when sasl_enabled is true")
		}
		if c.SASLPassword == "" {
			return fmt.Errorf("sasl_password is required when sasl_enabled is true")
		}
	}
	if c.TLSEnabled {
		if (c.TLSCertFile != "" && c.TLSKeyFile == "") || (c.TLSCertFile == "" && c.TLSKeyFile != "") {
			return fmt.Errorf("tls_cert_file and tls_key_file must both be provided for mTLS")
		}
	}
	return nil
}
