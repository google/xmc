// Copyright 2025 Google LLC
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
package main

import (
	"flag"
	"fmt"
	"os"
	"time"
)

// Config holds the configuration for the log monitor application.
type Config struct {
	ProjectID                   string
	SubscriptionID              string
	Port                        string
	BatchSize                   int
	BatchInterval               time.Duration
	SpannerDatabaseName         string
	QuotaProjectID              string
	LogKeyword                  string
	MaxConcurrentUpdates        int
	StatusMessageHistoryEnabled bool
}

var (
	projectID                   string
	subscriptionID              string
	port                        string
	batchSize                   int
	batchInterval               time.Duration
	spannerDatabaseName         string
	quotaProjectID              string
	logKeyword                  string
	maxConcurrentUpdates        int
	statusMessageHistoryEnabled bool
)

func init() {
	defaultPort := os.Getenv("PORT")
	if defaultPort == "" {
		defaultPort = "8080"
	}
	flag.StringVar(&projectID, "project_id", os.Getenv("GOOGLE_CLOUD_PROJECT"), "The GCP project ID.")
	flag.StringVar(&subscriptionID, "subscription_id", os.Getenv("PUBSUB_SUBSCRIPTION_ID"), "The Pub/Sub subscription ID.")
	flag.StringVar(&port, "port", defaultPort, "The HTTP server port to listen on.")
	flag.IntVar(&batchSize, "batch_size", 1000, "The maximum number of messages to process in a batch.")
	flag.DurationVar(&batchInterval, "batch_interval", 250*time.Millisecond, "The maximum time to wait before processing a batch.")
	flag.StringVar(&spannerDatabaseName, "spanner_database_name", "", "The name of the Spanner database.")
	flag.StringVar(&quotaProjectID, "quota_project_id", "", "The ID of the quota project to use.")
	flag.StringVar(&logKeyword, "log_keyword", "XMC LOG:", "The keyword that is used in logging sink to filter logs.")
	flag.IntVar(&maxConcurrentUpdates, "max_concurrent_updates", 10, "The maximum number of concurrent updates to Spanner.")
	flag.BoolVar(&statusMessageHistoryEnabled, "status_message_history_enabled", false, "Whether to enable status message history.")
}

// LoadConfig validates and returns the configuration loaded from flags and environment variables.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		ProjectID:                   projectID,
		SubscriptionID:              subscriptionID,
		Port:                        port,
		BatchSize:                   batchSize,
		BatchInterval:               batchInterval,
		SpannerDatabaseName:         spannerDatabaseName,
		QuotaProjectID:              quotaProjectID,
		LogKeyword:                  logKeyword,
		MaxConcurrentUpdates:        maxConcurrentUpdates,
		StatusMessageHistoryEnabled: statusMessageHistoryEnabled,
	}

	if cfg.ProjectID == "" {
		return nil, fmt.Errorf("project_id is required")
	}
	if cfg.SubscriptionID == "" {
		return nil, fmt.Errorf("subscription_id is required")
	}
	if cfg.BatchSize <= 0 {
		return nil, fmt.Errorf("batch_size must be positive")
	}
	if cfg.BatchInterval <= 0 {
		return nil, fmt.Errorf("batch_interval must be positive")
	}
	if cfg.MaxConcurrentUpdates <= 0 {
		return nil, fmt.Errorf("max_concurrent_updates must be positive")
	}
	if cfg.SpannerDatabaseName == "" {
		return nil, fmt.Errorf("spanner_database_name must be set")
	}
	if cfg.QuotaProjectID == "" {
		return nil, fmt.Errorf("quota_project_id must be set")
	}

	return cfg, nil
}
