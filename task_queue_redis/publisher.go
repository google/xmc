package redisclient

import (
	"context"

	"go/log"
	"golang/github_com/go_redis/redis/v/v9/redis"
)

const (
	// DefaultMaxLen is the default max length for Redis streams. Set to 10000 to avoid Redis
	// streams growing indefinitely but still keep a decent history.
	DefaultMaxLen = 10000
)

// NewPublisher creates a new redis client for publishing messages.
func NewPublisher(redisConfig Config, streamName string) *PublisherClient {
	options := getRedisOptions(redisConfig)
	return &PublisherClient{
		baseManager: baseManager{
			Client:     redis.NewClient(options),
			StreamName: streamName,
		},
	}
}

// AddMessage adds a message to the Redis stream.
func (p *PublisherClient) AddMessage(ctx context.Context, data string, failedAttempts string) {
	args := &redis.XAddArgs{
		Stream: p.StreamName,
		MaxLen: DefaultMaxLen,
		Approx: true,
		Values: map[string]any{
			"data":           data,
			"failedAttempts": failedAttempts,
		},
	}
	err := p.XAdd(ctx, args).Err()
	if err != nil {
		log.ErrorContextf(ctx, "Failed to add message to stream %s: %v", p.StreamName, err)
	}
}
