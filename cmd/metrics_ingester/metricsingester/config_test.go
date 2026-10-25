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

package metricsingester

import (
	"flag"
	"strings"
	"testing"
	"time"
)

// baseFlags returns flags that produce a valid Config, for tests to mutate one field at a time.
func baseFlags() Flags {
	return Flags{
		ProjectID:      "test-project",
		SubscriptionID: "xmc-metrics-ingester",
		BatchSize:      defaultBatchSize,
		BatchInterval:  defaultBatchInterval,
	}
}

// baseResolvedConfig returns the Config produced by LoadConfig(baseFlags()) with a clean env.
func baseResolvedConfig() Config {
	return Config{
		Port:              defaultPort,
		ProjectID:         "test-project",
		SubscriptionID:    "xmc-metrics-ingester",
		MetricsStore:      StoreClickHouse,
		BigQueryProjectID: "test-project",
		BigQueryDataset:   defaultBigQueryDataset,
		BigQueryTable:     defaultBigQueryTable,
		BatchSize:         defaultBatchSize,
		BatchInterval:     defaultBatchInterval,
	}
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"PORT",
		"GOOGLE_CLOUD_PROJECT",
		"PUBSUB_SUBSCRIPTION_ID",
		"METRICS_STORE",
		"BIGQUERY_PROJECT_ID",
		"BIGQUERY_DATASET",
		"BIGQUERY_TABLE",
	} {
		t.Setenv(key, "")
	}
}

func TestRegisterFlagsDefaults(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	f := RegisterFlags(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	want := Flags{BatchSize: defaultBatchSize, BatchInterval: defaultBatchInterval}
	if *f != want {
		t.Errorf("RegisterFlags() defaults = %+v, want %+v", *f, want)
	}
}

func TestRegisterFlagsParsesValues(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	f := RegisterFlags(fs)
	args := []string{
		"-port=9090",
		"-project_id=p",
		"-subscription_id=s",
		"-metrics_store=bigquery",
		"-bigquery_project_id=bqp",
		"-bigquery_dataset=bqd",
		"-bigquery_table=bqt",
		"-batch_size=5",
		"-batch_interval=2s",
	}
	if err := fs.Parse(args); err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	want := Flags{
		Port:              "9090",
		ProjectID:         "p",
		SubscriptionID:    "s",
		MetricsStore:      "bigquery",
		BigQueryProjectID: "bqp",
		BigQueryDataset:   "bqd",
		BigQueryTable:     "bqt",
		BatchSize:         5,
		BatchInterval:     2 * time.Second,
	}
	if *f != want {
		t.Errorf("RegisterFlags() parsed = %+v, want %+v", *f, want)
	}
}

func TestLoadConfigResolvesPrecedence(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*Flags)
		env      map[string]string
		wantDiff func(*Config)
	}{
		{
			name: "flags only, default port and clickhouse store",
		},
		{
			name:   "environment fills unset flags including bigquery settings",
			mutate: func(f *Flags) { f.ProjectID, f.SubscriptionID = "", "" },
			env: map[string]string{
				"PORT":                   "7070",
				"GOOGLE_CLOUD_PROJECT":   "env-project",
				"PUBSUB_SUBSCRIPTION_ID": "env-sub",
				"METRICS_STORE":          " BIGQUERY ",
				"BIGQUERY_PROJECT_ID":    "bq-env-project",
				"BIGQUERY_DATASET":       "bq_env_ds",
				"BIGQUERY_TABLE":         "bq_env_tbl",
			},
			wantDiff: func(c *Config) {
				c.Port = "7070"
				c.ProjectID = "env-project"
				c.SubscriptionID = "env-sub"
				c.MetricsStore = StoreBigQuery
				c.BigQueryProjectID = "bq-env-project"
				c.BigQueryDataset = "bq_env_ds"
				c.BigQueryTable = "bq_env_tbl"
			},
		},
		{
			name: "bigquery project falls back to project_id when unset",
			mutate: func(f *Flags) {
				f.MetricsStore = StoreBigQuery
			},
			wantDiff: func(c *Config) {
				c.MetricsStore = StoreBigQuery
			},
		},
		{
			name: "explicit flags beat environment",
			mutate: func(f *Flags) {
				f.Port = "9090"
				f.MetricsStore = StoreBigQuery
				f.BigQueryProjectID = "bq-flag-proj"
				f.BigQueryDataset = "bq_flag_ds"
				f.BigQueryTable = "bq_flag_tbl"
			},
			env: map[string]string{
				"PORT":                   "7070",
				"GOOGLE_CLOUD_PROJECT":   "env-project",
				"PUBSUB_SUBSCRIPTION_ID": "env-sub",
				"METRICS_STORE":          "clickhouse",
				"BIGQUERY_PROJECT_ID":    "bq-env-proj",
				"BIGQUERY_DATASET":       "bq_env_ds",
				"BIGQUERY_TABLE":         "bq_env_tbl",
			},
			wantDiff: func(c *Config) {
				c.Port = "9090"
				c.MetricsStore = StoreBigQuery
				c.BigQueryProjectID = "bq-flag-proj"
				c.BigQueryDataset = "bq_flag_ds"
				c.BigQueryTable = "bq_flag_tbl"
			},
		},
		{
			name:   "whitespace is trimmed",
			mutate: func(f *Flags) { f.ProjectID = "  padded  " },
			wantDiff: func(c *Config) {
				c.ProjectID = "padded"
				c.BigQueryProjectID = "padded"
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearConfigEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			f := baseFlags()
			if tc.mutate != nil {
				tc.mutate(&f)
			}
			want := baseResolvedConfig()
			if tc.wantDiff != nil {
				tc.wantDiff(&want)
			}

			got, err := LoadConfig(&f)
			if err != nil {
				t.Fatalf("LoadConfig() error = %v", err)
			}
			if *got != want {
				t.Errorf("LoadConfig() = %+v, want %+v", *got, want)
			}
		})
	}
}

func TestLoadConfigRejectsInvalid(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Flags)
		wantErr string
	}{
		{name: "missing project", mutate: func(f *Flags) { f.ProjectID = "" }, wantErr: "project_id is required"},
		{name: "blank project", mutate: func(f *Flags) { f.ProjectID = "   " }, wantErr: "project_id is required"},
		{name: "missing subscription", mutate: func(f *Flags) { f.SubscriptionID = "" }, wantErr: "subscription_id is required"},
		{name: "zero batch size", mutate: func(f *Flags) { f.BatchSize = 0 }, wantErr: "batch_size must be positive"},
		{name: "negative batch interval", mutate: func(f *Flags) { f.BatchInterval = -time.Second }, wantErr: "batch_interval must be positive"},
		{name: "unsupported store", mutate: func(f *Flags) { f.MetricsStore = "postgres" }, wantErr: "unsupported metrics_store"},
		{
			name:    "bigquery project contains slash",
			mutate:  func(f *Flags) { f.MetricsStore = StoreBigQuery; f.BigQueryProjectID = "projects/p" },
			wantErr: "bigquery_project_id",
		},
		{
			name:    "bigquery dataset contains slash",
			mutate:  func(f *Flags) { f.MetricsStore = StoreBigQuery; f.BigQueryDataset = "datasets/d" },
			wantErr: "bigquery_dataset",
		},
		{
			name:    "bigquery table contains slash",
			mutate:  func(f *Flags) { f.MetricsStore = StoreBigQuery; f.BigQueryTable = "tables/t" },
			wantErr: "bigquery_table",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearConfigEnv(t)
			f := baseFlags()
			tc.mutate(&f)

			_, err := LoadConfig(&f)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("LoadConfig() error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestFirstNonEmpty(t *testing.T) {
	tests := []struct {
		values []string
		want   string
	}{
		{values: nil, want: ""},
		{values: []string{"", " ", "b", "c"}, want: "b"},
		{values: []string{" a "}, want: "a"},
	}
	for _, tc := range tests {
		if got := firstNonEmpty(tc.values...); got != tc.want {
			t.Errorf("firstNonEmpty(%q) = %q, want %q", tc.values, got, tc.want)
		}
	}
}
