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
	"io"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"
	spb "github.com/google/xmc/api/protos/health"
	"github.com/google/xmc/pkg/components/summary"
	"google.golang.org/protobuf/testing/protocmp"
)

func TestListConfigs(t *testing.T) {
	tests := []struct {
		name    string
		fsys    fs.FS
		want    []*spb.ConfigGroup
		wantErr bool
	}{
		{
			name: "Success",
			fsys: fstest.MapFS{
				"ess/ess_key":           &fstest.MapFile{Data: []byte("ess_val")},
				"health-service/hs_key": &fstest.MapFile{Data: []byte("hs_val")},
			},
			want: []*spb.ConfigGroup{
				{
					DisplayName: "Experiment State Server Config",
					Configs: []*spb.ConfigInfo{
						{Key: "ess_key", Value: "ess_val"},
					},
				},
				{
					DisplayName: "Health Service Config",
					Configs: []*spb.ConfigInfo{
						{Key: "hs_key", Value: "hs_val"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "HiddenFilesAndDirsIgnored",
			fsys: fstest.MapFS{
				"ess/valid_key":   &fstest.MapFile{Data: []byte("val")},
				"ess/.hidden_key": &fstest.MapFile{Data: []byte("val")},
				"ess/subdir/":     &fstest.MapFile{Mode: fs.ModeDir},
			},
			want: []*spb.ConfigGroup{
				{
					DisplayName: "Experiment State Server Config",
					Configs: []*spb.ConfigInfo{
						{Key: "valid_key", Value: "val"},
					},
				},
			},
			wantErr: false,
		},
		{
			name:    "MissingDirsSkipped",
			fsys:    fstest.MapFS{},
			want:    nil,
			wantErr: false,
		},
		{
			name: "UnreadableFileIgnored",
			fsys: errorFileFS{
				FS: fstest.MapFS{
					"ess/valid_key":      &fstest.MapFile{Data: []byte("val")},
					"ess/unreadable_key": &fstest.MapFile{Data: []byte("secret")},
				},
				failFile: "ess/unreadable_key",
				err:      fs.ErrPermission,
			},
			want: []*spb.ConfigGroup{
				{
					DisplayName: "Experiment State Server Config",
					Configs: []*spb.ConfigInfo{
						{Key: "valid_key", Value: "val"},
					},
				},
			},
			wantErr: false,
		},
		{
			name:    "UnreadableDirFails",
			fsys:    errorFS{readDirErr: fs.ErrPermission},
			want:    nil,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := listConfigs(context.Background(), tc.fsys)
			if (err != nil) != tc.wantErr {
				t.Fatalf("listConfigs() error = %v, wantErr = %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("listConfigs() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// errorFS implements fs.FS to return directory errors deterministically.
type errorFS struct {
	readDirErr error
}

func (e errorFS) Open(name string) (fs.File, error) {
	return nil, fs.ErrNotExist
}

func (e errorFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return nil, e.readDirErr
}

// errorFileFS wraps a filesystem and injects read errors for a specific file.
type errorFileFS struct {
	fs.FS
	failFile string
	err      error
}

func (e errorFileFS) Open(name string) (fs.File, error) {
	if name == e.failFile {
		return nil, e.err
	}
	return e.FS.Open(name)
}

func (e errorFileFS) ReadFile(name string) ([]byte, error) {
	if name == e.failFile {
		return nil, e.err
	}
	if readFileFS, ok := e.FS.(fs.ReadFileFS); ok {
		return readFileFS.ReadFile(name)
	}
	f, err := e.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func TestConvertComponentConfigs(t *testing.T) {
	configs := []summary.ComponentConfig{
		{
			Name: "clickhouse",
			ConfigParameters: []summary.ConfigParameter{
				{Key: "type", Value: "COMPONENT_TYPE_CUSTOM_3P_WRAPPER"},
				{Key: "version", Value: "v1"},
			},
		},
		{
			Name:             "empty-comp",
			ConfigParameters: nil,
		},
	}

	got := convertComponentConfigs(configs)
	want := []*spb.ConfigGroup{
		{
			DisplayName: "clickhouse (Custom Component)",
			Configs: []*spb.ConfigInfo{
				{Key: "type", Value: "COMPONENT_TYPE_CUSTOM_3P_WRAPPER"},
				{Key: "version", Value: "v1"},
			},
		},
	}

	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("convertComponentConfigs() mismatch (-want +got):\n%s", diff)
	}
}
