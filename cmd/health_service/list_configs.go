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
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	spb "xmanager_cloud/health_service/proto/api_go_grpc_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/pkg/components/summary"
)

// readConfigDir reads all files in the given directory from fsys and returns ConfigInfo slice.
// It ignores directories and files starting with ".".
func readConfigDir(ctx context.Context, fsys fs.FS, dir string) ([]*spb.ConfigInfo, error) {
	files, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read config directory %s: %w", dir, err)
	}

	var configs []*spb.ConfigInfo
	for _, f := range files {
		if f.IsDir() || strings.HasPrefix(f.Name(), ".") {
			continue
		}
		path := filepath.Join(dir, f.Name())
		content, err := fs.ReadFile(fsys, path)
		if err != nil {
			logging.Warn(ctx, "Failed to read config file", "path", path, "error", err)
			continue
		}
		configs = append(configs, &spb.ConfigInfo{
			Key:   f.Name(),
			Value: string(content),
		})
	}
	return configs, nil
}

// listConfigs reads configurations from the mounted ConfigMap directories in fsys.
func listConfigs(ctx context.Context, fsys fs.FS) ([]*spb.ConfigGroup, error) {
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
		configs, err := readConfigDir(ctx, fsys, c.name)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				logging.Debug(ctx, "Config directory not found, skipping optional component", "dir", c.name)
				continue
			}
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

// convertComponentConfigs transforms custom component configurations into ConfigGroup objects.
func convertComponentConfigs(configs []summary.ComponentConfig) []*spb.ConfigGroup {
	groups := make([]*spb.ConfigGroup, 0, len(configs))
	for _, comp := range configs {
		if len(comp.ConfigParameters) == 0 {
			continue
		}
		infoList := make([]*spb.ConfigInfo, 0, len(comp.ConfigParameters))
		for _, param := range comp.ConfigParameters {
			infoList = append(infoList, &spb.ConfigInfo{
				Key:   param.Key,
				Value: param.Value,
			})
		}
		groups = append(groups, &spb.ConfigGroup{
			DisplayName: fmt.Sprintf("%s (Custom Component)", comp.Name),
			Configs:     infoList,
		})
	}
	return groups
}
