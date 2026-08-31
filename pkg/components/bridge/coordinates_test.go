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

package bridge

import (
	"reflect"
	"testing"
)

func TestParseCoordinates(t *testing.T) {
	tests := []struct {
		name       string
		endpoint   string
		secretName string
		want       ResolvedComponentCoordinates
	}{
		{
			name:       "empty input",
			endpoint:   "",
			secretName: "",
			want:       ResolvedComponentCoordinates{},
		},
		{
			name:       "secret name only",
			endpoint:   "",
			secretName: "my-secret",
			want: ResolvedComponentCoordinates{
				SecretName: "my-secret",
			},
		},
		{
			name:       "http url with port and path",
			endpoint:   "http://ray-head.xmc.svc.cluster.local:8265/api",
			secretName: "ray-secret",
			want: ResolvedComponentCoordinates{
				Scheme:           "http",
				Host:             "ray-head.xmc.svc.cluster.local",
				Port:             "8265",
				ServiceNamespace: "xmc",
				ServiceName:      "ray-head",
				SecretName:       "ray-secret",
				RawEndpoint:      "http://ray-head.xmc.svc.cluster.local:8265/api",
			},
		},
		{
			name:       "https url standard port",
			endpoint:   "https://vllm-service.inference.svc.cluster.local:443",
			secretName: "vllm-token",
			want: ResolvedComponentCoordinates{
				Scheme:           "https",
				Host:             "vllm-service.inference.svc.cluster.local",
				Port:             "443",
				ServiceNamespace: "inference",
				ServiceName:      "vllm-service",
				SecretName:       "vllm-token",
				RawEndpoint:      "https://vllm-service.inference.svc.cluster.local:443",
			},
		},
		{
			name:       "bare host port",
			endpoint:   "clickhouse-server.analytics:9000",
			secretName: "",
			want: ResolvedComponentCoordinates{
				Scheme:           "",
				Host:             "clickhouse-server.analytics",
				Port:             "9000",
				ServiceNamespace: "analytics",
				ServiceName:      "clickhouse-server",
				SecretName:       "",
				RawEndpoint:      "clickhouse-server.analytics:9000",
			},
		},
		{
			name:       "fqdn with trailing dot",
			endpoint:   "http://service.default.svc.cluster.local.:8080",
			secretName: "",
			want: ResolvedComponentCoordinates{
				Scheme:           "http",
				Host:             "service.default.svc.cluster.local.",
				Port:             "8080",
				ServiceNamespace: "default",
				ServiceName:      "service",
				SecretName:       "",
				RawEndpoint:      "http://service.default.svc.cluster.local.:8080",
			},
		},
		{
			name:       "external domain",
			endpoint:   "https://api.openai.com:443",
			secretName: "openai-key",
			want: ResolvedComponentCoordinates{
				Scheme:           "https",
				Host:             "api.openai.com",
				Port:             "443",
				ServiceNamespace: "",
				ServiceName:      "api",
				SecretName:       "openai-key",
				RawEndpoint:      "https://api.openai.com:443",
			},
		},
		{
			name:       "simple service name without namespace",
			endpoint:   "redis-master:6379",
			secretName: "redis-auth",
			want: ResolvedComponentCoordinates{
				Scheme:           "",
				Host:             "redis-master",
				Port:             "6379",
				ServiceNamespace: "",
				ServiceName:      "redis-master",
				SecretName:       "redis-auth",
				RawEndpoint:      "redis-master:6379",
			},
		},
		{
			name:       "bracketed ipv6 address without port",
			endpoint:   "[::1]",
			secretName: "",
			want: ResolvedComponentCoordinates{
				Scheme:           "",
				Host:             "::1",
				Port:             "",
				ServiceNamespace: "",
				ServiceName:      "",
				SecretName:       "",
				RawEndpoint:      "[::1]",
			},
		},
		{
			name:       "bracketed ipv6 address with port",
			endpoint:   "http://[2001:db8::1]:8080",
			secretName: "",
			want: ResolvedComponentCoordinates{
				Scheme:           "http",
				Host:             "2001:db8::1",
				Port:             "8080",
				ServiceNamespace: "",
				ServiceName:      "",
				SecretName:       "",
				RawEndpoint:      "http://[2001:db8::1]:8080",
			},
		},
		{
			name:       "ipv4 address endpoint",
			endpoint:   "http://127.0.0.1:9090",
			secretName: "",
			want: ResolvedComponentCoordinates{
				Scheme:           "http",
				Host:             "127.0.0.1",
				Port:             "9090",
				ServiceNamespace: "",
				ServiceName:      "",
				SecretName:       "",
				RawEndpoint:      "http://127.0.0.1:9090",
			},
		},
		{
			name:       "service with svc suffix without namespace",
			endpoint:   "http://my-service.svc.cluster.local:8080",
			secretName: "",
			want: ResolvedComponentCoordinates{
				Scheme:           "http",
				Host:             "my-service.svc.cluster.local",
				Port:             "8080",
				ServiceNamespace: "",
				ServiceName:      "my-service",
				SecretName:       "",
				RawEndpoint:      "http://my-service.svc.cluster.local:8080",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseCoordinates(tt.endpoint, tt.secretName)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseCoordinates(%q, %q) = %+v, want %+v", tt.endpoint, tt.secretName, got, tt.want)
			}
		})
	}
}
