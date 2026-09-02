#!/usr/bin/env python3
# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Unit tests for check_go_determinism.py."""

from __future__ import annotations

import tempfile
import unittest
from pathlib import Path

from tools.check_go_determinism import check_file, find_test_files, is_excluded


class CheckGoDeterminismTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temp_dir = tempfile.TemporaryDirectory()
        self.test_dir = Path(self.temp_dir.name)

    def tearDown(self) -> None:
        self.temp_dir.cleanup()

    def test_clean_file_passes(self) -> None:
        file_path = self.test_dir / "clean_test.go"
        file_path.write_text(
            """package main

import (
	"testing"
	"time"
)

func TestSomething(t *testing.T) {
	t.Setenv("FOO", "BAR")
	time.Sleep(10 * time.Millisecond)
}
""",
            encoding="utf-8",
        )
        violations = check_file(file_path)
        self.assertEqual(violations, [])

    def test_os_setenv_flags_violation(self) -> None:
        file_path = self.test_dir / "dirty_env_test.go"
        file_path.write_text(
            """package main

import (
	"os"
	"testing"
)

func TestDirty(t *testing.T) {
	os.Setenv("POLLUTE", "TRUE")
}
""",
            encoding="utf-8",
        )
        violations = check_file(file_path)
        self.assertEqual(len(violations), 1)
        self.assertIn("os.Setenv used in test", violations[0])
        self.assertIn("Use t.Setenv", violations[0])

    def test_long_sleep_flags_violation(self) -> None:
        file_path = self.test_dir / "slow_sleep_test.go"
        file_path.write_text(
            """package main

import (
	"testing"
	"time"
)

func TestSlow(t *testing.T) {
	time.Sleep(15 * time.Second)
}
""",
            encoding="utf-8",
        )
        violations = check_file(file_path)
        self.assertEqual(len(violations), 1)
        self.assertIn("arbitrary second-level time.Sleep in test", violations[0])

    def test_nolint_preceding_line_bypasses(self) -> None:
        file_path = self.test_dir / "allowed_sleep_test.go"
        file_path.write_text(
            """package main

import (
	"testing"
	"time"
)

func TestAllowed(t *testing.T) {
	// nolint:sleep // Requires Spanner read staleness
	time.Sleep(15 * time.Second)
}
""",
            encoding="utf-8",
        )
        violations = check_file(file_path)
        self.assertEqual(violations, [])

    def test_nolint_inline_bypasses(self) -> None:
        file_path = self.test_dir / "inline_allowed_test.go"
        file_path.write_text(
            """package main

import (
	"testing"
	"time"
)

func TestInlineAllowed(t *testing.T) {
	time.Sleep(5 * time.Second) // nolint:sleep
}
""",
            encoding="utf-8",
        )
        violations = check_file(file_path)
        self.assertEqual(violations, [])

    def test_block_comment_bypasses(self) -> None:
        file_path = self.test_dir / "block_comment_test.go"
        file_path.write_text(
            """package main

import "testing"

func TestBlock(t *testing.T) {
	/*
	os.Setenv("FOO", "BAR")
	time.Sleep(15 * time.Second)
	*/
}
""",
            encoding="utf-8",
        )
        violations = check_file(file_path)
        self.assertEqual(violations, [])

    def test_multiline_sleep_flags_violation(self) -> None:
        file_path = self.test_dir / "multiline_sleep_test.go"
        file_path.write_text(
            """package main

import (
	"testing"
	"time"
)

func TestMulti(t *testing.T) {
	time.Sleep(
		15 * time.Second,
	)
}
""",
            encoding="utf-8",
        )
        violations = check_file(file_path)
        self.assertEqual(len(violations), 1)
        self.assertIn("arbitrary second-level time.Sleep in test", violations[0])
        self.assertIn(":9:", violations[0])

    def test_string_literal_bypasses(self) -> None:
        file_path = self.test_dir / "string_literal_test.go"
        file_path.write_text(
            """package main

import "testing"

func TestString(t *testing.T) {
	msg := "time.Sleep(15 * time.Second) os.Setenv(a, b)"
	t.Log(msg)
}
""",
            encoding="utf-8",
        )
        violations = check_file(file_path)
        self.assertEqual(violations, [])

    def test_rune_literal_does_not_mask_subsequent_code(self) -> None:
        file_path = self.test_dir / "rune_test.go"
        file_path.write_text(
            """package main

import (
	"os"
	"testing"
)

func TestRune(t *testing.T) {
	q := '"'
	os.Setenv("FOO", "BAR")
}
""",
            encoding="utf-8",
        )
        violations = check_file(file_path)
        self.assertEqual(len(violations), 1)
        self.assertIn("os.Setenv used in test", violations[0])

    def test_operand_ordering_sleep_flags_violation(self) -> None:
        file_path = self.test_dir / "reverse_operand_test.go"
        file_path.write_text(
            """package main

import (
	"testing"
	"time"
)

func TestReverse(t *testing.T) {
	time.Sleep(time.Second * 5)
}
""",
            encoding="utf-8",
        )
        violations = check_file(file_path)
        self.assertEqual(len(violations), 1)
        self.assertIn("arbitrary second-level time.Sleep in test", violations[0])

    def test_multiline_inline_nolint_bypasses(self) -> None:
        file_path = self.test_dir / "multiline_inline_test.go"
        file_path.write_text(
            """package main

import (
	"testing"
	"time"
)

func TestMultilineInline(t *testing.T) {
	time.Sleep(
		15 * time.Second, // nolint:sleep
	)
}
""",
            encoding="utf-8",
        )
        violations = check_file(file_path)
        self.assertEqual(violations, [])

    def test_struct_member_setenv_not_flagged(self) -> None:
        file_path = self.test_dir / "struct_member_test.go"
        file_path.write_text(
            """package main

import "testing"

type MockOS struct{}

func (m *MockOS) Setenv(k, v string) {}

func TestStruct(t *testing.T) {
	mock := &MockOS{}
	mock.Setenv("K", "V")
}
""",
            encoding="utf-8",
        )
        violations = check_file(file_path)
        self.assertEqual(violations, [])

    def test_unclosed_block_comment_at_eof(self) -> None:
        file_path = self.test_dir / "unclosed_test.go"
        file_path.write_text(
            """package main

/* unclosed comment at EOF
""",
            encoding="utf-8",
        )
        violations = check_file(file_path)
        self.assertEqual(violations, [])

    def test_directory_argument_discovers_nested_tests(self) -> None:
        sub_dir = self.test_dir / "nested"
        sub_dir.mkdir()
        test_file = sub_dir / "nested_test.go"
        test_file.write_text('package main\n\nimport "testing"\n', encoding="utf-8")
        found = find_test_files(self.test_dir)
        self.assertIn(test_file, found)

    def test_excluded_directories(self) -> None:
        self.assertTrue(is_excluded(Path("vendor/pkg/foo_test.go")))
        self.assertTrue(is_excluded(Path("node_modules/foo/bar_test.go")))
        self.assertTrue(is_excluded(Path("bazel-bin/cmd/foo_test.go")))
        self.assertFalse(is_excluded(Path("cmd/health_service/server_test.go")))


if __name__ == "__main__":
    unittest.main()
