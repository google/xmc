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

package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"xmanager_cloud/internal/security/keyreloader"
)

// BuildTLSConfig builds a crypto/tls.Config from Config, integrating keyreloader for client mTLS hot-reloading.
func BuildTLSConfig(cfg *Config) (*tls.Config, *keyreloader.KeyPairReloader, error) {
	if !cfg.TLSEnabled {
		return nil, nil, nil
	}

	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: cfg.TLSInsecureSkipVerify,
	}

	if cfg.TLSCaFile != "" {
		caCert, err := os.ReadFile(cfg.TLSCaFile)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read CA certificate file %s: %w", cfg.TLSCaFile, err)
		}
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, nil, fmt.Errorf("failed to parse CA certificate from %s", cfg.TLSCaFile)
		}
		tlsConfig.RootCAs = caCertPool
	}

	var reloader *keyreloader.KeyPairReloader
	if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
		var err error
		reloader, err = keyreloader.NewKeyPairReloader(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to initialize keyreloader: %w", err)
		}
		tlsConfig.GetClientCertificate = reloader.GetClientCertificate
	}

	return tlsConfig, reloader, nil
}
