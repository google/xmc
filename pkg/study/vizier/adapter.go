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
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	spb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/pkg/study"
	"xmanager_cloud/pkg/study/vizierpb"
)

const vizierOwnerParent = "owners/xmc"

// Engine implements study.StudyEngine against an OSS or BYO Vizier gRPC server.
type Engine struct{ client vizierpb.VizierServiceClient }

var _ study.StudyEngine = (*Engine)(nil)

// NewEngine constructs a Vizier StudyEngine backed by conn.
func NewEngine(conn *grpc.ClientConn) (*Engine, error) {
	if conn == nil {
		return nil, errors.New("vizier gRPC connection must not be nil")
	}
	return &Engine{client: vizierpb.NewVizierServiceClient(conn)}, nil
}

// CreateStudy idempotently registers the study for xid in Vizier under owners/xmc/studies/study-<xid>.
// It calls CreateStudy directly with an empty Study.Name because OSS Vizier 0.1.24 treats name as
// OUTPUT_ONLY, raises codes.Unknown on GetStudy for missing studies, and deduplicates CreateStudy
// by display_name. If a BYO Vizier server returns AlreadyExists, it falls back to GetStudy.
func (e *Engine) CreateStudy(ctx context.Context, xid int64, cfg *spb.StudyConfig) (string, error) {
	if e == nil || e.client == nil {
		return "", errors.New("vizier engine is not initialized")
	}
	if xid <= 0 {
		return "", fmt.Errorf("%w: xid must be positive", study.ErrInvalidStudyConfig)
	}
	spec, err := ToStudySpec(cfg)
	if err != nil {
		return "", err
	}
	displayName := fmt.Sprintf("study-%d", xid)
	studyName := fmt.Sprintf("%s/studies/%s", vizierOwnerParent, displayName)
	created, err := e.client.CreateStudy(ctx, &vizierpb.CreateStudyRequest{
		Parent: vizierOwnerParent,
		Study:  &vizierpb.Study{DisplayName: displayName, StudySpec: spec},
	})
	if err == nil {
		return cmpOrName(created.GetName(), studyName), nil
	}
	if status.Code(err) == codes.AlreadyExists {
		got, getErr := e.client.GetStudy(ctx, &vizierpb.GetStudyRequest{Name: studyName})
		if getErr == nil {
			return cmpOrName(got.GetName(), studyName), nil
		}
		err = getErr
	}
	if status.Code(err) == codes.InvalidArgument {
		return "", fmt.Errorf("%w: vizier rejected study spec", study.ErrInvalidStudyConfig)
	}
	return "", fmt.Errorf("vizier create study (code=%s): %w", status.Code(err), err)
}

func cmpOrName(got, fallback string) string {
	if got != "" {
		return got
	}
	return fallback
}
