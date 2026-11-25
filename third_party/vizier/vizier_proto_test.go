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

package vizier

import "testing"

func TestGeneratedStudyTypes(t *testing.T) {
	tests := []struct {
		name string
		req  *CreateStudyRequest
	}{
		{
			name: "create study request with study spec and parameter spec",
			req: &CreateStudyRequest{
				Parent: "owners/xmc",
				Study: &Study{
					DisplayName: "test-study",
					StudySpec: &StudySpec{
						Parameters: []*StudySpec_ParameterSpec{
							{
								ParameterId: "learning_rate",
								ParameterValueSpec: &StudySpec_ParameterSpec_DoubleValueSpec_{
									DoubleValueSpec: &StudySpec_ParameterSpec_DoubleValueSpec{
										MinValue: 0.001,
										MaxValue: 0.1,
									},
								},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.req.GetParent() != "owners/xmc" {
				t.Fatalf("GetParent() = %q, want %q", tt.req.GetParent(), "owners/xmc")
			}
			study := tt.req.GetStudy()
			if study.GetDisplayName() != "test-study" {
				t.Fatalf("GetDisplayName() = %q, want %q", study.GetDisplayName(), "test-study")
			}
			params := study.GetStudySpec().GetParameters()
			if len(params) != 1 || params[0].GetParameterId() != "learning_rate" {
				t.Fatalf("GetParameters() = %v, want 1 parameter named learning_rate", params)
			}
		})
	}
}

func TestVizierServiceDescriptor(t *testing.T) {
	const wantServiceName = "vizier.VizierService"
	if VizierService_ServiceDesc.ServiceName != wantServiceName {
		t.Fatalf("ServiceName = %q, want %q", VizierService_ServiceDesc.ServiceName, wantServiceName)
	}

	registered := make(map[string]bool, len(VizierService_ServiceDesc.Methods))
	for _, m := range VizierService_ServiceDesc.Methods {
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
