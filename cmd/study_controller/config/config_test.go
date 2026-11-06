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

package config

import (
	"flag"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestRegisterFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want Config
	}{
		{name: "defaults", args: nil, want: Config{Port: DefaultPort}},
		{name: "explicit port", args: []string{"-port=9090"}, want: Config{Port: "9090"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			got := RegisterFlags(fs)
			if err := fs.Parse(tc.args); err != nil {
				t.Fatalf("fs.Parse(%v) error = %v", tc.args, err)
			}
			if diff := cmp.Diff(tc.want, *got); diff != "" {
				t.Errorf("RegisterFlags() config mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestConfig_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		port    string
		wantErr bool
	}{
		{name: "default port", port: DefaultPort},
		{name: "minimum port", port: "1"},
		{name: "maximum port", port: "65535"},
		{name: "empty port", port: "", wantErr: true},
		{name: "non-numeric port", port: "http", wantErr: true},
		{name: "zero port", port: "0", wantErr: true},
		{name: "negative port", port: "-80", wantErr: true},
		{name: "port out of range", port: "65536", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := Config{Port: tc.port}.Validate()
			if (err != nil) != tc.wantErr {
				t.Errorf("Config{Port: %q}.Validate() error = %v, wantErr %v", tc.port, err, tc.wantErr)
			}
		})
	}
}
