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
	"testing"
	"time"
)

func TestStringVar(t *testing.T) {
	t.Setenv("TEST_STRING_VAR", "hello")

	var val string
	StringVar(&val, "TEST_STRING_VAR", "default")
	if val != "hello" {
		t.Errorf("StringVar got %q, want %q", val, "hello")
	}

	StringVar(&val, "NON_EXISTENT_VAR", "default")
	if val != "default" {
		t.Errorf("StringVar got %q, want %q", val, "default")
	}
}

func TestIntVar(t *testing.T) {
	t.Setenv("TEST_INT_VAR", "42")

	var val int
	IntVar(&val, "TEST_INT_VAR", 10)
	if val != 42 {
		t.Errorf("IntVar got %d, want %d", val, 42)
	}

	t.Setenv("TEST_INT_VAR", "invalid")
	IntVar(&val, "TEST_INT_VAR", 10)
	if val != 10 {
		t.Errorf("IntVar got %d, want %d (default on parse error)", val, 10)
	}

	IntVar(&val, "NON_EXISTENT_VAR", 10)
	if val != 10 {
		t.Errorf("IntVar got %d, want %d", val, 10)
	}
}

func TestInt64Var(t *testing.T) {
	t.Setenv("TEST_INT64_VAR", "123456789012345")

	var val int64
	Int64Var(&val, "TEST_INT64_VAR", 10)
	if val != 123456789012345 {
		t.Errorf("Int64Var got %d, want %d", val, 123456789012345)
	}

	t.Setenv("TEST_INT64_VAR", "invalid")
	Int64Var(&val, "TEST_INT64_VAR", 10)
	if val != 10 {
		t.Errorf("Int64Var got %d, want %d (default on parse error)", val, 10)
	}
}

func TestBoolVar(t *testing.T) {
	t.Setenv("TEST_BOOL_VAR", "true")

	var val bool
	BoolVar(&val, "TEST_BOOL_VAR", false)
	if !val {
		t.Errorf("BoolVar got %t, want true", val)
	}

	t.Setenv("TEST_BOOL_VAR", "invalid")
	BoolVar(&val, "TEST_BOOL_VAR", false)
	if val {
		t.Errorf("BoolVar got %t, want false (default on parse error)", val)
	}

	BoolVar(&val, "NON_EXISTENT_VAR", true)
	if !val {
		t.Errorf("BoolVar got %t, want true", val)
	}
}

func TestDurationVar(t *testing.T) {
	t.Setenv("TEST_DURATION_VAR", "5m")

	var val time.Duration
	DurationVar(&val, "TEST_DURATION_VAR", 10*time.Second)
	if val != 5*time.Minute {
		t.Errorf("DurationVar got %s, want %s", val, 5*time.Minute)
	}

	t.Setenv("TEST_DURATION_VAR", "invalid")
	DurationVar(&val, "TEST_DURATION_VAR", 10*time.Second)
	if val != 10*time.Second {
		t.Errorf("DurationVar got %s, want %s (default on parse error)", val, 10*time.Second)
	}
}

func TestFloat64Var(t *testing.T) {
	t.Setenv("TEST_FLOAT_VAR", "3.14")

	var val float64
	Float64Var(&val, "TEST_FLOAT_VAR", 1.0)
	if val != 3.14 {
		t.Errorf("Float64Var got %f, want %f", val, 3.14)
	}

	t.Setenv("TEST_FLOAT_VAR", "invalid")
	Float64Var(&val, "TEST_FLOAT_VAR", 1.0)
	if val != 1.0 {
		t.Errorf("Float64Var got %f, want %f (default on parse error)", val, 1.0)
	}
}
