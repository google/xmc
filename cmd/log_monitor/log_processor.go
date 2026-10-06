// Copyright 2025 Google LLC
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
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"golang/cloud_google_com/go/pubsub/v/v1/pubsub"
	fmpb "protobuf/field_mask_go_proto"
	timestamppb "protobuf/timestamp_go_proto"
	"task/go/status"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/kubernetes/common/constants"
	"xmanager_cloud/kubernetes/common/labels"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/storage"
)

const (
	podLogExperimentIDLabel = "k8s-pod/xmc_google_com/experiment-id"
	podLogWorkUnitIDLabel   = "k8s-pod/xmc_google_com/work-unit-id"
	podLogExecutionIDLabel  = "k8s-pod/xmc_google_com/execution-id"
)

// LogJSONPayload represents the JSON payload in a log entry.
type LogJSONPayload struct {
	Message    string `json:"message"`
	StackTrace string `json:"stack_trace"`
}

// LogEntry represents a log entry from Pub/Sub.
type LogEntry struct {
	JSONPayload LogJSONPayload    `json:"jsonPayload"`
	Labels      map[string]string `json:"labels"`
	TextPayload string            `json:"textPayload"`
	Timestamp   time.Time         `json:"timestamp"`
	Severity    string            `json:"severity"`
}

// PubSubMessage is an interface for Pub/Sub messages to allow mocking.
type PubSubMessage interface {
	Data() []byte
	Ack()
	Nack()
}

// pubSubMessageWrapper wraps a *pubsub.Message to implement PubSubMessage.
type pubSubMessageWrapper struct {
	msg *pubsub.Message
}

func (w *pubSubMessageWrapper) Data() []byte {
	return w.msg.Data
}

func (w *pubSubMessageWrapper) Ack() {
	w.msg.Ack()
}

func (w *pubSubMessageWrapper) Nack() {
	w.msg.Nack()
}

// pendingUpdate holds the PubSub message and the corresponding update.
type pendingUpdate struct {
	msg    PubSubMessage
	update *storage.WorkUnitExecutionUpdate
}

// ExecutionUpdater updates work unit executions.
type ExecutionUpdater interface {
	UpdateWorkUnitExecutions(ctx context.Context, xid int64, updates []*storage.WorkUnitExecutionUpdate) (map[int64]*essmsgpb.WorkUnitExecution, error)
}

// LogProcessor processes logs from Pub/Sub.
type LogProcessor struct {
	storageClient        ExecutionUpdater
	logKeyword           string
	maxConcurrentUpdates int
}

// NewLogProcessor creates a new LogProcessor.
func NewLogProcessor(storageClient ExecutionUpdater, logKeyword string, maxConcurrentUpdates int) *LogProcessor {
	return &LogProcessor{
		storageClient:        storageClient,
		logKeyword:           logKeyword,
		maxConcurrentUpdates: maxConcurrentUpdates,
	}
}

// ProcessBatch processes a batch of Pub/Sub messages.
func (lp *LogProcessor) ProcessBatch(ctx context.Context, batch []PubSubMessage) map[int64][]*storage.WorkUnitExecutionUpdate {
	// Generate a trace ID for this batch of messages.
	ctx = logging.InjectOperation(ctx, "")
	logging.Info(ctx, "Processing batch", "count", len(batch))
	pendingUpdates := make(map[int64][]*pendingUpdate)
	for _, msg := range batch {
		var entry LogEntry
		if err := json.Unmarshal(msg.Data(), &entry); err != nil {
			logging.Error(ctx, "Failed to unmarshal log entry", "error", err)
			msg.Ack()
			continue
		}

		logRes := lp.buildUpdate(ctx, entry)
		if !logRes.OK {
			msg.Ack()
			continue
		}
		xid, update := logRes.ExperimentID, logRes.Update

		pendingUpdates[xid] = append(pendingUpdates[xid], &pendingUpdate{msg: msg, update: update})
	}

	result := make(map[int64][]*storage.WorkUnitExecutionUpdate)
	var mu sync.Mutex
	var wg sync.WaitGroup
	// Limit the number of concurrent goroutines to avoid overloading the storage client.
	sem := make(chan struct{}, lp.maxConcurrentUpdates)
	for xid, updates := range pendingUpdates {
		wg.Add(1)
		sem <- struct{}{}
		go func(xid int64, updates []*pendingUpdate) {
			defer wg.Done()
			defer func() { <-sem }()
			wuUpdates, err := lp.processUpdatesForExperiment(ctx, xid, updates)
			if err != nil {
				return
			}
			mu.Lock()
			result[xid] = wuUpdates
			mu.Unlock()
		}(xid, updates)
	}
	wg.Wait()
	return result
}

func (lp *LogProcessor) processUpdatesForExperiment(ctx context.Context, xid int64, updates []*pendingUpdate) ([]*storage.WorkUnitExecutionUpdate, error) {
	ctx = logging.WithValues(ctx, "xid", xid)
	wuUpdates := make([]*storage.WorkUnitExecutionUpdate, len(updates))
	for i, pu := range updates {
		wuUpdates[i] = pu.update
	}

	logging.Info(ctx, "Grouped updates", "updates", wuUpdates)
	if _, err := lp.storageClient.UpdateWorkUnitExecutions(ctx, xid, wuUpdates); err != nil {
		logging.Error(ctx, "Failed to update work unit executions", "error", err)

		shouldAck := false
		if errors.Is(err, status.ErrNotFound) {
			// We ack message for NotFoundError as it doesn't make sense to retry it.
			logging.Info(ctx, "Acking messages due to the NotFound error")
			shouldAck = true
		}

		for _, pu := range updates {
			if shouldAck {
				pu.msg.Ack()
			} else {
				// We explicitly nack message so it is available for redelivery without waiting for ack
				// deadline and its extension period to pass.
				pu.msg.Nack()
			}
		}
		return nil, err
	}

	for _, pu := range updates {
		pu.msg.Ack()
	}
	return wuUpdates, nil
}

// ParsedLogUpdate holds structured result data for a parsed log entry update.
type ParsedLogUpdate struct {
	ExperimentID int64
	Update       *storage.WorkUnitExecutionUpdate
	OK           bool
}

func (lp *LogProcessor) buildUpdate(ctx context.Context, entry LogEntry) ParsedLogUpdate {
	jobLabels := mapLogLabels(entry.Labels)
	xmcLabels, err := labels.ParseXMCLabels(jobLabels)
	if err != nil {
		logging.Warn(ctx, "Failed to parse XMC labels from log entry", "err", err, "labels", entry.Labels)
		return ParsedLogUpdate{OK: false}
	}
	xid, wid, eid := xmcLabels.XID, xmcLabels.WID, xmcLabels.EID

	message, stackTrace := extractMessageAndStackTrace(entry, lp.logKeyword)

	structuredMessage := &essmsgpb.StructuredMessage{
		UserFacingText: message,
		Severity:       mapSeverity(ctx, entry.Severity, message, stackTrace),
		Source:         essmsgpb.StructuredMessage_APPLICATION,
		CreateTime:     timestamppb.New(entry.Timestamp),
	}

	if stackTrace != "" {
		structuredMessage.AuxiliaryInfo = &essmsgpb.AuxiliaryInfo{
			Detail: &essmsgpb.AuxiliaryInfo_StackTrace{
				StackTrace: stackTrace,
			},
		}
	}

	update := &storage.WorkUnitExecutionUpdate{
		WID: wid,
		EID: eid,
		WorkUnitExecution: &essmsgpb.WorkUnitExecution{
			Status: &essmsgpb.WorkUnitStatus{
				UserMessage: structuredMessage,
			},
		},
		UpdateMask: &fmpb.FieldMask{
			Paths: []string{"status.user_message"},
		},
	}
	return ParsedLogUpdate{
		ExperimentID: xid,
		Update:       update,
		OK:           true,
	}
}

// mapLogLabels maps the labels from the cloud logging format to the labels applied by the actuator.
func mapLogLabels(labels map[string]string) map[string]string {
	newLabels := make(map[string]string)
	for k, v := range labels {
		switch k {
		case podLogExperimentIDLabel:
			newLabels[constants.ExperimentIDLabel] = v
		case podLogWorkUnitIDLabel:
			newLabels[constants.WorkUnitIDLabel] = v
		case podLogExecutionIDLabel:
			newLabels[constants.ExecutionIDLabel] = v
		default:
			newLabels[k] = v
		}
	}
	return newLabels
}

func mapSeverity(ctx context.Context, severity string, messageText, stackTrace string) essmsgpb.StructuredMessage_Severity {
	// Cloud logging severity for Tracebacks is set to INFO, we want to override it to ERROR.
	if strings.HasPrefix(messageText, "Traceback (most recent call last)") || stackTrace != "" {
		return essmsgpb.StructuredMessage_ERROR
	}
	return toMessageSeverity(ctx, severity)
}

func toMessageSeverity(ctx context.Context, severity string) essmsgpb.StructuredMessage_Severity {
	switch severity {
	case "ERROR", "CRITICAL", "ALERT", "EMERGENCY":
		return essmsgpb.StructuredMessage_ERROR
	case "WARNING":
		return essmsgpb.StructuredMessage_WARNING
	case "DEBUG":
		return essmsgpb.StructuredMessage_DEBUG
	case "INFO", "NOTICE":
		return essmsgpb.StructuredMessage_INFO
	// DEFAULT is used for the severity that is not explicitly set.
	case "DEFAULT":
		return essmsgpb.StructuredMessage_SEVERITY_UNSPECIFIED
	default:
		logging.Warn(ctx, "Unknown severity, falling back to SEVERITY_UNSPECIFIED", "severity", severity)
		return essmsgpb.StructuredMessage_SEVERITY_UNSPECIFIED
	}
}

func extractMessageAndStackTrace(entry LogEntry, logKeyword string) (message string, stackTrace string) {
	message = cmp.Or(entry.JSONPayload.Message, entry.TextPayload)

	// If the message has logKeyword (by default set to "XMC LOG:") as a prefix, we want to remove it.
	if logKeyword != "" {
		if after, ok := strings.CutPrefix(message, logKeyword); ok {
			message = strings.TrimSpace(after)
		}
	}
	return message, entry.JSONPayload.StackTrace
}
