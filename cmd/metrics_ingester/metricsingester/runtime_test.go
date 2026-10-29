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
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"cloud.google.com/go/bigquery/storage/apiv1/storagepb"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"google.golang.org/api/option"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/dynamicpb"
	pb "xmanager_cloud/api/protos/metrics"
)

type fakeBigQueryWriteServer struct {
	storagepb.UnimplementedBigQueryWriteServer

	mu                            sync.Mutex
	getStreamErr                  error
	transientUnavailableRemaining int
	appendRespCode                codes.Code
	appendAttempts                int
	serializedRows                [][]byte
}

func (s *fakeBigQueryWriteServer) GetWriteStream(_ context.Context, req *storagepb.GetWriteStreamRequest) (*storagepb.WriteStream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getStreamErr != nil {
		return nil, s.getStreamErr
	}
	return &storagepb.WriteStream{
		Name:     req.GetName(),
		Type:     storagepb.WriteStream_COMMITTED,
		Location: "US",
	}, nil
}

func (s *fakeBigQueryWriteServer) AppendRows(stream storagepb.BigQueryWrite_AppendRowsServer) error {
	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		resp := s.handleAppend(req)
		if err := stream.Send(resp); err != nil {
			return err
		}
	}
}

func (s *fakeBigQueryWriteServer) handleAppend(req *storagepb.AppendRowsRequest) *storagepb.AppendRowsResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendAttempts++
	if s.transientUnavailableRemaining > 0 {
		s.transientUnavailableRemaining--
		return &storagepb.AppendRowsResponse{
			Response: &storagepb.AppendRowsResponse_Error{
				Error: &status.Status{Code: int32(codes.Unavailable), Message: "transient unavailable"},
			},
		}
	}
	if s.appendRespCode != codes.OK {
		return &storagepb.AppendRowsResponse{
			Response: &storagepb.AppendRowsResponse_Error{
				Error: &status.Status{Code: int32(s.appendRespCode), Message: "append rejected"},
			},
		}
	}
	for _, row := range req.GetProtoRows().GetRows().GetSerializedRows() {
		s.serializedRows = append(s.serializedRows, append([]byte(nil), row...))
	}
	return &storagepb.AppendRowsResponse{
		Response: &storagepb.AppendRowsResponse_AppendResult_{
			AppendResult: &storagepb.AppendRowsResponse_AppendResult{},
		},
	}
}

func (s *fakeBigQueryWriteServer) snapshotRows() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]byte, len(s.serializedRows))
	copy(out, s.serializedRows)
	return out
}

func startFakeBigQueryServers(t *testing.T, tableStatus *atomic.Int32) (*fakeBigQueryWriteServer, []option.ClientOption, []option.ClientOption) {
	t.Helper()
	restSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code := int(tableStatus.Load())
		if code != http.StatusOK {
			http.Error(w, `{"error":{"code":404,"message":"not found"}}`, code)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"kind":           "bigquery#table",
			"tableReference": map[string]string{"projectId": "bq-proj", "datasetId": "xmc_metrics", "tableId": "xmc_metrics"},
		})
	}))
	t.Cleanup(restSrv.Close)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	writeSrv := &fakeBigQueryWriteServer{}
	grpcSrv := grpc.NewServer()
	storagepb.RegisterBigQueryWriteServer(grpcSrv, writeSrv)
	go func() { _ = grpcSrv.Serve(lis) }()
	t.Cleanup(grpcSrv.Stop)

	bqOpts := []option.ClientOption{
		option.WithEndpoint(restSrv.URL),
		option.WithoutAuthentication(),
	}
	mwOpts := []option.ClientOption{
		option.WithEndpoint(lis.Addr().String()),
		option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
		option.WithoutAuthentication(),
	}
	return writeSrv, bqOpts, mwOpts
}

func bigQueryTestConfig() *Config {
	return &Config{
		Port:              "8080",
		ProjectID:         "ps-proj",
		SubscriptionID:    "xmc-sub",
		MetricsStore:      StoreBigQuery,
		BigQueryProjectID: "bq-proj",
		BigQueryDataset:   "xmc_metrics",
		BigQueryTable:     "xmc_metrics",
		BatchSize:         10,
		BatchInterval:     defaultBatchInterval,
	}
}

func TestOpenBigQueryStoreWriteReadinessAndIdempotentClose(t *testing.T) {
	var tableStatus atomic.Int32
	tableStatus.Store(http.StatusOK)
	writeSrv, bqOpts, mwOpts := startFakeBigQueryServers(t, &tableStatus)
	msgDesc := loadCanonicalBQDescriptor(t)

	store, err := openBigQueryStoreWithOptions(context.Background(), bigQueryTestConfig(), bqOpts, mwOpts)
	if err != nil {
		t.Fatalf("openBigQueryStoreWithOptions() error = %v", err)
	}

	if err := store.Writer().Write(context.Background(), []*pb.Metric{testMetric()}); err != nil {
		t.Fatalf("Writer().Write() error = %v", err)
	}
	rows := writeSrv.snapshotRows()
	if len(rows) != 1 {
		t.Fatalf("persisted rows = %d, want 1", len(rows))
	}
	dyn := dynamicpb.NewMessage(msgDesc)
	if err := proto.Unmarshal(rows[0], dyn); err != nil {
		t.Fatalf("proto.Unmarshal() error = %v", err)
	}
	if got := dyn.Get(msgDesc.Fields().ByName("metric_name")).String(); got != "loss" {
		t.Errorf("metric_name = %q, want \"loss\"", got)
	}

	mux := NewHealthMux(fakeRunning(true), store.Pinger())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/readyz status when healthy = %d, want 200", rec.Code)
	}

	tableStatus.Store(http.StatusServiceUnavailable)
	recOutage := httptest.NewRecorder()
	mux.ServeHTTP(recOutage, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recOutage.Code != http.StatusServiceUnavailable {
		t.Errorf("/readyz status during BigQuery metadata outage = %d, want 503", recOutage.Code)
	}

	writeSrv.mu.Lock()
	writeSrv.transientUnavailableRemaining = 1
	writeSrv.mu.Unlock()
	if err := store.Writer().Write(context.Background(), []*pb.Metric{testMetric()}); err != nil {
		t.Fatalf("Writer().Write() after transient Unavailable error = %v, want automatic retry success", err)
	}
	writeSrv.mu.Lock()
	gotAttempts := writeSrv.appendAttempts
	writeSrv.appendRespCode = codes.InvalidArgument
	writeSrv.mu.Unlock()
	if gotAttempts != 3 {
		t.Errorf("appendAttempts after 1 initial write + 1 retried write = %d, want 3", gotAttempts)
	}
	if got := len(writeSrv.snapshotRows()); got != 2 {
		t.Errorf("persisted rows after retried write = %d, want 2", got)
	}
	if err := store.Writer().Write(context.Background(), []*pb.Metric{testMetric()}); err == nil {
		t.Error("Writer().Write() succeeded when AppendRows returned non-retryable InvalidArgument, want error")
	}

	if err := store.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if err := store.Writer().Write(context.Background(), []*pb.Metric{testMetric()}); err == nil {
		t.Error("Writer().Write() after Close() succeeded, want error")
	}
}

func TestOpenBigQueryStoreStartupErrorRollbacks(t *testing.T) {
	conflictingOpts := []option.ClientOption{
		option.WithAPIKey("test-key"),
		option.WithoutAuthentication(),
	}

	t.Run("invalid bigquery REST client options", func(t *testing.T) {
		_, err := openBigQueryStoreWithOptions(context.Background(), bigQueryTestConfig(), conflictingOpts, nil)
		if err == nil || !strings.Contains(err.Error(), "failed to create bigquery client") {
			t.Fatalf("error = %v, want failed to create bigquery client", err)
		}
	})

	t.Run("missing destination table fails startup verification", func(t *testing.T) {
		var statusCode atomic.Int32
		statusCode.Store(http.StatusNotFound)
		_, bqOpts, mwOpts := startFakeBigQueryServers(t, &statusCode)
		_, err := openBigQueryStoreWithOptions(context.Background(), bigQueryTestConfig(), bqOpts, mwOpts)
		if err == nil || !strings.Contains(err.Error(), "failed to verify bigquery table") {
			t.Fatalf("error = %v, want failed to verify bigquery table", err)
		}
	})

	t.Run("invalid managedwriter client options rolls back bqClient", func(t *testing.T) {
		var statusCode atomic.Int32
		statusCode.Store(http.StatusOK)
		_, bqOpts, _ := startFakeBigQueryServers(t, &statusCode)
		_, err := openBigQueryStoreWithOptions(context.Background(), bigQueryTestConfig(), bqOpts, conflictingOpts)
		if err == nil || !strings.Contains(err.Error(), "failed to create bigquery storage write client") {
			t.Fatalf("error = %v, want failed to create bigquery storage write client", err)
		}
	})

	t.Run("GetWriteStream failure rolls back both clients", func(t *testing.T) {
		var statusCode atomic.Int32
		statusCode.Store(http.StatusOK)
		writeSrv, bqOpts, mwOpts := startFakeBigQueryServers(t, &statusCode)
		writeSrv.mu.Lock()
		writeSrv.getStreamErr = grpcstatus.Error(codes.PermissionDenied, "denied")
		writeSrv.mu.Unlock()

		_, err := openBigQueryStoreWithOptions(context.Background(), bigQueryTestConfig(), bqOpts, mwOpts)
		if err == nil || !strings.Contains(err.Error(), "failed to open bigquery write stream") {
			t.Fatalf("error = %v, want failed to open bigquery write stream", err)
		}
	})
}

type fakeClickHouseConn struct {
	closed bool
}

func (f *fakeClickHouseConn) PrepareBatch(context.Context, string, ...driver.PrepareBatchOption) (driver.Batch, error) {
	return nil, errors.New("unused")
}

func (f *fakeClickHouseConn) Ping(context.Context) error { return nil }

func (f *fakeClickHouseConn) Close() error {
	f.closed = true
	return nil
}

func TestOpenRuntimeStoreAndCleanupsLIFO(t *testing.T) {
	t.Run("clickhouse store happy path via injected connector", func(t *testing.T) {
		conn := &fakeClickHouseConn{}
		store, err := openClickHouseStore(context.Background(), func(context.Context) (clickHouseConn, error) {
			return conn, nil
		})
		if err != nil {
			t.Fatalf("openClickHouseStore() error = %v", err)
		}
		if store.Writer() == nil || store.Pinger() != conn {
			t.Fatal("RuntimeStore accessors did not wire ClickHouse handle")
		}
		if err := store.Close(); err != nil || !conn.closed {
			t.Fatalf("Close() = %v, closed = %v; want nil, true", err, conn.closed)
		}
	})

	t.Run("OpenRuntimeStore dispatches clickhouse, bigquery, and rejects unknown", func(t *testing.T) {
		t.Setenv("CLICKHOUSE_DSN", "clickhouse://localhost:9000?dial_timeout=invalid")
		if _, err := OpenRuntimeStore(context.Background(), &Config{MetricsStore: StoreClickHouse}); err == nil {
			t.Fatal("OpenRuntimeStore(clickhouse invalid DSN) = nil, want error")
		}

		t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/nonexistent/creds.json")
		if _, err := OpenRuntimeStore(context.Background(), bigQueryTestConfig()); err == nil {
			t.Fatal("OpenRuntimeStore(bigquery invalid creds) = nil, want error")
		}

		if _, err := OpenRuntimeStore(context.Background(), &Config{MetricsStore: "unknown"}); err == nil {
			t.Fatal("OpenRuntimeStore(unknown) = nil, want error")
		}
	})

	t.Run("runCleanupsLIFO executes reverse order and joins errors", func(t *testing.T) {
		var order []int
		errA := errors.New("errA")
		errB := errors.New("errB")
		joined := runCleanupsLIFO([]func() error{
			func() error { order = append(order, 1); return errA },
			func() error { order = append(order, 2); return nil },
			func() error { order = append(order, 3); return errB },
		})
		if len(order) != 3 || order[0] != 3 || order[1] != 2 || order[2] != 1 {
			t.Errorf("cleanup order = %v, want [3 2 1]", order)
		}
		if !errors.Is(joined, errA) || !errors.Is(joined, errB) {
			t.Errorf("joined error = %v, want wrapping errA and errB", joined)
		}
	})
}
