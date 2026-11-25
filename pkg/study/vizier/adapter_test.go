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

package vizier

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"xmanager_cloud/pkg/study"
	"xmanager_cloud/pkg/study/vizierpb"
)

type fakeVizierServer struct {
	vizierpb.UnimplementedVizierServiceServer
	mu         sync.Mutex
	studies    map[string]*vizierpb.Study
	createReqs []*vizierpb.CreateStudyRequest
	getReqs    []*vizierpb.GetStudyRequest
	createErr  error
	getErr     error
}

func (s *fakeVizierServer) CreateStudy(_ context.Context, req *vizierpb.CreateStudyRequest) (*vizierpb.Study, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createReqs = append(s.createReqs, proto.Clone(req).(*vizierpb.CreateStudyRequest))
	if s.createErr != nil {
		return nil, s.createErr
	}
	if s.studies == nil {
		s.studies = make(map[string]*vizierpb.Study)
	}
	for _, existing := range s.studies {
		if existing.GetDisplayName() == req.GetStudy().GetDisplayName() {
			return proto.Clone(existing).(*vizierpb.Study), nil
		}
	}
	name := req.GetParent() + "/studies/" + req.GetStudy().GetDisplayName()
	created := proto.Clone(req.GetStudy()).(*vizierpb.Study)
	created.Name = name
	s.studies[name] = created
	return proto.Clone(created).(*vizierpb.Study), nil
}

func (s *fakeVizierServer) GetStudy(_ context.Context, req *vizierpb.GetStudyRequest) (*vizierpb.Study, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getReqs = append(s.getReqs, proto.Clone(req).(*vizierpb.GetStudyRequest))
	if s.getErr != nil {
		return nil, s.getErr
	}
	if existing, ok := s.studies[req.GetName()]; ok {
		return proto.Clone(existing).(*vizierpb.Study), nil
	}
	return nil, status.Error(codes.NotFound, "not found")
}

func dialVizierBufconn(t *testing.T, srv *fakeVizierServer) *Client {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	gs := grpc.NewServer()
	if srv != nil {
		vizierpb.RegisterVizierServiceServer(gs, srv)
	}
	go func() { _ = gs.Serve(lis) }()
	c, err := Dial("127.0.0.1:7077", grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}))
	if err != nil {
		gs.Stop()
		_ = lis.Close()
		t.Fatalf("Dial() error = %v", err)
	}
	t.Cleanup(func() { _ = c.Close(); gs.Stop(); _ = lis.Close() })
	return c
}

func TestNewEngine_RejectsNilConn(t *testing.T) {
	t.Parallel()
	if _, err := NewEngine(nil); err == nil {
		t.Fatal("NewEngine(nil) = nil, want error")
	}
}

func TestEngine_CreateStudy_IdempotentAndValidRequest(t *testing.T) {
	t.Parallel()
	srv := &fakeVizierServer{}
	c := dialVizierBufconn(t, srv)
	engine, err := NewEngine(c.Conn())
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}

	cfg := validStudyConfig()
	wantSpec, err := ToStudySpec(cfg)
	if err != nil {
		t.Fatalf("ToStudySpec() error = %v", err)
	}

	name1, err := engine.CreateStudy(context.Background(), 42, cfg)
	if err != nil {
		t.Fatalf("first CreateStudy() error = %v", err)
	}
	name2, err := engine.CreateStudy(context.Background(), 42, cfg)
	if err != nil {
		t.Fatalf("second CreateStudy() error = %v", err)
	}
	if name1 != "owners/xmc/studies/study-42" || name2 != name1 {
		t.Errorf("study names = (%q, %q), want %q", name1, name2, "owners/xmc/studies/study-42")
	}
	if len(srv.studies) != 1 || len(srv.getReqs) != 0 {
		t.Fatalf("studies=%d getReqs=%d, want 1 study and 0 GetStudy calls", len(srv.studies), len(srv.getReqs))
	}
	req := srv.createReqs[0]
	if req.GetParent() != "owners/xmc" || req.GetStudy().GetName() != "" || req.GetStudy().GetDisplayName() != "study-42" {
		t.Errorf("CreateStudyRequest = parent:%q name:%q display:%q", req.GetParent(), req.GetStudy().GetName(), req.GetStudy().GetDisplayName())
	}
	if diff := cmp.Diff(wantSpec, req.GetStudy().GetStudySpec(), protocmp.Transform()); diff != "" {
		t.Errorf("StudySpec mismatch (-want +got):\n%s", diff)
	}
}

func TestEngine_CreateStudy_AlreadyExistsFallbackAndErrors(t *testing.T) {
	t.Parallel()

	t.Run("AlreadyExists falls back to GetStudy", func(t *testing.T) {
		t.Parallel()
		srv := &fakeVizierServer{
			createErr: status.Error(codes.AlreadyExists, "exists"),
			studies: map[string]*vizierpb.Study{
				"owners/xmc/studies/study-7": {Name: "owners/xmc/studies/study-7", DisplayName: "study-7"},
			},
		}
		engine, _ := NewEngine(dialVizierBufconn(t, srv).Conn())
		name, err := engine.CreateStudy(context.Background(), 7, validStudyConfig())
		if err != nil {
			t.Fatalf("CreateStudy() error = %v", err)
		}
		if name != "owners/xmc/studies/study-7" || len(srv.getReqs) != 1 || srv.getReqs[0].GetName() != "owners/xmc/studies/study-7" {
			t.Errorf("got name=%q getReqs=%v", name, srv.getReqs)
		}
	})

	t.Run("Unavailable returns retryable error", func(t *testing.T) {
		t.Parallel()
		srv := &fakeVizierServer{createErr: status.Error(codes.Unavailable, "restarting")}
		engine, _ := NewEngine(dialVizierBufconn(t, srv).Conn())
		_, err := engine.CreateStudy(context.Background(), 7, validStudyConfig())
		if err == nil || errors.Is(err, study.ErrInvalidStudyConfig) {
			t.Fatalf("CreateStudy() error = %v, want retryable non-ErrInvalidStudyConfig", err)
		}
	})

	t.Run("InvalidArgument wraps ErrInvalidStudyConfig without server payload", func(t *testing.T) {
		t.Parallel()
		const secretMsg = "SECRET_SERVER_DETAIL"
		srv := &fakeVizierServer{createErr: status.Error(codes.InvalidArgument, secretMsg)}
		engine, _ := NewEngine(dialVizierBufconn(t, srv).Conn())
		_, err := engine.CreateStudy(context.Background(), 7, validStudyConfig())
		if !errors.Is(err, study.ErrInvalidStudyConfig) {
			t.Fatalf("CreateStudy() error = %v, want ErrInvalidStudyConfig", err)
		}
		if strings.Contains(err.Error(), secretMsg) {
			t.Errorf("error %q leaked server message %q", err.Error(), secretMsg)
		}
	})

	t.Run("non-positive xid wraps ErrInvalidStudyConfig", func(t *testing.T) {
		t.Parallel()
		engine, _ := NewEngine(dialVizierBufconn(t, nil).Conn())
		if _, err := engine.CreateStudy(context.Background(), 0, validStudyConfig()); !errors.Is(err, study.ErrInvalidStudyConfig) {
			t.Fatalf("CreateStudy(0) error = %v, want ErrInvalidStudyConfig", err)
		}
	})
}
