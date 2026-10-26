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
package configutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStringEnv(t *testing.T) {
	t.Setenv("TEST_STRING_VAR", "hello")

	var val string
	StringEnv(&val, "TEST_STRING_VAR", "default")
	if val != "hello" {
		t.Errorf("StringEnv got %q, want %q", val, "hello")
	}

	StringEnv(&val, "NON_EXISTENT_VAR", "default")
	if val != "default" {
		t.Errorf("StringEnv got %q, want %q", val, "default")
	}
}

func TestIntEnv(t *testing.T) {
	t.Setenv("TEST_INT_VAR", "42")

	var val int
	IntEnv(&val, "TEST_INT_VAR", 10)
	if val != 42 {
		t.Errorf("IntEnv got %d, want %d", val, 42)
	}

	t.Setenv("TEST_INT_VAR", "invalid")
	IntEnv(&val, "TEST_INT_VAR", 10)
	if val != 10 {
		t.Errorf("IntEnv got %d, want %d (default on parse error)", val, 10)
	}

	IntEnv(&val, "NON_EXISTENT_VAR", 10)
	if val != 10 {
		t.Errorf("IntEnv got %d, want %d", val, 10)
	}
}

func TestInt64Env(t *testing.T) {
	t.Setenv("TEST_INT64_VAR", "123456789012345")

	var val int64
	Int64Env(&val, "TEST_INT64_VAR", 10)
	if val != 123456789012345 {
		t.Errorf("Int64Env got %d, want %d", val, 123456789012345)
	}

	t.Setenv("TEST_INT64_VAR", "invalid")
	Int64Env(&val, "TEST_INT64_VAR", 10)
	if val != 10 {
		t.Errorf("Int64Env got %d, want %d (default on parse error)", val, 10)
	}
}

func TestBoolEnv(t *testing.T) {
	t.Setenv("TEST_BOOL_VAR", "true")

	var val bool
	BoolEnv(&val, "TEST_BOOL_VAR", false)
	if !val {
		t.Errorf("BoolEnv got %t, want true", val)
	}

	t.Setenv("TEST_BOOL_VAR", "invalid")
	BoolEnv(&val, "TEST_BOOL_VAR", false)
	if val {
		t.Errorf("BoolEnv got %t, want false (default on parse error)", val)
	}

	BoolEnv(&val, "NON_EXISTENT_VAR", true)
	if !val {
		t.Errorf("BoolEnv got %t, want true", val)
	}
}

func TestDurationEnv(t *testing.T) {
	t.Setenv("TEST_DURATION_VAR", "5m")

	var val time.Duration
	DurationEnv(&val, "TEST_DURATION_VAR", 10*time.Second)
	if val != 5*time.Minute {
		t.Errorf("DurationEnv got %s, want %s", val, 5*time.Minute)
	}

	t.Setenv("TEST_DURATION_VAR", "invalid")
	DurationEnv(&val, "TEST_DURATION_VAR", 10*time.Second)
	if val != 10*time.Second {
		t.Errorf("DurationEnv got %s, want %s (default on parse error)", val, 10*time.Second)
	}
}

func TestFloat64Env(t *testing.T) {
	t.Setenv("TEST_FLOAT_VAR", "3.14")

	var val float64
	Float64Env(&val, "TEST_FLOAT_VAR", 1.0)
	if val != 3.14 {
		t.Errorf("Float64Env got %f, want %f", val, 3.14)
	}

	t.Setenv("TEST_FLOAT_VAR", "invalid")
	Float64Env(&val, "TEST_FLOAT_VAR", 1.0)
	if val != 1.0 {
		t.Errorf("Float64Env got %f, want %f (default on parse error)", val, 1.0)
	}
}

func TestReadSecretFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte(" secret value \r\n"), 0o600); err != nil {
		t.Fatalf("failed to write secret file: %v", err)
	}

	got, err := ReadSecretFile(path, "TEST_SECRET_FILE")
	if err != nil {
		t.Fatalf("ReadSecretFile() failed: %v", err)
	}
	if got != " secret value " {
		t.Errorf("ReadSecretFile() = %q, want %q", got, " secret value ")
	}

	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := ReadSecretFile(missing, "TEST_SECRET_FILE"); err == nil || !strings.Contains(err.Error(), "TEST_SECRET_FILE") {
		t.Errorf("ReadSecretFile(missing) error = %v, want error naming TEST_SECRET_FILE", err)
	}
}
