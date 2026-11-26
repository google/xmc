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
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"xmanager_cloud/internal/experiment/experimenttest"
	"xmanager_cloud/pkg/study"
	"xmanager_cloud/pkg/study/vizierpb"
)

type fakeVizierServer struct {
	vizierpb.UnimplementedVizierServiceServer
	mu          sync.Mutex
	studies     map[string]*vizierpb.Study
	createReqs  []*vizierpb.CreateStudyRequest
	getReqs     []*vizierpb.GetStudyRequest
	createErr   error
	getErr      error
	omitResName bool
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
	if !s.omitResName {
		created.Name = name
	}
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

func newBufconnEngine(t *testing.T, srv *fakeVizierServer) *Engine {
	t.Helper()
	c, _ := dialBufconn(t, func(gs *grpc.Server) {
		if srv != nil {
			vizierpb.RegisterVizierServiceServer(gs, srv)
		}
	})
	engine, err := NewEngine(c)
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	return engine
}

func TestNewEngine_RejectsNilClient(t *testing.T) {
	t.Parallel()
	if _, err := NewEngine(nil); err == nil {
		t.Fatal("NewEngine(nil) = nil, want error")
	}
	if _, err := NewEngine(&Client{}); err == nil {
		t.Fatal("NewEngine(empty) = nil, want error")
	}
	var nilEngine *Engine
	if _, err := nilEngine.CreateStudy(context.Background(), 1, experimenttest.StudyConfig()); err == nil {
		t.Fatal("(*Engine)(nil).CreateStudy() = nil, want error")
	}
}

func TestEngine_CreateStudy_IdempotentAndValidRequest(t *testing.T) {
	t.Parallel()
	srv := &fakeVizierServer{}
	engine := newBufconnEngine(t, srv)
	cfg := experimenttest.StudyConfig()
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

func TestEngine_CreateStudy_FallbacksAndErrors(t *testing.T) {
	t.Parallel()

	t.Run("AlreadyExists falls back to GetStudy", func(t *testing.T) {
		t.Parallel()
		srv := &fakeVizierServer{
			createErr: status.Error(codes.AlreadyExists, "exists"),
			studies:   map[string]*vizierpb.Study{"owners/xmc/studies/study-7": {Name: "owners/xmc/studies/study-7", DisplayName: "study-7"}},
		}
		name, err := newBufconnEngine(t, srv).CreateStudy(context.Background(), 7, experimenttest.StudyConfig())
		if err != nil || name != "owners/xmc/studies/study-7" || len(srv.getReqs) != 1 {
			t.Fatalf("got name=%q err=%v getReqs=%v", name, err, srv.getReqs)
		}
	})

	t.Run("AlreadyExists GetStudy InvalidArgument stays retryable", func(t *testing.T) {
		t.Parallel()
		srv := &fakeVizierServer{
			createErr: status.Error(codes.AlreadyExists, "exists"),
			getErr:    status.Error(codes.InvalidArgument, "bad get"),
		}
		_, err := newBufconnEngine(t, srv).CreateStudy(context.Background(), 7, experimenttest.StudyConfig())
		if err == nil || errors.Is(err, study.ErrInvalidStudyConfig) {
			t.Fatalf("CreateStudy() error = %v, want retryable non-ErrInvalidStudyConfig", err)
		}
	})

	t.Run("empty Study.Name response falls back to constructed name", func(t *testing.T) {
		t.Parallel()
		name, err := newBufconnEngine(t, &fakeVizierServer{omitResName: true}).CreateStudy(context.Background(), 9, experimenttest.StudyConfig())
		if err != nil || name != "owners/xmc/studies/study-9" {
			t.Fatalf("got name=%q err=%v, want owners/xmc/studies/study-9", name, err)
		}
	})

	t.Run("Unavailable returns retryable error", func(t *testing.T) {
		t.Parallel()
		_, err := newBufconnEngine(t, &fakeVizierServer{createErr: status.Error(codes.Unavailable, "restarting")}).CreateStudy(context.Background(), 7, experimenttest.StudyConfig())
		if err == nil || errors.Is(err, study.ErrInvalidStudyConfig) {
			t.Fatalf("CreateStudy() error = %v, want retryable non-ErrInvalidStudyConfig", err)
		}
	})

	t.Run("InvalidArgument wraps ErrInvalidStudyConfig without server payload", func(t *testing.T) {
		t.Parallel()
		const secretMsg = "SECRET_SERVER_DETAIL"
		_, err := newBufconnEngine(t, &fakeVizierServer{createErr: status.Error(codes.InvalidArgument, secretMsg)}).CreateStudy(context.Background(), 7, experimenttest.StudyConfig())
		if !errors.Is(err, study.ErrInvalidStudyConfig) || strings.Contains(err.Error(), secretMsg) {
			t.Fatalf("CreateStudy() error = %v, want ErrInvalidStudyConfig without %q", err, secretMsg)
		}
	})

	t.Run("invalid xid or StudyConfig short-circuits before RPC", func(t *testing.T) {
		t.Parallel()
		srv := &fakeVizierServer{}
		engine := newBufconnEngine(t, srv)
		if _, err := engine.CreateStudy(context.Background(), 0, experimenttest.StudyConfig()); !errors.Is(err, study.ErrInvalidStudyConfig) {
			t.Fatalf("CreateStudy(0) error = %v, want ErrInvalidStudyConfig", err)
		}
		if _, err := engine.CreateStudy(context.Background(), 5, nil); !errors.Is(err, study.ErrInvalidStudyConfig) {
			t.Fatalf("CreateStudy(nil) error = %v, want ErrInvalidStudyConfig", err)
		}
		if len(srv.createReqs) != 0 {
			t.Fatalf("CreateStudy RPC calls = %d, want 0", len(srv.createReqs))
		}
	})
}
