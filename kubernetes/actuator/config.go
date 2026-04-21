package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"go/flag"
	"xmanager_cloud/kubernetes/actuator/k8s_client/k8sclient"
	"xmanager_cloud/kubernetes/common/constants"
	"xmanager_cloud/logging/logging"
)

// RedisConfig holds the configuration for the Redis queue.
type RedisConfig struct {
	RedisAddr                    string
	RedisMaxRetries              int
	RedisProcessTimeout          time.Duration
	RedisAbandonedMessageTimeout time.Duration
	RedisReadBatchSize           int64
}

// Config holds the configuration for the Actuator server.
type Config struct {
	Port                        string
	RuntimeConfig               k8sclient.ActuatorRuntimeConfig
	Audience                    string
	XManagerServiceAccount      string
	VerifyingKeyFile            string
	MaxRecentKeys               int
	StatusMessageHistoryEnabled bool
	UseRedisQueue               bool
	RedisConfig                 RedisConfig
}

var (
	port                        string
	createNewNamespaces         bool
	spannerDatabaseName         string
	quotaProjectID              string
	xmanagerServiceAccount      string
	audience                    string
	defaultExecutionEnvironment string
	verifyingKeyFile            string
	maxRecentKeys               int
	useRedisQueue               bool
	statusMessageHistoryEnabled bool
)

// Redis flags.
var (
	redisAddr                    string
	redisMaxRetries              int
	redisProcessTimeout          time.Duration
	redisAbandonedMessageTimeout time.Duration
	redisReadBatchSize           int64
)

func init() {
	flag.StringVar(&port, "port", "8080", "The HTTP server port to listen on.")
	flag.BoolVar(&createNewNamespaces, "create_new_namespaces", true, "Whether a new namespace can be created when creating new Work Units.")
	flag.StringVar(&spannerDatabaseName, "spanner_database_name", "projects/xmc-test-project-1/instances/xmc/databases/experiment-db", "The name of the Spanner database.")
	flag.StringVar(&quotaProjectID, "quota_project_id", "xmc-test-project-1", "The ID of the quota project to use.")
	flag.StringVar(&xmanagerServiceAccount, "xmanager_service_account", "", "The service account to use for OIDC authentication.")
	flag.StringVar(&audience, "audience", "", "The audience to use for OIDC authentication. If empty, it defaults to https://actuator.xmanager.svc.cluster.local:<port>.")
	flag.StringVar(&defaultExecutionEnvironment, "default_execution_environment", "gke", "The execution environment to use if not specified in the request.")
	flag.StringVar(&verifyingKeyFile, "verifying_key_file", "/etc/verification-key/tls.crt", "Path to the key for verifying message signatures.")
	flag.IntVar(&maxRecentKeys, "max_recent_keys", 2, "The maximum number of recent keys to keep for key rotation.")
	flag.BoolVar(&useRedisQueue, "use_redis_queue", false, "Whether to use Redis queue.")
	flag.BoolVar(&statusMessageHistoryEnabled, "status_message_history_enabled", false, "Whether to enable status message history.")
	initRedisFlags()
}

func initRedisFlags() {
	flag.StringVar(&redisAddr, "redis_addr", "", "The address of the Redis instance.")
	flag.IntVar(&redisMaxRetries, "redis_max_retries", constants.RedisMaxRetries, "The maximum number of retries for a Redis task.")
	flag.DurationVar(&redisProcessTimeout, "redis_process_timeout", constants.RedisProcessTimeout, "The timeout for processing a Redis task.")
	flag.DurationVar(&redisAbandonedMessageTimeout, "redis_abandoned_message_timeout", constants.RedisAbandonedMessageTimeout, "The timeout for abandoned Redis messages.")
	flag.Int64Var(&redisReadBatchSize, "redis_read_batch_size", constants.RedisReadBatchSize, "The batch size for reading Redis messages.")
}

func validate() error {
	if p, err := strconv.Atoi(port); err != nil || p <= 0 || p > 65535 {
		return fmt.Errorf("invalid -port: %q; must be an integer between 1 and 65535", port)
	}
	if xmanagerServiceAccount == "" {
		return fmt.Errorf("missing -xmanager_service_account")
	}
	if useRedisQueue && redisAddr == "" {
		return fmt.Errorf("-redis_addr must be provided if -use_redis_queue is true")
	}
	if !useRedisQueue && redisAddr != "" {
		return fmt.Errorf("-redis_addr must not be provided if -use_redis_queue is false")
	}
	return nil
}

func Load() Config {
	ctx := context.Background()
	if err := validate(); err != nil {
		logging.Fatal(ctx, "Invalid configuration", "error", err)
	}

	aud := audience
	if aud == "" {
		logging.Warn(ctx, "Audience is not set, using default")
		aud = fmt.Sprintf("https://actuator.xmanager.svc.cluster.local:%s", port)
	}

	return Config{
		Port: port,
		RuntimeConfig: k8sclient.ActuatorRuntimeConfig{
			CreateNewNamespaces:         createNewNamespaces,
			SpannerDatabaseName:         spannerDatabaseName,
			QuotaProjectID:              quotaProjectID,
			DefaultExecutionEnvironment: defaultExecutionEnvironment,
		},
		Audience:                    aud,
		XManagerServiceAccount:      xmanagerServiceAccount,
		VerifyingKeyFile:            verifyingKeyFile,
		MaxRecentKeys:               maxRecentKeys,
		UseRedisQueue:               useRedisQueue,
		StatusMessageHistoryEnabled: statusMessageHistoryEnabled,
		RedisConfig: RedisConfig{
			RedisAddr:                    redisAddr,
			RedisMaxRetries:              redisMaxRetries,
			RedisProcessTimeout:          redisProcessTimeout,
			RedisAbandonedMessageTimeout: redisAbandonedMessageTimeout,
			RedisReadBatchSize:           redisReadBatchSize,
		},
	}
}

