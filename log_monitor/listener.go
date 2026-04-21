package main

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"golang/cloud_google_com/go/pubsub/v/v1/pubsub"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/storage"
)

// LogBatchProcessor processes a batch of Pub/Sub messages.
type LogBatchProcessor interface {
	ProcessBatch(ctx context.Context, batch []PubSubMessage) map[int64][]*storage.WorkUnitExecutionUpdate
}

// Listener pulls messages from Pub/Sub and sends them to LogBatchProcessor.
type Listener struct {
	config       *Config
	logProcessor LogBatchProcessor
	client       *pubsub.Client
	subscription *pubsub.Subscription
	isRunning    atomic.Bool
}

// NewListener creates a new Listener.
func NewListener(config *Config, logProcessor LogBatchProcessor, client *pubsub.Client) *Listener {
	return &Listener{
		config:       config,
		logProcessor: logProcessor,
		client:       client,
		subscription: client.Subscription(config.SubscriptionID),
	}
}

// Start pulls messages from Pub/Sub in batches and sends them to LogProcessor.
func (l *Listener) Start(ctx context.Context) {
	logging.Info(ctx, "Log Monitor starting to pull messages", "subscription", l.config.SubscriptionID)

	l.isRunning.Store(true)
	defer l.isRunning.Store(false)

	// Buffer the channel to avoid blocking Receive if the batch processor is busy.
	msgCh := make(chan *pubsub.Message, l.config.BatchSize)

	// Goroutine to process messages in batches
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var batch []PubSubMessage
		ticker := time.NewTicker(l.config.BatchInterval)
		defer ticker.Stop()

		for {
			select {
			case message, ok := <-msgCh:
				if !ok {
					// Channel closed, process remaining messages and exit
					if len(batch) > 0 {
						// Use context.Background() to ensure the final batch is processed even if the
						// original context is cancelled.
						l.logProcessor.ProcessBatch(context.Background(), batch)
					}
					return
				}
				batch = append(batch, &pubSubMessageWrapper{msg: message})
				if len(batch) >= l.config.BatchSize {
					l.logProcessor.ProcessBatch(ctx, batch)
					batch = nil
				}
			case <-ticker.C:
				if len(batch) > 0 {
					l.logProcessor.ProcessBatch(ctx, batch)
					batch = nil
				}
			}
		}
	}()

	// Receive messages from Pub/Sub and send to channel.
	// Receive blocks until context is cancelled or an error occurs.
	err := l.subscription.Receive(ctx, func(ctx context.Context, msg *pubsub.Message) {
		msgCh <- msg
	})
	if err != nil {
		logging.Error(ctx, "Pub/Sub Receive error", "error", err)
	}

	close(msgCh)
	wg.Wait()
}

// IsHealthy returns true if the listener's background receive loop is running.
func (l *Listener) IsHealthy() bool {
	return l.isRunning.Load()
}
