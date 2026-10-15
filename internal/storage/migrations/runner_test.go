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

package migrations

import (
	"testing"

	"k8s.io/utils/ptr"
)

func TestParseArg(t *testing.T) {
	tests := []struct {
		name              string
		cmd               string
		args              []string
		wantArg           *int
		wantMigrationName string
		wantErr           bool
	}{
		{
			name:    "NoArg",
			args:    []string{},
			wantArg: nil,
			wantErr: false,
		},
		{
			name:    "NumberArg",
			args:    []string{"123"},
			wantArg: ptr.To(123),
			wantErr: false,
		},
		{
			name:    "InvalidArg",
			args:    []string{"abc"},
			wantArg: nil,
			wantErr: true,
		},
		{
			name:    "TooManyArgs",
			args:    []string{"123", "456"},
			wantArg: nil,
			wantErr: true,
		},
		{
			name:              "Create",
			cmd:               "create",
			args:              []string{"new_migration"},
			wantMigrationName: "new_migration",
			wantErr:           false,
		},
		{
			name:              "Create_NoArg",
			cmd:               "create",
			args:              []string{},
			wantMigrationName: "",
			wantErr:           false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			arg, migrationName, err := ParseArg(tc.cmd, tc.args)

			if (err != nil) != tc.wantErr {
				t.Errorf("ParseArg(%v) = %v, want error presence %v", tc.args, err, tc.wantErr)
			}

			if (arg == nil) != (tc.wantArg == nil) {
				t.Errorf("ParseArg(%v).arg nil mismatch: got %v, want %v", tc.args, arg, tc.wantArg)
			} else if arg != nil && *arg != *tc.wantArg {
				t.Errorf("ParseArg(%v).arg = %d, want %d", tc.args, *arg, *tc.wantArg)
			}

			if migrationName != tc.wantMigrationName {
				t.Errorf("ParseArg(%v).migrationName = %q, want %q", tc.args, migrationName, tc.wantMigrationName)
			}
		})
	}
}
