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
	"cmp"
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
	MessageBus            string
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
	EnableComponents      bool
	ComponentsNamespace   string
}

// Flags holds the CLI flag value bindings for FlagSet registration.
type Flags struct {
	Port                  string
	MessageBus            string
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
	EnableComponents      bool
	ComponentsNamespace   string
}

// RegisterFlags registers the ingestion gateway CLI flags on the given FlagSet and returns a Flags struct binding.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	fs.StringVar(&f.Port, "port", "8080", "The HTTP server port to listen on.")
	// Defaulted to empty rather than to MessageBusRedpanda so that LoadConfig can tell "unset" from
	// "explicitly redpanda" and let an explicit flag win over MESSAGE_BUS. The effective default is
	// applied in LoadConfig, after the environment has had its turn.
	fs.StringVar(&f.MessageBus, "message_bus", "", "Message bus backend to publish metrics to: redpanda or pubsub (default redpanda).")
	fs.StringVar(&f.RedpandaBrokers, "redpanda_brokers", "localhost:9092", "Comma-separated Redpanda broker addresses. Ignored when message_bus is pubsub.")
	fs.StringVar(&f.TopicName, "topic_name", "xmc-metrics", "The metrics topic name: a Redpanda topic, or a Pub/Sub topic ID when message_bus is pubsub.")
	fs.IntVar(&f.MinPartitions, "min_partitions", 128, "Minimum required partition count for topic topology assertion. Ignored when message_bus is pubsub.")
	fs.IntVar(&f.MinReplicas, "min_replicas", 3, "Minimum required replica count for topic topology assertion. Ignored when message_bus is pubsub.")
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
	fs.StringVar(&f.ProjectID, "project_id", "", "GCP project ID for logging, and for Pub/Sub publishing when message_bus is pubsub.")
	fs.BoolVar(&f.EnableComponents, "enable_components", false, "Whether to resolve Redpanda connection parameters from the redpanda Component CR.")
	fs.StringVar(&f.ComponentsNamespace, "components_namespace", "xmc", "Kubernetes namespace containing the redpanda Component CR.")
	return f
}

// LoadConfig creates and validates a Config struct from optional Flags, environment variables, and mounted secrets.
func LoadConfig(optionalFlags ...*Flags) (*Config, error) {
	var f Flags
	if len(optionalFlags) > 0 && optionalFlags[0] != nil {
		f = *optionalFlags[0]
	}

	effectivePort := getEnvOrDefault(f.Port, "8080", "PORT")
	// Passing "" as the default keeps the flag's own value distinguishable from its absence, so the
	// precedence is flag, then environment, then the built-in default applied below. Passing
	// MessageBusRedpanda here instead would make -message_bus=redpanda look unset and let
	// MESSAGE_BUS=pubsub silently redirect an explicitly configured gateway.
	effectiveMessageBus := cmp.Or(strings.TrimSpace(getEnvOrDefault(f.MessageBus, "", "MESSAGE_BUS")), MessageBusRedpanda)
	effectiveBrokersStr := getEnvOrDefault(f.RedpandaBrokers, "localhost:9092", "REDPANDA_BROKERS", "REDPANDA_BROKER")
	effectiveTopicName := getEnvOrDefault(f.TopicName, "xmc-metrics", "TOPIC_NAME", "REDPANDA_TOPIC")
	effectiveComponentsNS := getEnvOrDefault(f.ComponentsNamespace, "xmc", "COMPONENTS_NAMESPACE")
	effectiveEnableComponents := resolveBoolFlagOrEnv(f.EnableComponents, "ENABLE_COMPONENTS")

	cfg := &Config{
		Port:                  strings.TrimSpace(effectivePort),
		MessageBus:            strings.ToLower(effectiveMessageBus),
		RedpandaBrokers:       parseBrokers(effectiveBrokersStr),
		TopicName:             strings.TrimSpace(effectiveTopicName),
		MinPartitions:         cmp.Or(f.MinPartitions, 128),
		MinReplicas:           cmp.Or(f.MinReplicas, 3),
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
		EnableComponents:      effectiveEnableComponents,
		ComponentsNamespace:   strings.TrimSpace(effectiveComponentsNS),
	}

	// SASL is a Kafka wire-protocol concept. Skip the secret file reads in Pub/Sub mode so that a
	// stray sasl_enabled surfaces as the clear validation error below, not a missing-file error.
	if cfg.SASLEnabled && cfg.messageBusOrDefault() != MessageBusPubSub {
		if err := loadSASLCredentials(cfg); err != nil {
			return nil, fmt.Errorf("failed to load SASL credentials: %w", err)
		}
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return cfg, nil
}

func resolveBoolFlagOrEnv(flagVal bool, envKey string) bool {
	if flagVal {
		return true
	}
	b, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(envKey)))
	return err == nil && b
}

// getEnvOrDefault returns flagVal if set (differs from default or non-empty), otherwise falls back to environment variables or defaultVal.
func getEnvOrDefault(flagVal, defaultVal string, envKeys ...string) string {
	if flagVal != defaultVal && strings.TrimSpace(flagVal) != "" {
		return flagVal
	}
	for _, envKey := range envKeys {
		if envVal := strings.TrimSpace(os.Getenv(envKey)); envVal != "" {
			return envVal
		}
	}
	return defaultVal
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

// Validate checks configuration field constraints shared by every backend, then delegates to the
// checks specific to the selected message bus.
func (c *Config) Validate() error {
	p, err := strconv.Atoi(c.Port)
	if err != nil || p <= 0 || p > 65535 {
		return fmt.Errorf("invalid port %q: must be between 1 and 65535", c.Port)
	}
	if strings.TrimSpace(c.TopicName) == "" {
		return fmt.Errorf("topic_name cannot be empty")
	}
	if c.messageBusOrDefault() == MessageBusPubSub {
		return c.validatePubSub()
	}
	if c.messageBusOrDefault() == MessageBusRedpanda {
		return c.validateRedpanda()
	}
	return errUnsupportedMessageBus(c.MessageBus)
}

// messageBusOrDefault resolves the selected backend, treating an unset value as the default.
//
// LoadConfig always populates MessageBus; this covers Config values built directly, notably in tests.
func (c *Config) messageBusOrDefault() string {
	if c.MessageBus == "" {
		return MessageBusRedpanda
	}
	return c.MessageBus
}

// validateRedpanda checks the broker addressing, topology thresholds, and Kafka wire-protocol
// authentication settings that only apply to the Redpanda backend.
func (c *Config) validateRedpanda() error {
	if len(c.RedpandaBrokers) == 0 {
		return fmt.Errorf("redpanda_brokers cannot be empty")
	}
	if c.MinPartitions <= 0 {
		return fmt.Errorf("min_partitions must be > 0")
	}
	if c.MinReplicas <= 0 {
		return fmt.Errorf("min_replicas must be > 0")
	}
	if err := c.validateSASL(); err != nil {
		return err
	}
	return c.validateTLS()
}

// validatePubSub checks the Pub/Sub backend settings.
//
// Broker addresses, partition and replica counts, SASL, and TLS have no Pub/Sub equivalent:
// durability and per-key ordering are service-managed and transport security is not configurable.
// Setting them is rejected rather than ignored so an operator is never misled about what is in
// effect.
func (c *Config) validatePubSub() error {
	if strings.TrimSpace(c.ProjectID) == "" {
		return fmt.Errorf("project_id is required when message_bus is %q", MessageBusPubSub)
	}
	if c.SASLEnabled {
		return fmt.Errorf("sasl_enabled is not supported when message_bus is %q", MessageBusPubSub)
	}
	if c.TLSEnabled {
		return fmt.Errorf("tls_enabled is not supported when message_bus is %q", MessageBusPubSub)
	}
	return nil
}

func (c *Config) validateSASL() error {
	if !c.SASLEnabled {
		return nil
	}
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
	return nil
}

func (c *Config) validateTLS() error {
	if !c.TLSEnabled {
		return nil
	}
	if (c.TLSCertFile != "" && c.TLSKeyFile == "") || (c.TLSCertFile == "" && c.TLSKeyFile != "") {
		return fmt.Errorf("tls_cert_file and tls_key_file must both be provided for mTLS")
	}
	return nil
}
