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
	"net"
	"net/url"
	"strings"
)

// ResolvedComponentCoordinates captures normalized networking and credential coordinates for a 3P component.
type ResolvedComponentCoordinates struct {
	Scheme           string `json:"scheme,omitempty"`
	Host             string `json:"host,omitempty"`
	Port             string `json:"port,omitempty"`
	ServiceNamespace string `json:"serviceNamespace,omitempty"`
	ServiceName      string `json:"serviceName,omitempty"`
	SecretName       string `json:"secretName,omitempty"`
	RawEndpoint      string `json:"rawEndpoint,omitempty"`
}

// ParseCoordinates parses an endpoint string (URL, host:port, or FQDN) and secret name into structured coordinates.
func ParseCoordinates(endpoint string, secretName string) ResolvedComponentCoordinates {
	raw := strings.TrimSpace(endpoint)
	if raw == "" && secretName == "" {
		return ResolvedComponentCoordinates{}
	}

	res := ResolvedComponentCoordinates{
		SecretName:  strings.TrimSpace(secretName),
		RawEndpoint: raw,
	}

	if raw == "" {
		return res
	}

	scheme, hostPart := extractSchemeAndHost(raw)
	res.Scheme = scheme

	host, port := extractHostAndPort(hostPart)
	res.Host = host
	res.Port = port

	service, namespace := parseServiceAndNamespace(host)
	res.ServiceName = service
	res.ServiceNamespace = namespace

	return res
}

func extractSchemeAndHost(raw string) (string, string) {
	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(raw)
		if err == nil {
			return strings.ToLower(parsed.Scheme), parsed.Host
		}
	}
	return "", raw
}

func extractHostAndPort(hostPart string) (string, string) {
	if h, p, err := net.SplitHostPort(hostPart); err == nil {
		return h, p
	}
	if strings.HasPrefix(hostPart, "[") && strings.HasSuffix(hostPart, "]") {
		return hostPart[1 : len(hostPart)-1], ""
	}
	return hostPart, ""
}

func parseServiceAndNamespace(host string) (string, string) {
	if net.ParseIP(host) != nil {
		return "", ""
	}
	cleanHost := strings.TrimSuffix(host, ".")
	parts := strings.Split(cleanHost, ".")

	svcIdx := -1
	for i, part := range parts {
		if strings.ToLower(part) == "svc" {
			svcIdx = i
			break
		}
	}

	if svcIdx != -1 {
		if svcIdx >= 2 {
			return parts[0], parts[1]
		}
		if svcIdx == 1 {
			return parts[0], ""
		}
	}

	if len(parts) == 2 {
		tlds := map[string]bool{"com": true, "io": true, "net": true, "org": true, "ai": true, "dev": true, "edu": true, "gov": true}
		if !tlds[strings.ToLower(parts[1])] {
			return parts[0], parts[1]
		}
	}
	if len(parts) >= 1 {
		return parts[0], ""
	}
	return "", ""
}
