// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package vizierpb_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"xmanager_cloud/pkg/study/vizierpb"
)

var _ vizierpb.VizierServiceClient = vizierpb.NewVizierServiceClient(nil)

func TestGeneratedStudyTypes(t *testing.T) {
	tests := []struct {
		name string
		req  *vizierpb.CreateStudyRequest
	}{
		{
			name: "create study request with double parameter and maximize metric",
			req: &vizierpb.CreateStudyRequest{
				Parent: "owners/xmc",
				Study: &vizierpb.Study{
					DisplayName: "test-study",
					StudySpec: &vizierpb.StudySpec{
						Parameters: []*vizierpb.StudySpec_ParameterSpec{
							{
								ParameterId: "learning_rate",
								ScaleType:   vizierpb.StudySpec_ParameterSpec_UNIT_LOG_SCALE,
								ParameterValueSpec: &vizierpb.StudySpec_ParameterSpec_DoubleValueSpec_{
									DoubleValueSpec: &vizierpb.StudySpec_ParameterSpec_DoubleValueSpec{
										MinValue: 0.001,
										MaxValue: 0.1,
									},
								},
							},
						},
						Metrics: []*vizierpb.StudySpec_MetricSpec{
							{
								MetricId: "accuracy",
								Goal:     vizierpb.StudySpec_MetricSpec_MAXIMIZE,
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := proto.Marshal(tt.req)
			if err != nil {
				t.Fatalf("proto.Marshal(%v) failed: %v", tt.req, err)
			}
			var got vizierpb.CreateStudyRequest
			if err := proto.Unmarshal(raw, &got); err != nil {
				t.Fatalf("proto.Unmarshal() failed: %v", err)
			}
			if diff := cmp.Diff(tt.req, &got, protocmp.Transform()); diff != "" {
				t.Fatalf("CreateStudyRequest round-trip mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestVizierServiceDescriptor(t *testing.T) {
	const wantServiceName = "vizier.VizierService"
	if vizierpb.VizierService_ServiceDesc.ServiceName != wantServiceName {
		t.Fatalf("ServiceName = %q, want %q", vizierpb.VizierService_ServiceDesc.ServiceName, wantServiceName)
	}

	registered := make(map[string]bool, len(vizierpb.VizierService_ServiceDesc.Methods))
	for _, m := range vizierpb.VizierService_ServiceDesc.Methods {
		registered[m.MethodName] = true
	}

	for _, rpcName := range []string{"CreateStudy", "GetStudy"} {
		t.Run(rpcName, func(t *testing.T) {
			if !registered[rpcName] {
				t.Fatalf("method %q not registered on %s", rpcName, wantServiceName)
			}
		})
	}
}
