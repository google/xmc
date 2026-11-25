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
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	xidmsgpb "xmanager_cloud/api/protos/xid"
)

// mockXidClient is a mock implementation of the XidServiceClient.
type mockXidClient struct {
	xidmsgpb.XidServiceClient
	mu                                                                                                 sync.Mutex
	getXidEntryErr, createXidEntryErr, checkUserAuthorizationErr, updateXidEntryErr, listXidEntriesErr error
	getXidEntryResp, createXidEntryResp, updateXidEntryResp                                            *xidmsgpb.XidEntry
	getXidEntryCalls, createXidEntryCalls                                                              int
	createXidEntryResps                                                                                []*xidmsgpb.XidEntry
	createXidEntryErrs, createXidEntryCtxErrs                                                          []error
	gotCreateXidEntryReq                                                                               *xidmsgpb.CreateXidEntryRequest
	createXidEntryMD                                                                                   []metadata.MD
	createXidEntryDeadlines                                                                            []time.Time
	checkUserAuthorizationResp                                                                         *xidmsgpb.CheckUserAuthorizationResponse
	listXidEntriesResp                                                                                 *xidmsgpb.ListXidEntriesResponse
	gotListXidEntriesReq                                                                               *xidmsgpb.ListXidEntriesRequest
}

func (m *mockXidClient) GetXidEntry(_ context.Context, _ *xidmsgpb.GetXidEntryRequest, _ ...grpc.CallOption) (*xidmsgpb.XidEntry, error) {
	m.getXidEntryCalls++
	return m.getXidEntryResp, m.getXidEntryErr
}

func (m *mockXidClient) CreateXidEntry(ctx context.Context, in *xidmsgpb.CreateXidEntryRequest, _ ...grpc.CallOption) (*xidmsgpb.XidEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := m.createXidEntryCalls
	m.createXidEntryCalls, m.gotCreateXidEntryReq = idx+1, in
	md, _ := metadata.FromOutgoingContext(ctx)
	dl, _ := ctx.Deadline()
	m.createXidEntryMD, m.createXidEntryCtxErrs, m.createXidEntryDeadlines = append(m.createXidEntryMD, md), append(m.createXidEntryCtxErrs, ctx.Err()), append(m.createXidEntryDeadlines, dl)
	if idx < len(m.createXidEntryErrs) && m.createXidEntryErrs[idx] != nil {
		return nil, m.createXidEntryErrs[idx]
	}
	if idx < len(m.createXidEntryResps) {
		return m.createXidEntryResps[idx], nil
	}
	return m.createXidEntryResp, m.createXidEntryErr
}

func (m *mockXidClient) CheckUserAuthorization(_ context.Context, _ *xidmsgpb.CheckUserAuthorizationRequest, _ ...grpc.CallOption) (*xidmsgpb.CheckUserAuthorizationResponse, error) {
	return m.checkUserAuthorizationResp, m.checkUserAuthorizationErr
}

func (m *mockXidClient) UpdateXidEntry(_ context.Context, _ *xidmsgpb.UpdateXidEntryRequest, _ ...grpc.CallOption) (*xidmsgpb.XidEntry, error) {
	return m.updateXidEntryResp, m.updateXidEntryErr
}

func (m *mockXidClient) ListXidEntries(_ context.Context, in *xidmsgpb.ListXidEntriesRequest, _ ...grpc.CallOption) (*xidmsgpb.ListXidEntriesResponse, error) {
	m.gotListXidEntriesReq = in
	return m.listXidEntriesResp, m.listXidEntriesErr
}
