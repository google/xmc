// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package experiment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	pb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/validator"
)

// experimentGetters read an experiment through both the plain Repository.FetchExperiment and, in
// a transaction of its own, the locking TxRepository.FetchExperiment.
var experimentGetters = []struct {
	name  string
	fetch func(ctx context.Context, repo *Repository, xid int64) (*pb.Experiment, error)
}{
	{
		name: "Repository",
		fetch: func(ctx context.Context, repo *Repository, xid int64) (*pb.Experiment, error) {
			return repo.FetchExperiment(ctx, xid)
		},
	},
	{
		name: "TxRepository",
		fetch: func(ctx context.Context, repo *Repository, xid int64) (*pb.Experiment, error) {
			var got *pb.Experiment
			err := runInTx(ctx, repo, func(ctx context.Context, tx *TxRepository) error {
				var err error
				got, err = tx.FetchExperiment(ctx, xid)
				return err
			})
			return got, err
		},
	},
}

func TestFetchExperiment(t *testing.T) {
	for _, getter := range experimentGetters {
		t.Run(getter.name, func(t *testing.T) {
			ctx := t.Context()
			repo, _ := newTestRepository(t)

			createdExp := createBlankExperiment(ctx, t, repo, 123)
			xid, err := validator.ValidateExperimentName(createdExp.GetName())
			if err != nil {
				t.Fatalf("ValidateExperimentName(%q) unexpected error: %v", createdExp.GetName(), err)
			}

			gotExp, err := getter.fetch(ctx, repo, xid)
			if err != nil {
				t.Fatalf("%s.FetchExperiment(%d) unexpected error: %v", getter.name, xid, err)
			}

			if diff := cmp.Diff(createdExp, gotExp, protocmp.Transform()); diff != "" {
				t.Errorf("%s.FetchExperiment(%d) mismatch (-want +got):\n%s", getter.name, xid, diff)
			}
		})
	}
}

func TestFetchExperiment_PopulatedProtoRoundTrip(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	completedAt := timestamppb.New(time.Now().Add(time.Minute).Truncate(time.Microsecond))
	exp := blankExperimentProto(456)
	exp.Title = "full-experiment"
	exp.Creator = "alice@example.com"
	exp.Priority = pb.Priority_HIGH
	exp.Urls = []*pb.Url{
		{Url: "https://example.com/a", Icon: "link", DisplayName: "A", Priority: 1},
		{Url: "https://example.com/b", DisplayName: "B", Priority: 2},
	}
	exp.Tags = []string{"llm", "gpu"}
	exp.Notes = "some notes"
	exp.Resources = []*pb.ResourceSet{
		{
			Resources: &pb.ResourceSet_CpuResources{
				CpuResources: &pb.CpuResources{
					CpuMillicores:      2000,
					MemoryBytes:        4096,
					LimitCpuMillicores: 4000,
				},
			},
			EphemeralStorageBytes: 8192,
		},
	}
	exp.Status = &pb.ExperimentStatus{
		ActiveState: pb.ExperimentStatus_INACTIVE,
		LaunchState: pb.ExperimentStatus_COMPLETED,
		ChildStateCounts: []*pb.ExperimentStatus_StateCount{
			{State: pb.WorkUnitStatus_COMPLETED, Count: 3},
		},
		MessageSeverityCounts: []*pb.ExperimentStatus_MessageSeverityCount{
			{Severity: pb.StructuredMessage_WARNING, Count: 1},
		},
		StructuredStatusMessage: &pb.StructuredMessage{
			Severity:       pb.StructuredMessage_INFO,
			Source:         pb.StructuredMessage_XMANAGER_BACKEND,
			UserFacingText: "all work units finished",
		},
	}
	exp.CompleteTime = completedAt

	createdExp := createExperiment(ctx, t, repo, exp)

	for _, getter := range experimentGetters {
		got, err := getter.fetch(ctx, repo, 456)
		if err != nil {
			t.Fatalf("%s.FetchExperiment(456) unexpected error: %v", getter.name, err)
		}
		if diff := cmp.Diff(createdExp, got, protocmp.Transform()); diff != "" {
			t.Errorf("%s.FetchExperiment(456) mismatch (-want +got):\n%s", getter.name, diff)
		}
	}
}

func TestFetchExperiment_NotFound(t *testing.T) {
	for _, getter := range experimentGetters {
		t.Run(getter.name, func(t *testing.T) {
			ctx := t.Context()
			repo, _ := newTestRepository(t)

			_, err := getter.fetch(ctx, repo, 123456789)
			if !errors.Is(err, grpcerr.ErrNotFound) || status.Code(err) != codes.NotFound {
				t.Errorf("%s.FetchExperiment() error = %v (code %v), want %v (code %v)", getter.name, err, status.Code(err), grpcerr.ErrNotFound, codes.NotFound)
			}
		})
	}
}

func TestFetchExperimentStatus(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	createdExp := createBlankExperiment(ctx, t, repo, 123)
	xid, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName(%q) unexpected error: %v", createdExp.GetName(), err)
	}

	gotStatus, err := repo.FetchExperimentStatus(ctx, xid)
	if err != nil {
		t.Fatalf("FetchExperimentStatus(%d) unexpected error: %v", xid, err)
	}

	if diff := cmp.Diff(createdExp.Status, gotStatus, protocmp.Transform()); diff != "" {
		t.Errorf("FetchExperimentStatus(%d) mismatch (-want +got):\n%s", xid, diff)
	}
}

func TestFetchExperimentStatus_NotFound(t *testing.T) {
	ctx := t.Context()
	repo, _ := newTestRepository(t)

	_, err := repo.FetchExperimentStatus(ctx, 123456789)
	if !errors.Is(err, grpcerr.ErrNotFound) || status.Code(err) != codes.NotFound {
		t.Errorf("FetchExperimentStatus() error = %v (code %v), want %v (code %v)", err, status.Code(err), grpcerr.ErrNotFound, codes.NotFound)
	}
}
