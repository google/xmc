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
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"
	fmpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	timestamppb "protobuf/timestamp_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/storage/storage"
)

var testTimestamp = time.Date(2025, time.December, 15, 0, 0, 0, 0, time.UTC)

type mockStorage struct {
	storage.MockStorage
	mu        sync.Mutex
	updateErr error
	// Map of xid to updates passed to UpdateWorkUnitExecutions.
	gotWorkUnitExecutionUpdates map[int64][]*storage.WorkUnitExecutionUpdate
}

func (s *mockStorage) UpdateWorkUnitExecutions(ctx context.Context, xid int64, updates []*storage.WorkUnitExecutionUpdate) (map[int64]*essmsgpb.WorkUnitExecution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.updateErr != nil {
		return nil, s.updateErr
	}
	if s.gotWorkUnitExecutionUpdates == nil {
		s.gotWorkUnitExecutionUpdates = make(map[int64][]*storage.WorkUnitExecutionUpdate)
	}
	s.gotWorkUnitExecutionUpdates[xid] = append(s.gotWorkUnitExecutionUpdates[xid], updates...)
	return nil, nil
}

// mockMessage implements PubSubMessage for testing.
type mockMessage struct {
	data   []byte
	acked  bool
	nacked bool
}

func (m *mockMessage) Data() []byte {
	return m.data
}

func (m *mockMessage) Ack() {
	m.acked = true
}

func (m *mockMessage) Nack() {
	m.nacked = true
}

func TestProcessBatch(t *testing.T) {
	ctx := context.Background()
	mStorage := &mockStorage{}
	lp := NewLogProcessor(mStorage, "XMC LOG:", 10)

	validEntry := LogEntry{
		Labels: map[string]string{
			"k8s-pod/xmc_google_com/experiment-id": "1",
			"k8s-pod/xmc_google_com/work-unit-id":  "2",
			"k8s-pod/xmc_google_com/execution-id":  "3",
		},
		TextPayload: "test log",
		Timestamp:   testTimestamp,
		Severity:    "INFO",
	}
	validData, _ := json.Marshal(validEntry)

	msg1 := &mockMessage{data: validData}
	msg2 := &mockMessage{data: []byte("invalid json")}

	// Message with missing labels
	missingLabelsEntry := LogEntry{
		Labels: map[string]string{
			"k8s-pod/xmc_google_com/experiment-id": "1",
		},
	}
	missingLabelsData, _ := json.Marshal(missingLabelsEntry)
	msg3 := &mockMessage{data: missingLabelsData}

	messages := []PubSubMessage{msg1, msg2, msg3}

	gotUpdates := lp.ProcessBatch(ctx, messages)

	if !msg1.acked {
		t.Error("Message 1 (valid) was not acked")
	}

	wantUpdates := map[int64][]*storage.WorkUnitExecutionUpdate{
		1: {
			{
				WID: 2,
				EID: 3,
				WorkUnitExecution: &essmsgpb.WorkUnitExecution{
					Status: &essmsgpb.WorkUnitStatus{
						UserMessage: &essmsgpb.StructuredMessage{
							UserFacingText: "test log",
							Severity:       essmsgpb.StructuredMessage_INFO,
							Source:         essmsgpb.StructuredMessage_APPLICATION,
							CreateTime:     timestamppb.New(testTimestamp),
						},
					},
				},
				UpdateMask: &fmpb.FieldMask{
					Paths: []string{"status.user_message"},
				},
			},
		},
	}

	if diff := cmp.Diff(wantUpdates, gotUpdates, protocmp.Transform()); diff != "" {
		t.Errorf("ProcessBatch() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(wantUpdates, mStorage.gotWorkUnitExecutionUpdates, protocmp.Transform()); diff != "" {
		t.Errorf("UpdateWorkUnitExecutions() updates mismatch (-want +got):\n%s", diff)
	}
	if !msg2.acked {
		t.Error("Message 2 (invalid json) was not acked")
	}
	if !msg3.acked {
		t.Error("Message 3 (missing labels) was not acked")
	}
}

func TestBuildUpdateWithCustomKeyword(t *testing.T) {
	ctx := context.Background()
	lp := NewLogProcessor(&mockStorage{}, "CUSTOM LOG:", 10)

	entry := LogEntry{
		Labels: map[string]string{
			"k8s-pod/xmc_google_com/experiment-id": "100",
			"k8s-pod/xmc_google_com/work-unit-id":  "200",
			"k8s-pod/xmc_google_com/execution-id":  "300",
		},
		TextPayload: "CUSTOM LOG: custom message",
		Severity:    "INFO",
		Timestamp:   testTimestamp,
	}

	res := lp.buildUpdate(ctx, entry)
	if !res.OK {
		t.Fatal("buildUpdate() failed")
	}
	if res.ExperimentID != 100 {
		t.Errorf("buildUpdate() xid = %v, want 100", res.ExperimentID)
	}

	wantUserFacingText := "custom message"
	gotUserFacingText := res.Update.WorkUnitExecution.Status.UserMessage.UserFacingText
	if gotUserFacingText != wantUserFacingText {
		t.Errorf("buildUpdate() userFacingText = %q, want %q", gotUserFacingText, wantUserFacingText)
	}
}

func TestProcessBatchMultipleExperiments(t *testing.T) {
	ctx := context.Background()
	mStorage := &mockStorage{}
	lp := NewLogProcessor(mStorage, "XMC LOG:", 10)

	entry1 := LogEntry{
		Labels: map[string]string{
			"k8s-pod/xmc_google_com/experiment-id": "1",
			"k8s-pod/xmc_google_com/work-unit-id":  "2",
			"k8s-pod/xmc_google_com/execution-id":  "3",
		},
		TextPayload: "log 1",
		Timestamp:   testTimestamp,
		Severity:    "INFO",
	}
	data1, _ := json.Marshal(entry1)
	msg1 := &mockMessage{data: data1}

	entry2 := LogEntry{
		Labels: map[string]string{
			"k8s-pod/xmc_google_com/experiment-id": "5",
			"k8s-pod/xmc_google_com/work-unit-id":  "6",
			"k8s-pod/xmc_google_com/execution-id":  "7",
		},
		TextPayload: "log 2",
		Timestamp:   testTimestamp,
		Severity:    "INFO",
	}
	data2, _ := json.Marshal(entry2)
	msg2 := &mockMessage{data: data2}

	messages := []PubSubMessage{msg1, msg2}

	lp.ProcessBatch(ctx, messages)

	if !msg1.acked {
		t.Error("Message 1 was not acked")
	}
	if !msg2.acked {
		t.Error("Message 2 was not acked")
	}

	wantUpdates := map[int64][]*storage.WorkUnitExecutionUpdate{
		1: {
			{
				WID: 2,
				EID: 3,
				WorkUnitExecution: &essmsgpb.WorkUnitExecution{
					Status: &essmsgpb.WorkUnitStatus{
						UserMessage: &essmsgpb.StructuredMessage{
							UserFacingText: "log 1",
							Severity:       essmsgpb.StructuredMessage_INFO,
							Source:         essmsgpb.StructuredMessage_APPLICATION,
							CreateTime:     timestamppb.New(testTimestamp),
						},
					},
				},
				UpdateMask: &fmpb.FieldMask{
					Paths: []string{"status.user_message"},
				},
			},
		},
		5: {
			{
				WID: 6,
				EID: 7,
				WorkUnitExecution: &essmsgpb.WorkUnitExecution{
					Status: &essmsgpb.WorkUnitStatus{
						UserMessage: &essmsgpb.StructuredMessage{
							UserFacingText: "log 2",
							Severity:       essmsgpb.StructuredMessage_INFO,
							Source:         essmsgpb.StructuredMessage_APPLICATION,
							CreateTime:     timestamppb.New(testTimestamp),
						},
					},
				},
				UpdateMask: &fmpb.FieldMask{
					Paths: []string{"status.user_message"},
				},
			},
		},
	}

	if diff := cmp.Diff(wantUpdates, mStorage.gotWorkUnitExecutionUpdates, protocmp.Transform()); diff != "" {
		t.Errorf("UpdateWorkUnitExecutions() updates mismatch (-want +got):\n%s", diff)
	}
}

func TestProcessBatchHandlesUpdateErrors(t *testing.T) {
	ctx := context.Background()
	validEntry := LogEntry{
		Labels: map[string]string{
			"k8s-pod/xmc_google_com/experiment-id": "1",
			"k8s-pod/xmc_google_com/work-unit-id":  "2",
			"k8s-pod/xmc_google_com/execution-id":  "3",
		},
		TextPayload: "test log",
	}
	validData, _ := json.Marshal(validEntry)

	tests := []struct {
		name         string
		storageError error
		wantAck      bool
		wantNack     bool
	}{
		{
			name:         "update fails with NotFound error",
			storageError: grpcerr.ErrNotFound,
			wantAck:      true,
			wantNack:     false,
		},
		{
			name:         "update fails with generic error",
			storageError: fmt.Errorf("generic error"),
			wantAck:      false,
			wantNack:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockStorage := &mockStorage{updateErr: tc.storageError}
			lp := NewLogProcessor(mockStorage, "XMC LOG:", 10)
			msg := &mockMessage{data: validData}
			messages := []PubSubMessage{msg}

			lp.ProcessBatch(ctx, messages)

			if msg.acked != tc.wantAck {
				t.Errorf("Message acked = %v, want %v", msg.acked, tc.wantAck)
			}
			if msg.nacked != tc.wantNack {
				t.Errorf("Message nacked = %v, want %v", msg.nacked, tc.wantNack)
			}
		})
	}
}

func TestBuildUpdate(t *testing.T) {
	ctx := context.Background()
	lp := NewLogProcessor(&mockStorage{}, "XMC LOG:", 10)

	tests := []struct {
		name       string
		entry      LogEntry
		wantXID    int64
		wantUpdate *storage.WorkUnitExecutionUpdate
		wantOK     bool
	}{
		{
			name: "valid entry",
			entry: LogEntry{
				Labels: map[string]string{
					"k8s-pod/xmc_google_com/experiment-id": "100",
					"k8s-pod/xmc_google_com/work-unit-id":  "200",
					"k8s-pod/xmc_google_com/execution-id":  "300",
				},
				TextPayload: "some log",
				Severity:    "INFO",
				Timestamp:   testTimestamp,
			},
			wantXID: 100,
			wantUpdate: &storage.WorkUnitExecutionUpdate{
				WID: 200,
				EID: 300,
				WorkUnitExecution: &essmsgpb.WorkUnitExecution{
					Status: &essmsgpb.WorkUnitStatus{
						UserMessage: &essmsgpb.StructuredMessage{
							UserFacingText: "some log",
							Severity:       essmsgpb.StructuredMessage_INFO,
							Source:         essmsgpb.StructuredMessage_APPLICATION,
							CreateTime:     timestamppb.New(testTimestamp),
						},
					},
				},
				UpdateMask: &fmpb.FieldMask{
					Paths: []string{"status.user_message"},
				},
			},
			wantOK: true,
		},
		{
			name: "jsonPayload message",
			entry: LogEntry{
				Labels: map[string]string{
					"k8s-pod/xmc_google_com/experiment-id": "100",
					"k8s-pod/xmc_google_com/work-unit-id":  "200",
					"k8s-pod/xmc_google_com/execution-id":  "300",
				},
				Severity:  "INFO",
				Timestamp: testTimestamp,
				JSONPayload: LogJSONPayload{
					Message: "json message",
				},
			},
			wantXID: 100,
			wantUpdate: &storage.WorkUnitExecutionUpdate{
				WID: 200,
				EID: 300,
				WorkUnitExecution: &essmsgpb.WorkUnitExecution{
					Status: &essmsgpb.WorkUnitStatus{
						UserMessage: &essmsgpb.StructuredMessage{
							UserFacingText: "json message",
							Severity:       essmsgpb.StructuredMessage_INFO,
							Source:         essmsgpb.StructuredMessage_APPLICATION,
							CreateTime:     timestamppb.New(testTimestamp),
						},
					},
				},
				UpdateMask: &fmpb.FieldMask{
					Paths: []string{"status.user_message"},
				},
			},
			wantOK: true,
		},
		{
			name: "jsonPayload stack trace",
			entry: LogEntry{
				Labels: map[string]string{
					"k8s-pod/xmc_google_com/experiment-id": "100",
					"k8s-pod/xmc_google_com/work-unit-id":  "200",
					"k8s-pod/xmc_google_com/execution-id":  "300",
				},
				Severity:  "INFO",
				Timestamp: testTimestamp,
				JSONPayload: LogJSONPayload{
					Message:    "some error message",
					StackTrace: "Traceback (most recent call last):\n...",
				},
			},
			wantXID: 100,
			wantUpdate: &storage.WorkUnitExecutionUpdate{
				WID: 200,
				EID: 300,
				WorkUnitExecution: &essmsgpb.WorkUnitExecution{
					Status: &essmsgpb.WorkUnitStatus{
						UserMessage: &essmsgpb.StructuredMessage{
							UserFacingText: "some error message",
							Severity:       essmsgpb.StructuredMessage_ERROR,
							Source:         essmsgpb.StructuredMessage_APPLICATION,
							CreateTime:     timestamppb.New(testTimestamp),
							AuxiliaryInfo: &essmsgpb.AuxiliaryInfo{
								Detail: &essmsgpb.AuxiliaryInfo_StackTrace{
									StackTrace: "Traceback (most recent call last):\n...",
								},
							},
						},
					},
				},
				UpdateMask: &fmpb.FieldMask{
					Paths: []string{"status.user_message"},
				},
			},
			wantOK: true,
		},
		{
			name: "XMC LOG prefix removal",
			entry: LogEntry{
				Labels: map[string]string{
					"k8s-pod/xmc_google_com/experiment-id": "100",
					"k8s-pod/xmc_google_com/work-unit-id":  "200",
					"k8s-pod/xmc_google_com/execution-id":  "300",
				},
				TextPayload: "XMC LOG: real message",
				Severity:    "INFO",
				Timestamp:   testTimestamp,
			},
			wantXID: 100,
			wantUpdate: &storage.WorkUnitExecutionUpdate{
				WID: 200,
				EID: 300,
				WorkUnitExecution: &essmsgpb.WorkUnitExecution{
					Status: &essmsgpb.WorkUnitStatus{
						UserMessage: &essmsgpb.StructuredMessage{
							UserFacingText: "real message",
							Severity:       essmsgpb.StructuredMessage_INFO,
							Source:         essmsgpb.StructuredMessage_APPLICATION,
							CreateTime:     timestamppb.New(testTimestamp),
						},
					},
				},
				UpdateMask: &fmpb.FieldMask{
					Paths: []string{"status.user_message"},
				},
			},
			wantOK: true,
		},
		{
			name: "textPayload traceback entry",
			entry: LogEntry{
				Labels: map[string]string{
					"k8s-pod/xmc_google_com/experiment-id": "100",
					"k8s-pod/xmc_google_com/work-unit-id":  "200",
					"k8s-pod/xmc_google_com/execution-id":  "300",
				},
				TextPayload: "Traceback (most recent call last):",
				Severity:    "INFO",
				Timestamp:   testTimestamp,
			},
			wantXID: 100,
			wantUpdate: &storage.WorkUnitExecutionUpdate{
				WID: 200,
				EID: 300,
				WorkUnitExecution: &essmsgpb.WorkUnitExecution{
					Status: &essmsgpb.WorkUnitStatus{
						UserMessage: &essmsgpb.StructuredMessage{
							UserFacingText: "Traceback (most recent call last):",
							Severity:       essmsgpb.StructuredMessage_ERROR,
							Source:         essmsgpb.StructuredMessage_APPLICATION,
							CreateTime:     timestamppb.New(testTimestamp),
						},
					},
				},
				UpdateMask: &fmpb.FieldMask{
					Paths: []string{"status.user_message"},
				},
			},
			wantOK: true,
		},
		{
			name: "missing experiment-id",
			entry: LogEntry{
				Labels: map[string]string{
					"k8s-pod/xmc_google_com/work-unit-id": "200",
					"k8s-pod/xmc_google_com/execution-id": "300",
				},
			},
			wantOK: false,
		},
		{
			name: "invalid experiment-id",
			entry: LogEntry{
				Labels: map[string]string{
					"k8s-pod/xmc_google_com/experiment-id": "abc",
					"k8s-pod/xmc_google_com/work-unit-id":  "200",
					"k8s-pod/xmc_google_com/execution-id":  "300",
				},
			},
			wantOK: false,
		},
		{
			name: "missing work-unit-id",
			entry: LogEntry{
				Labels: map[string]string{
					"k8s-pod/xmc_google_com/experiment-id": "100",
					"k8s-pod/xmc_google_com/execution-id":  "300",
				},
			},
			wantOK: false,
		},
		{
			name: "missing execution-id",
			entry: LogEntry{
				Labels: map[string]string{
					"k8s-pod/xmc_google_com/experiment-id": "100",
					"k8s-pod/xmc_google_com/work-unit-id":  "200",
				},
			},
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := lp.buildUpdate(ctx, tc.entry)
			if res.OK != tc.wantOK {
				t.Errorf("buildUpdate() ok = %v, want %v", res.OK, tc.wantOK)
			}
			if !res.OK {
				return
			}
			if res.ExperimentID != tc.wantXID {
				t.Errorf("buildUpdate() xid = %v, want %v", res.ExperimentID, tc.wantXID)
			}
			if diff := cmp.Diff(tc.wantUpdate, res.Update, protocmp.Transform()); diff != "" {
				t.Errorf("buildUpdate() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestMapLogLabels(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   map[string]string
	}{
		{
			name:   "empty labels",
			labels: map[string]string{},
			want:   map[string]string{},
		},
		{
			name: "pod log labels",
			labels: map[string]string{
				"k8s-pod/xmc_google_com/experiment-id": "1",
				"k8s-pod/xmc_google_com/work-unit-id":  "2",
				"k8s-pod/xmc_google_com/execution-id":  "3",
				"other-label":                          "other-value",
			},
			want: map[string]string{
				"xmc.google.com/experiment-id": "1",
				"xmc.google.com/work-unit-id":  "2",
				"xmc.google.com/execution-id":  "3",
				"other-label":                  "other-value",
			},
		},
		{
			name: "no pod log labels",
			labels: map[string]string{
				"other-label": "other-value",
			},
			want: map[string]string{
				"other-label": "other-value",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mapLogLabels(tc.labels)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("mapLogLabels(%v) returned an unexpected diff (-want +got): %v", tc.labels, diff)
			}
		})
	}
}

func TestMapSeverity(t *testing.T) {
	ctx := context.Background()

	testCases := map[essmsgpb.StructuredMessage_Severity][]string{
		essmsgpb.StructuredMessage_ERROR:                {"ERROR", "CRITICAL", "ALERT", "EMERGENCY"},
		essmsgpb.StructuredMessage_WARNING:              {"WARNING"},
		essmsgpb.StructuredMessage_INFO:                 {"INFO", "NOTICE"},
		essmsgpb.StructuredMessage_DEBUG:                {"DEBUG"},
		essmsgpb.StructuredMessage_SEVERITY_UNSPECIFIED: {"DEFAULT", "SOMETHING_ELSE"},
	}

	for want, severities := range testCases {
		for _, severity := range severities {
			t.Run(severity, func(t *testing.T) {
				message := "some text"
				stackTrace := ""
				got := mapSeverity(ctx, severity, message, stackTrace)
				if got != want {
					t.Errorf("mapSeverity(%q, %q, %q) = %v, want %v", severity, message, stackTrace, got, want)
				}
			})
		}
	}

	t.Run("traceback_in_message", func(t *testing.T) {
		severity := "INFO"
		textPayload := "Traceback (most recent call last): some traceback message"
		want := essmsgpb.StructuredMessage_ERROR
		got := mapSeverity(ctx, severity, textPayload, "")
		if got != want {
			t.Errorf("mapSeverity(%q, %q, %q) = %v, want %v", severity, textPayload, "", got, want)
		}
	})

	t.Run("traceback_in_stackTrace", func(t *testing.T) {
		severity := "INFO"
		message := "some text"
		stackTrace := "Some traceback message"
		want := essmsgpb.StructuredMessage_ERROR
		got := mapSeverity(ctx, severity, message, stackTrace)
		if got != want {
			t.Errorf("mapSeverity(%q, %q, %q) = %v, want %v", severity, message, stackTrace, got, want)
		}
	})
}
