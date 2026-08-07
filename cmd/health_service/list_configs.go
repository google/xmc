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

package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	spb "xmanager_cloud/health_service/proto/api_go_grpc_proto"
)

// readConfigDir reads all files in the given directory and returns ConfigInfo slice.
// It ignores directories and files starting with ".".
func readConfigDir(dir string) ([]*spb.ConfigInfo, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // Return nil if directory doesn't exist (optional volume)
		}
		return nil, err
	}

	var configs []*spb.ConfigInfo
	for _, f := range files {
		if f.IsDir() || strings.HasPrefix(f.Name(), ".") {
			continue
		}
		path := filepath.Join(dir, f.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		configs = append(configs, &spb.ConfigInfo{
			Key:   f.Name(),
			Value: string(content),
		})
	}
	return configs, nil
}

// listConfigs reads configurations from the mounted ConfigMap directories.
func listConfigs(configDir string) ([]*spb.ConfigGroup, error) {
	components := []struct {
		name        string
		displayName string
	}{
		{"ess", "Experiment State Server Config"},
		{"actuator", "Actuator Config"},
		{"reconciler", "Reconciler Config"},
		{"xid", "XID Service Config"},
		{"health-service", "Health Service Config"},
	}

	var groups []*spb.ConfigGroup
	for _, c := range components {
		dir := filepath.Join(configDir, c.name)
		configs, err := readConfigDir(dir)
		if err != nil {
			slog.Error("Failed to read config dir", "dir", dir, "error", err)
			return nil, err
		}
		if len(configs) > 0 {
			groups = append(groups, &spb.ConfigGroup{
				DisplayName: c.displayName,
				Configs:     configs,
			})
		}
	}

	return groups, nil
}
