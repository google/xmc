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

package main

import (
	"strings"
	"testing"
)

func assertViolation(t *testing.T, violations []string, wantSubstr string) {
	t.Helper()
	if len(violations) == 0 {
		t.Fatalf("expected violation containing %q, got 0 violations", wantSubstr)
	}
	for _, v := range violations {
		if strings.Contains(v, wantSubstr) {
			return
		}
	}
	t.Errorf("expected violation substring %q in %v", wantSubstr, violations)
}

func assertNoViolations(t *testing.T, violations []string) {
	t.Helper()
	if len(violations) > 0 {
		t.Errorf("expected 0 violations, got: %v", violations)
	}
}

func TestCheckSource(t *testing.T) {
	tests := []struct {
		name          string
		src           string
		wantViolation bool
		wantSubstr    string
	}{
		{
			name: "clean file passes",
			src: `package main
import "testing"
func TestClean(t *testing.T) {
	t.Setenv("FOO", "BAR")
}
`,
			wantViolation: false,
		},
		{
			name: "os.Setenv flags violation",
			src: `package main
import (
	"os"
	"testing"
)
func TestPollute(t *testing.T) {
	os.Setenv("FOO", "BAR")
}
`,
			wantViolation: true,
			wantSubstr:    "os.Setenv used in test",
		},
		{
			name: "os.Setenv with nolint:determinism is suppressed",
			src: `package main
import (
	"os"
	"testing"
)
func TestSuppressed(t *testing.T) {
	os.Setenv("FOO", "BAR") // nolint:determinism
}
`,
			wantViolation: false,
		},
		{
			name: "long sleep flags violation",
			src: `package main
import (
	"testing"
	"time"
)
func TestSleep(t *testing.T) {
	time.Sleep(15 * time.Second)
}
`,
			wantViolation: true,
			wantSubstr:    "arbitrary second-level time.Sleep in test",
		},
		{
			name: "reverse operand sleep flags violation",
			src: `package main
import (
	"testing"
	"time"
)
func TestReverse(t *testing.T) {
	time.Sleep(time.Second * 5)
}
`,
			wantViolation: true,
			wantSubstr:    "arbitrary second-level time.Sleep in test",
		},
		{
			name: "minute sleep flags violation",
			src: `package main
import (
	"testing"
	"time"
)
func TestMinute(t *testing.T) {
	time.Sleep(1 * time.Minute)
}
`,
			wantViolation: true,
			wantSubstr:    "arbitrary second-level time.Sleep in test",
		},
		{
			name: "nested multiplication sleep flags violation",
			src: `package main
import (
	"testing"
	"time"
)
func TestNested(t *testing.T) {
	time.Sleep(5 * time.Second * 2)
}
`,
			wantViolation: true,
			wantSubstr:    "arbitrary second-level time.Sleep in test",
		},
		{
			name: "subtraction sleep flags violation",
			src: `package main
import (
	"testing"
	"time"
)
func TestSub(t *testing.T) {
	time.Sleep(5*time.Second - 100*time.Millisecond)
}
`,
			wantViolation: true,
			wantSubstr:    "arbitrary second-level time.Sleep in test",
		},
		{
			name: "division sleep flags violation",
			src: `package main
import (
	"testing"
	"time"
)
func TestDiv(t *testing.T) {
	time.Sleep(time.Minute / 2)
}
`,
			wantViolation: true,
			wantSubstr:    "arbitrary second-level time.Sleep in test",
		},
		{
			name: "parenthesized sleep flags violation",
			src: `package main
import (
	"testing"
	"time"
)
func TestParen(t *testing.T) {
	time.Sleep((15 * time.Second))
}
`,
			wantViolation: true,
			wantSubstr:    "arbitrary second-level time.Sleep in test",
		},
		{
			name: "bare second sleep flags violation",
			src: `package main
import (
	"testing"
	"time"
)
func TestBare(t *testing.T) {
	time.Sleep(time.Second)
}
`,
			wantViolation: true,
			wantSubstr:    "arbitrary second-level time.Sleep in test",
		},
		{
			name: "type conversion sleep flags violation",
			src: `package main
import (
	"testing"
	"time"
)
func TestConversion(t *testing.T) {
	time.Sleep(time.Duration(5 * time.Second))
}
`,
			wantViolation: true,
			wantSubstr:    "arbitrary second-level time.Sleep in test",
		},
		{
			name: "sleep with nolint:determinism is suppressed",
			src: `package main
import (
	"testing"
	"time"
)
func TestSuppressedSleep(t *testing.T) {
	time.Sleep(15 * time.Second) // nolint:determinism
}
`,
			wantViolation: false,
		},
		{
			name: "millisecond sleep passes",
			src: `package main
import (
	"testing"
	"time"
)
func TestFast(t *testing.T) {
	time.Sleep(500 * time.Millisecond)
}
`,
			wantViolation: false,
		},
		{
			name: "multiline sleep flags violation",
			src: `package main
import (
	"testing"
	"time"
)
func TestMultiline(t *testing.T) {
	time.Sleep(
		15 * time.Second,
	)
}
`,
			wantViolation: true,
			wantSubstr:    "arbitrary second-level time.Sleep in test",
		},
		{
			name: "multiline sleep with inline nolint is suppressed",
			src: `package main
import (
	"testing"
	"time"
)
func TestMultilineSuppressed(t *testing.T) {
	time.Sleep(
		15 * time.Second, // nolint:sleep
	)
}
`,
			wantViolation: false,
		},
		{
			name: "block comment nolint is suppressed",
			src: `package main
import (
	"testing"
	"time"
)
func TestBlockSuppressed(t *testing.T) {
	/* nolint:sleep */
	time.Sleep(15 * time.Second)
}
`,
			wantViolation: false,
		},
		{
			name: "code inside comment is ignored",
			src: `package main
import "testing"
func TestComment(t *testing.T) {
	// os.Setenv("A", "B")
	/* time.Sleep(15 * time.Second) */
}
`,
			wantViolation: false,
		},
		{
			name: "code inside string literal is ignored",
			src: `package main
import "testing"
func TestString(t *testing.T) {
	s := "os.Setenv(\"A\", \"B\") time.Sleep(15 * time.Second)"
	t.Log(s)
}
`,
			wantViolation: false,
		},
		{
			name: "rune literal with quotes parses cleanly",
			src: `package main
import (
	"os"
	"testing"
)
func TestRune(t *testing.T) {
	q := '"'
	_ = q
	os.Setenv("A", "B")
}
`,
			wantViolation: true,
			wantSubstr:    "os.Setenv used in test",
		},
		{
			name: "custom struct Setenv is not flagged",
			src: `package main
import "testing"
type MockOS struct{}
func (m *MockOS) Setenv(k, v string) {}
func TestStruct(t *testing.T) {
	mock := &MockOS{}
	mock.Setenv("A", "B")
}
`,
			wantViolation: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			violations, err := CheckSource("test.go", tc.src)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantViolation {
				assertViolation(t, violations, tc.wantSubstr)
				return
			}
			assertNoViolations(t, violations)
		})
	}
}

func TestIsExcluded(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"vendor/pkg/foo_test.go", true},
		{"node_modules/foo/bar_test.go", true},
		{"bazel-bin/cmd/foo_test.go", true},
		{"bazel-out/cmd/foo_test.go", true},
		{"bazel-my-custom-workspace/foo_test.go", true},
		{"cmd/health_service/server_test.go", false},
		{"pkg/auth/oauth2/config_test.go", false},
		{"pkg/build/bazel-runner_test.go", false},
	}

	for _, tc := range tests {
		got := isExcluded(tc.path)
		if got != tc.want {
			t.Errorf("isExcluded(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestCollectTargetFilesNonExistent(t *testing.T) {
	_, err := collectTargetFiles([]string{"non_existent_file_test.go"})
	if err == nil {
		t.Errorf("expected error for non-existent file, got nil")
	}
}
