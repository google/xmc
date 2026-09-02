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

package main

import (
	"context"
	"errors"
	"testing"

	"github.com/hibiken/asynq"
)

type mockServerRunner struct {
	started      bool
	stopped      bool
	shutdown     bool
	pingErr      error
	startErr     error
	startHandler asynq.Handler
}

func (m *mockServerRunner) Start(handler asynq.Handler) error {
	m.started = true
	m.startHandler = handler
	return m.startErr
}

func (m *mockServerRunner) Stop() {
	m.stopped = true
}

func (m *mockServerRunner) Shutdown() {
	m.shutdown = true
}

func (m *mockServerRunner) Ping() error {
	return m.pingErr
}

type dummyHandler struct{}

func (d *dummyHandler) ProcessTask(ctx context.Context, task *asynq.Task) error {
	return nil
}

func TestNewAsynqWorkerServer_Validation(t *testing.T) {
	tests := []struct {
		name      string
		cfg       AsynqServerConfig
		handler   asynq.Handler
		wantError bool
	}{
		{
			name:      "empty redis addr",
			cfg:       AsynqServerConfig{RedisAddr: ""},
			handler:   &dummyHandler{},
			wantError: true,
		},
		{
			name:      "nil handler",
			cfg:       AsynqServerConfig{RedisAddr: "localhost:6379"},
			handler:   nil,
			wantError: true,
		},
		{
			name:      "valid config",
			cfg:       AsynqServerConfig{RedisAddr: "localhost:6379", Concurrency: 5},
			handler:   &dummyHandler{},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewAsynqWorkerServer(tt.cfg, tt.handler)
			if (err != nil) != tt.wantError {
				t.Fatalf("NewAsynqWorkerServer() error = %v, wantError = %v", err, tt.wantError)
			}
		})
	}
}

func TestAsynqWorkerServer_Lifecycle(t *testing.T) {
	ctx := context.Background()
	mock := &mockServerRunner{}
	handler := &dummyHandler{}
	srv := NewAsynqWorkerServerWithRunner(mock, handler)

	if err := srv.Start(); err != nil {
		t.Fatalf("Start() unexpected error: %v", err)
	}
	if !mock.started {
		t.Error("expected server to be started")
	}
	if mock.startHandler != handler {
		t.Error("expected handler to be passed to Start()")
	}

	if err := srv.Ping(ctx); err != nil {
		t.Fatalf("Ping() unexpected error: %v", err)
	}

	srv.Stop()
	if !mock.stopped {
		t.Error("expected server to be stopped")
	}

	srv.Shutdown()
	if !mock.shutdown {
		t.Error("expected server to be shut down")
	}
}

func TestAsynqWorkerServer_PingError(t *testing.T) {
	expectedErr := errors.New("connection failed")
	mock := &mockServerRunner{pingErr: expectedErr}
	srv := NewAsynqWorkerServerWithRunner(mock, &dummyHandler{})

	err := srv.Ping(context.Background())
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected ping error %v, got %v", expectedErr, err)
	}
}

func TestDefaultQueueWeights(t *testing.T) {
	weights := DefaultQueueWeights()
	if weights["critical"] != 6 {
		t.Errorf("expected critical weight 6, got %d", weights["critical"])
	}
	if weights["default"] != 3 {
		t.Errorf("expected default weight 3, got %d", weights["default"])
	}
	if weights["low"] != 1 {
		t.Errorf("expected low weight 1, got %d", weights["low"])
	}
}
