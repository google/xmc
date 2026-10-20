// Copyright 2025 Google LLC
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

package auth

import (
	"testing"
	"time"
)

func TestNewAuthorizer(t *testing.T) {
	baseParams := func() AuthorizerParams {
		return AuthorizerParams{
			CacheTTL:      10 * time.Minute,
			CacheErrorTTL: time.Minute,
			IAPAudiences:  []string{"aud1", "aud2"},
			IAPIssuer:     "https://issuer.com",
			Admission:     AdmitAll(),
		}
	}
	tests := []struct {
		name    string
		mutate  func(*AuthorizerParams)
		wantErr bool
	}{
		{name: "valid parameters"},
		{name: "empty IAP issuer", mutate: func(p *AuthorizerParams) { p.IAPIssuer = "" }, wantErr: true},
		{name: "empty IAP audience", mutate: func(p *AuthorizerParams) { p.IAPAudiences = []string{"aud1", ""} }, wantErr: true},
		{name: "missing admission", mutate: func(p *AuthorizerParams) { p.Admission = nil }, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := baseParams()
			if tt.mutate != nil {
				tt.mutate(&p)
			}
			got, err := NewAuthorizer(p)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewAuthorizer() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got.admission == nil {
				t.Error("NewAuthorizer() did not install the admission checker")
			}
		})
	}
}
