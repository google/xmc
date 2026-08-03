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

// Package main implements the xmc-diag CLI diagnostic tool for ClickHouse and Redpanda.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"
	"xmanager_cloud/pkg/clickhouse"
)

// Config holds CLI flags and runtime parameters for xmc-diag.
type Config struct {
	ClickHouseHost     string
	ClickHousePort     int
	ClickHouseUser     string
	ClickHousePassword string
	ClickHouseDatabase string
	ClickHouseDSN      string

	RedpandaBrokers []string
	TopicName       string
	ConsumerGroup   string
	SASLEnabled     bool
	SASLMechanism   string
	SASLUser        string
	SASLPassword    string
	TLSEnabled      bool
	TLSInsecure     bool

	OutputFormat   string
	SkipSmokeTests bool
}

// DiagnosticStatus represents status of a check: HEALTHY, DEGRADED, FAILED.
type DiagnosticStatus string

const (
	StatusHealthy  DiagnosticStatus = "HEALTHY"
	StatusDegraded DiagnosticStatus = "DEGRADED"
	StatusFailed   DiagnosticStatus = "FAILED"
)

// PartitionLag holds offset details and lag for a single topic partition.
type PartitionLag struct {
	Partition int32 `json:"partition"`
	HighWater int64 `json:"high_watermark"`
	Committed int64 `json:"committed_offset"`
	Lag       int64 `json:"lag"`
}

// LagReport holds lag details for a consumer group.
type LagReport struct {
	ConsumerGroup string         `json:"consumer_group"`
	Topic         string         `json:"topic"`
	TotalLag      int64          `json:"total_lag"`
	Partitions    []PartitionLag `json:"partitions"`
}

// ClickHouseReport holds diagnostic results for ClickHouse.
type ClickHouseReport struct {
	Status        DiagnosticStatus `json:"status"`
	Connected     bool             `json:"connected"`
	Version       string           `json:"version,omitempty"`
	MissingTables []string         `json:"missing_tables,omitempty"`
	QueryLatency  string           `json:"query_latency,omitempty"`
	Error         string           `json:"error,omitempty"`
}

// RedpandaReport holds diagnostic results for Redpanda/Kafka.
type RedpandaReport struct {
	Status          DiagnosticStatus `json:"status"`
	Connected       bool             `json:"connected"`
	BrokerCount     int              `json:"broker_count"`
	TopicPartitions int              `json:"topic_partitions"`
	Lag             LagReport        `json:"lag_report"`
	PingLatency     string           `json:"ping_latency,omitempty"`
	Error           string           `json:"error,omitempty"`
}

// DiagReport aggregates diagnostic results across all infrastructure components.
type DiagReport struct {
	OverallStatus DiagnosticStatus `json:"overall_status"`
	Timestamp     time.Time        `json:"timestamp"`
	ClickHouse    ClickHouseReport `json:"clickhouse"`
	Redpanda      RedpandaReport   `json:"redpanda"`
}

// ClickHouseQuerier abstracts ClickHouse database queries for testing.
type ClickHouseQuerier interface {
	Ping(ctx context.Context) error
	SelectVersion(ctx context.Context) (string, error)
	CheckTablesExist(ctx context.Context, tables []string) ([]string, error)
	MeasureLatency(ctx context.Context) (time.Duration, error)
}

// RedpandaQuerier abstracts Redpanda/Kafka queries for testing.
type RedpandaQuerier interface {
	Ping(ctx context.Context) (time.Duration, error)
	ListTopics(ctx context.Context, topics ...string) (kadm.TopicDetails, error)
	ListEndOffsets(ctx context.Context, topics ...string) (kadm.ListedOffsets, error)
	FetchOffsets(ctx context.Context, group string) (kadm.OffsetResponses, error)
}

// RealClickHouseQuerier wraps driver.Conn.
type RealClickHouseQuerier struct {
	conn driver.Conn
}

func (r *RealClickHouseQuerier) Ping(ctx context.Context) error {
	return r.conn.Ping(ctx)
}

func (r *RealClickHouseQuerier) SelectVersion(ctx context.Context) (string, error) {
	var ver string
	err := r.conn.QueryRow(ctx, "SELECT version()").Scan(&ver)
	return ver, err
}

func (r *RealClickHouseQuerier) CheckTablesExist(ctx context.Context, tables []string) ([]string, error) {
	var missing []string
	for _, tbl := range tables {
		var count uint64
		err := r.conn.QueryRow(ctx, "SELECT count() FROM system.tables WHERE database = currentDatabase() AND name = ?", tbl).Scan(&count)
		if err != nil {
			return nil, fmt.Errorf("failed checking table %q: %w", tbl, err)
		}
		if count == 0 {
			missing = append(missing, tbl)
		}
	}
	return missing, nil
}

func (r *RealClickHouseQuerier) MeasureLatency(ctx context.Context) (time.Duration, error) {
	start := time.Now()
	var dummy int
	err := r.conn.QueryRow(ctx, "SELECT 1").Scan(&dummy)
	return time.Since(start), err
}

// RealRedpandaQuerier wraps kadm.Client.
type RealRedpandaQuerier struct {
	client *kadm.Client
}

func (r *RealRedpandaQuerier) Ping(ctx context.Context) (time.Duration, error) {
	start := time.Now()
	_, err := r.client.ListBrokers(ctx)
	return time.Since(start), err
}

func (r *RealRedpandaQuerier) ListTopics(ctx context.Context, topics ...string) (kadm.TopicDetails, error) {
	return r.client.ListTopics(ctx, topics...)
}

func (r *RealRedpandaQuerier) ListEndOffsets(ctx context.Context, topics ...string) (kadm.ListedOffsets, error) {
	return r.client.ListEndOffsets(ctx, topics...)
}

func (r *RealRedpandaQuerier) FetchOffsets(ctx context.Context, group string) (kadm.OffsetResponses, error) {
	return r.client.FetchOffsets(ctx, group)
}

// ParseFlags parses command-line arguments into Config.
func ParseFlags(args []string) (*Config, error) {
	fs := flag.NewFlagSet("xmc-diag", flag.ContinueOnError)

	var (
		chHost      = fs.String("clickhouse_host", "", "ClickHouse host (overrides CLICKHOUSE_HOST env)")
		chPort      = fs.Int("clickhouse_port", 9000, "ClickHouse native protocol port")
		chUser      = fs.String("clickhouse_user", "default", "ClickHouse user")
		chPassword  = fs.String("clickhouse_password", "", "ClickHouse password")
		chDatabase  = fs.String("clickhouse_database", "default", "ClickHouse database name")
		chDSN       = fs.String("clickhouse_dsn", "", "ClickHouse DSN string")
		brokersStr  = fs.String("redpanda_brokers", "localhost:9092", "Comma-separated Redpanda broker addresses")
		topicName   = fs.String("topic_name", "xmc-metrics", "Redpanda metrics topic name")
		consumerGrp = fs.String("consumer_group", "xmc-clickhouse-ingester", "Kafka consumer group name to inspect lag for")
		saslEnable  = fs.Bool("sasl_enabled", false, "Enable SASL authentication for Redpanda")
		saslMech    = fs.String("sasl_mechanism", "SCRAM-SHA-256", "SASL mechanism")
		saslUser    = fs.String("sasl_user", "", "SASL user")
		saslPass    = fs.String("sasl_password", "", "SASL password")
		tlsEnable   = fs.Bool("tls_enabled", false, "Enable TLS for Redpanda connection")
		tlsInsecure = fs.Bool("tls_insecure_skip_verify", false, "Skip TLS verification for Redpanda")
		outFormat   = fs.String("output", "text", "Output format: text or json")
		skipSmoke   = fs.Bool("skip_smoke_tests", false, "Skip latency performance smoke tests")
	)

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	brokers := strings.Split(*brokersStr, ",")
	for i, b := range brokers {
		brokers[i] = strings.TrimSpace(b)
	}

	return &Config{
		ClickHouseHost:     *chHost,
		ClickHousePort:     *chPort,
		ClickHouseUser:     *chUser,
		ClickHousePassword: *chPassword,
		ClickHouseDatabase: *chDatabase,
		ClickHouseDSN:      *chDSN,
		RedpandaBrokers:    brokers,
		TopicName:          *topicName,
		ConsumerGroup:      *consumerGrp,
		SASLEnabled:        *saslEnable,
		SASLMechanism:      *saslMech,
		SASLUser:           *saslUser,
		SASLPassword:       *saslPass,
		TLSEnabled:         *tlsEnable,
		TLSInsecure:        *tlsInsecure,
		OutputFormat:       strings.ToLower(*outFormat),
		SkipSmokeTests:     *skipSmoke,
	}, nil
}

// CheckClickHouse performs ClickHouse diagnostics using querier.
func CheckClickHouse(ctx context.Context, querier ClickHouseQuerier, skipSmoke bool) ClickHouseReport {
	if querier == nil {
		return ClickHouseReport{Status: StatusFailed, Error: "clickhouse querier not initialized"}
	}

	if err := querier.Ping(ctx); err != nil {
		return ClickHouseReport{Status: StatusFailed, Error: fmt.Sprintf("ping failed: %v", err)}
	}

	ver, err := querier.SelectVersion(ctx)
	if err != nil {
		return ClickHouseReport{Status: StatusFailed, Connected: true, Error: fmt.Sprintf("version check failed: %v", err)}
	}

	requiredTables := []string{"xmc_metrics", "xmc_metrics_queue", "xmc_metrics_mv"}
	missing, err := querier.CheckTablesExist(ctx, requiredTables)
	if err != nil {
		return ClickHouseReport{Status: StatusFailed, Connected: true, Version: ver, Error: fmt.Sprintf("schema check failed: %v", err)}
	}

	status := StatusHealthy
	if len(missing) > 0 {
		status = StatusDegraded
	}

	var latencyStr string
	if !skipSmoke {
		lat, err := querier.MeasureLatency(ctx)
		if err != nil {
			status = StatusDegraded
		} else {
			latencyStr = lat.String()
		}
	}

	return ClickHouseReport{
		Status:        status,
		Connected:     true,
		Version:       ver,
		MissingTables: missing,
		QueryLatency:  latencyStr,
	}
}

// CalculateConsumerLag computes partition lag and total lag from kadm topic details and consumer group offsets.
func CalculateConsumerLag(endOffsets kadm.ListedOffsets, groupOffsets kadm.OffsetResponses, topic, group string) LagReport {
	report := LagReport{
		ConsumerGroup: group,
		Topic:         topic,
		Partitions:    make([]PartitionLag, 0),
	}

	topEnds, ok := endOffsets[topic]
	if !ok {
		return report
	}

	topCommitted, ok := groupOffsets[topic]

	for pID, endOffset := range topEnds {
		if endOffset.Err != nil {
			continue
		}
		highWater := endOffset.Offset

		var committed int64
		if ok {
			if cOffset, exists := topCommitted[pID]; exists && cOffset.Err == nil {
				if cOffset.At >= 0 {
					committed = cOffset.At
				}
			}
		}

		lag := highWater - committed
		if lag < 0 {
			lag = 0
		}

		report.TotalLag += lag
		report.Partitions = append(report.Partitions, PartitionLag{
			Partition: pID,
			HighWater: highWater,
			Committed: committed,
			Lag:       lag,
		})
	}

	sort.Slice(report.Partitions, func(i, j int) bool {
		return report.Partitions[i].Partition < report.Partitions[j].Partition
	})

	return report
}

// CheckRedpanda performs Redpanda diagnostics using querier.
func CheckRedpanda(ctx context.Context, querier RedpandaQuerier, topic, group string, skipSmoke bool) RedpandaReport {
	if querier == nil {
		return RedpandaReport{Status: StatusFailed, Error: "redpanda querier not initialized"}
	}

	var pingLatStr string
	if !skipSmoke {
		lat, err := querier.Ping(ctx)
		if err != nil {
			return RedpandaReport{Status: StatusFailed, Error: fmt.Sprintf("ping failed: %v", err)}
		}
		pingLatStr = lat.String()
	}

	topics, err := querier.ListTopics(ctx, topic)
	if err != nil {
		return RedpandaReport{Status: StatusFailed, PingLatency: pingLatStr, Error: fmt.Sprintf("failed listing topics: %v", err)}
	}

	tDetail, ok := topics[topic]
	if !ok || tDetail.Err != nil {
		return RedpandaReport{Status: StatusFailed, Connected: true, PingLatency: pingLatStr, Error: fmt.Sprintf("topic %q not found or error", topic)}
	}

	endOffsets, err := querier.ListEndOffsets(ctx, topic)
	if err != nil {
		return RedpandaReport{
			Status:          StatusDegraded,
			Connected:       true,
			TopicPartitions: len(tDetail.Partitions),
			PingLatency:     pingLatStr,
			Error:           fmt.Sprintf("failed listing end offsets for topic %q: %v", topic, err),
		}
	}

	grpOffsets, err := querier.FetchOffsets(ctx, group)
	if err != nil {
		return RedpandaReport{
			Status:          StatusDegraded,
			Connected:       true,
			TopicPartitions: len(tDetail.Partitions),
			PingLatency:     pingLatStr,
			Error:           fmt.Sprintf("failed fetching consumer offsets for group %q: %v", group, err),
		}
	}

	lagRep := CalculateConsumerLag(endOffsets, grpOffsets, topic, group)

	return RedpandaReport{
		Status:          StatusHealthy,
		Connected:       true,
		TopicPartitions: len(tDetail.Partitions),
		Lag:             lagRep,
		PingLatency:     pingLatStr,
	}
}

// FormatReport converts DiagReport to formatted text or JSON output string.
func FormatReport(report *DiagReport, format string) (string, error) {
	if format == "json" {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed serializing report to json: %w", err)
		}
		return string(data), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== XMC Infrastructure Diagnostics (Status: %s) ===\n", report.OverallStatus))
	sb.WriteString(fmt.Sprintf("Timestamp: %s\n\n", report.Timestamp.Format(time.RFC3339)))

	sb.WriteString(fmt.Sprintf("[ClickHouse Diagnostics] Status: %s\n", report.ClickHouse.Status))
	sb.WriteString(fmt.Sprintf("  Connected: %t\n", report.ClickHouse.Connected))
	if report.ClickHouse.Version != "" {
		sb.WriteString(fmt.Sprintf("  Version: %s\n", report.ClickHouse.Version))
	}
	if len(report.ClickHouse.MissingTables) > 0 {
		sb.WriteString(fmt.Sprintf("  Missing Tables: %s\n", strings.Join(report.ClickHouse.MissingTables, ", ")))
	} else if report.ClickHouse.Connected {
		sb.WriteString("  Schema Tables: All required tables present (xmc_metrics, xmc_metrics_queue, xmc_metrics_mv)\n")
	}
	if report.ClickHouse.QueryLatency != "" {
		sb.WriteString(fmt.Sprintf("  Query Smoke Latency: %s\n", report.ClickHouse.QueryLatency))
	}
	if report.ClickHouse.Error != "" {
		sb.WriteString(fmt.Sprintf("  Error: %s\n", report.ClickHouse.Error))
	}

	sb.WriteString(fmt.Sprintf("\n[Redpanda/Kafka Diagnostics] Status: %s\n", report.Redpanda.Status))
	sb.WriteString(fmt.Sprintf("  Connected: %t\n", report.Redpanda.Connected))
	sb.WriteString(fmt.Sprintf("  Topic Partitions (%s): %d\n", report.Redpanda.Lag.Topic, report.Redpanda.TopicPartitions))
	sb.WriteString(fmt.Sprintf("  Consumer Group (%s) Aggregate Lag: %d records\n", report.Redpanda.Lag.ConsumerGroup, report.Redpanda.Lag.TotalLag))
	if report.Redpanda.PingLatency != "" {
		sb.WriteString(fmt.Sprintf("  Ping Smoke Latency: %s\n", report.Redpanda.PingLatency))
	}
	if report.Redpanda.Error != "" {
		sb.WriteString(fmt.Sprintf("  Error: %s\n", report.Redpanda.Error))
	}

	return sb.String(), nil
}

// RunDiagnostics executes checks for ClickHouse and Redpanda and builds overall report.
func RunDiagnostics(ctx context.Context, cfg *Config, chQuerier ClickHouseQuerier, rpQuerier RedpandaQuerier) *DiagReport {
	chRep := CheckClickHouse(ctx, chQuerier, cfg.SkipSmokeTests)
	rpRep := CheckRedpanda(ctx, rpQuerier, cfg.TopicName, cfg.ConsumerGroup, cfg.SkipSmokeTests)

	overall := StatusHealthy
	if chRep.Status == StatusFailed || rpRep.Status == StatusFailed {
		overall = StatusFailed
	} else if chRep.Status == StatusDegraded || rpRep.Status == StatusDegraded {
		overall = StatusDegraded
	}

	return &DiagReport{
		OverallStatus: overall,
		Timestamp:     time.Now().UTC(),
		ClickHouse:    chRep,
		Redpanda:      rpRep,
	}
}

func main() {
	cfg, err := ParseFlags(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if cfg.ClickHouseHost != "" {
		_ = os.Setenv("CLICKHOUSE_HOST", cfg.ClickHouseHost)
	}
	if cfg.ClickHousePort != 0 {
		_ = os.Setenv("CLICKHOUSE_PORT", fmt.Sprintf("%d", cfg.ClickHousePort))
	}
	if cfg.ClickHouseUser != "" {
		_ = os.Setenv("CLICKHOUSE_USER", cfg.ClickHouseUser)
	}
	if cfg.ClickHousePassword != "" {
		_ = os.Setenv("CLICKHOUSE_PASSWORD", cfg.ClickHousePassword)
	}
	if cfg.ClickHouseDatabase != "" {
		_ = os.Setenv("CLICKHOUSE_DATABASE", cfg.ClickHouseDatabase)
	}
	if cfg.ClickHouseDSN != "" {
		_ = os.Setenv("CLICKHOUSE_DSN", cfg.ClickHouseDSN)
	}

	var chQuerier ClickHouseQuerier
	chConn, err := clickhouse.Connect(ctx)
	if err == nil && chConn != nil {
		chQuerier = &RealClickHouseQuerier{conn: chConn}
		defer chConn.Close()
	}

	opts := []kgo.Opt{kgo.SeedBrokers(cfg.RedpandaBrokers...)}
	if cfg.TLSEnabled {
		tlsCfg := &tls.Config{
			InsecureSkipVerify: cfg.TLSInsecure,
		}
		opts = append(opts, kgo.DialTLSConfig(tlsCfg))
	}
	if cfg.SASLEnabled {
		var mechanism sasl.Mechanism
		switch strings.ToUpper(cfg.SASLMechanism) {
		case "PLAIN":
			mechanism = plain.Auth{
				User: cfg.SASLUser,
				Pass: cfg.SASLPassword,
			}.AsMechanism()
		case "SCRAM-SHA-256":
			mechanism = scram.Auth{
				User: cfg.SASLUser,
				Pass: cfg.SASLPassword,
			}.AsSha256Mechanism()
		case "SCRAM-SHA-512":
			mechanism = scram.Auth{
				User: cfg.SASLUser,
				Pass: cfg.SASLPassword,
			}.AsSha512Mechanism()
		}
		if mechanism != nil {
			opts = append(opts, kgo.SASL(mechanism))
		}
	}

	var rpQuerier RedpandaQuerier
	kClient, err := kgo.NewClient(opts...)
	if err == nil && kClient != nil {
		admClient := kadm.NewClient(kClient)
		rpQuerier = &RealRedpandaQuerier{client: admClient}
		defer kClient.Close()
	}

	report := RunDiagnostics(ctx, cfg, chQuerier, rpQuerier)
	out, err := FormatReport(report, cfg.OutputFormat)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error formatting report: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(out)
	if report.OverallStatus == StatusFailed {
		os.Exit(2)
	}
}
