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

package server

import (
	"context"
	"sync"

	"google.golang.org/protobuf/proto"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
)

// recordingStorage wraps MockStorage to record CreateArtifact/UpdateArtifact calls, context errors,
// and optional blocking or panic injection for async observer tests.
type recordingStorage struct {
	*storage.MockStorage
	mu                                       sync.Mutex
	panicCreateArtifact, panicUpdateArtifact bool
	blockCreate                              chan struct{}
	createdArtifacts                         []*essmsgpb.Artifact
	createCtxErrs, updateCtxErrs             []error
	updateUpdates                            []*storage.ArtifactUpdate
}

func (s *recordingStorage) CreateArtifact(ctx context.Context, artifact *essmsgpb.Artifact) (*essmsgpb.Artifact, error) {
	if s.blockCreate != nil {
		<-s.blockCreate
	}
	if s.panicCreateArtifact {
		panic("unexpected CreateArtifact panic")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createCtxErrs = append(s.createCtxErrs, ctx.Err())
	created, err := s.MockStorage.CreateArtifact(ctx, artifact)
	if err != nil {
		return nil, err
	}
	s.createdArtifacts = append(s.createdArtifacts, proto.Clone(created).(*essmsgpb.Artifact))
	return created, nil
}

func (s *recordingStorage) UpdateArtifact(ctx context.Context, update *storage.ArtifactUpdate) (storage.UpdatedArtifacts, error) {
	clonedUpdate := &storage.ArtifactUpdate{AID: update.AID, Artifact: proto.Clone(update.Artifact).(*essmsgpb.Artifact), UpdateMask: update.UpdateMask}
	s.mu.Lock()
	s.updateUpdates, s.updateCtxErrs = append(s.updateUpdates, clonedUpdate), append(s.updateCtxErrs, ctx.Err())
	shouldPanic := s.panicUpdateArtifact
	s.mu.Unlock()
	if shouldPanic {
		panic("unexpected storage update panic")
	}
	return s.MockStorage.UpdateArtifact(ctx, update)
}
