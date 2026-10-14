# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Tests for check_legacy_imports."""

import argparse
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import HandledError  # noqa: E402
from tools.lint import check_legacy_imports as cli  # noqa: E402

_GO_SOURCE = """package foo

import "fmt"

import (
\t"context"
\tstatus "task/go/status"
\t_ "golang/uuid/uuid"
\t. "go/flag"
\t"go/ast"
\t"xmanager_cloud/internal/real"
\t"xmanager_cloud/storage/storage" // legacy layout
\t"github.com/google/go-cmp/cmp"
)

var s = "golang/not/an/import"
"""


class ExtractGoImportsTest(unittest.TestCase):
    def test_extracts_single_and_block_imports_with_aliases(self):
        self.assertEqual(
            cli.extract_go_imports(_GO_SOURCE),
            [
                "fmt",
                "context",
                "task/go/status",
                "golang/uuid/uuid",
                "go/flag",
                "go/ast",
                "xmanager_cloud/internal/real",
                "xmanager_cloud/storage/storage",
                "github.com/google/go-cmp/cmp",
            ],
        )

    def test_ignores_string_literals_outside_imports(self):
        self.assertNotIn("golang/not/an/import", cli.extract_go_imports(_GO_SOURCE))


class ClassificationTest(unittest.TestCase):
    def test_is_legacy_root_import(self):
        cases = {
            "task/go/status": True,
            "golang/cmp/cmp": True,
            "go/flag": True,
            "go/google": True,
            "go/ast": False,
            "net/rpc": False,
            "encoding/json": False,
            "github.com/google/uuid": False,
            "xmanager_cloud/internal/logging": False,
        }
        for path, want in cases.items():
            with self.subTest(path=path):
                self.assertEqual(cli.is_legacy_root_import(path), want)

    def test_is_unresolved_module_import(self):
        tracked = frozenset({"internal", "internal/real"})
        self.assertFalse(cli.is_unresolved_module_import("xmanager_cloud/internal/real", tracked))
        self.assertTrue(cli.is_unresolved_module_import("xmanager_cloud/storage/storage", tracked))
        self.assertFalse(cli.is_unresolved_module_import("github.com/x/y", tracked))

    def test_tracked_directories(self):
        self.assertEqual(
            cli.tracked_directories(["a/b/c.go", "a/d.go", "top.go"]),
            frozenset({"a", "a/b"}),
        )


class CollectMetricsTest(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.root = Path(self._tmp.name)
        self.files = {
            "internal/real/a.go": _GO_SOURCE,
            "pkg/BUILD.bazel": (
                'compat_go_library(name = "a")\ncompat_go_test(name = "b")\n'
                '    import_prefix = "third_party/x/proto",\n'
            ),
            "BUILD.bazel": "# gazelle:resolve go a //b\n# gazelle:prefix xmanager_cloud\n",
            "compat.bzl": "def compat_go_library(name):\n    pass\n",
            "import.bzl": "_r = repository_rule(implementation = _impl)\n",
            "api/x.proto": 'import "third_party/xmanager_cloud/x.proto";\n',
            "deploy/helm/templates/d.yaml": (
                '- "--logtostderr"\n- "--streamz_servers="\n- "--port"\n'
                '- "-envelope_enabled=false"\n- "-ess_tls_cert_file="\n'
            ),
            "deploy/ctk_v2/m/main.tf": '"-alsologtostderr",\n"-log_keyword=x",\n',
            "deploy/helm/templates/e.yaml": "args: ['--notracelog']\ncommand: run -vmodule=x\n",
            "cmd/ui/app/a.ts": "import { X } from 'javascript/closure/format/format';\n",
            "sdks/p.py": "from third_party.xmanager_cloud.x import y\n",
            "third_party/grpc_gateway.BUILD": "",
            # Self-excluded files must not be counted.
            "tools/lint/check_legacy_imports_test.py": "from third_party.xmanager_cloud import x\n",
        }
        for rel, content in self.files.items():
            path = self.root / rel
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content)

    def tearDown(self):
        self._tmp.cleanup()

    def test_collects_all_metrics(self):
        metrics = cli.collect_metrics(self.root, list(self.files))
        self.assertEqual(
            metrics,
            {
                "compat_go_targets": 2,
                "gazelle_resolve_directives": 1,
                "go_legacy_root_imports": 3,
                "go_unresolved_module_imports": 1,
                "helm_google3_flags": 6,
                "proto_third_party_paths": 2,
                "python_legacy_paths": 1,
                "third_party_files": 1,
                "ts_legacy_module_paths": 1,
            },
        )

    def test_detects_m_opts_and_shell_virtual_imports(self):
        (self.root / "api/BUILD.bazel").write_text(
            '    "Mxmanager_cloud/x.proto=xmanager_cloud/api/protos/x",\n'
        )
        (self.root / "install_venv.sh").write_text(
            'cp -R "${B}/_virtual_imports/t/third_party/." "${S}/"\n'
        )
        metrics = cli.collect_metrics(self.root, ["api/BUILD.bazel", "install_venv.sh"])
        self.assertEqual(metrics["proto_third_party_paths"], 1)
        self.assertEqual(metrics["python_legacy_paths"], 1)

    def test_missing_tracked_file_is_ignored(self):
        metrics = cli.collect_metrics(self.root, ["deleted/x.go"])
        self.assertEqual(sum(metrics.values()), 0)

    def test_untracked_directory_does_not_resolve_import(self):
        # A leftover directory on disk (e.g. after `git rm`) must not hide an
        # unresolved import; only tracked files define package directories.
        (self.root / "removed_pkg").mkdir()
        (self.root / "use.go").write_text('package a\n\nimport "xmanager_cloud/removed_pkg"\n')
        metrics = cli.collect_metrics(self.root, ["use.go"])
        self.assertEqual(metrics["go_unresolved_module_imports"], 1)


class BaselineTest(unittest.TestCase):
    def test_compare_to_baseline(self):
        diff = cli.compare_to_baseline({"a": 3, "b": 1, "c": 2, "new": 1}, {"a": 2, "b": 5, "c": 2})
        self.assertEqual(
            diff["regressed"],
            {"a": {"baseline": 2, "current": 3}, "new": {"baseline": 0, "current": 1}},
        )
        self.assertEqual(diff["improved"], {"b": {"baseline": 5, "current": 1}})

    def test_write_baseline_refuses_increase(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "b.json"
            with self.assertRaises(HandledError):
                cli.write_baseline(path, {"a": 2}, {"a": 1})
            self.assertFalse(path.exists())

    def test_write_baseline_allows_decrease_initial_creation_and_new_keys(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "b.json"
            cli.write_baseline(path, {"a": 2}, {})
            cli.write_baseline(path, {"a": 1, "b": 5}, {"a": 2})
            self.assertEqual(json.loads(path.read_text()), {"a": 1, "b": 5})

    def test_compare_flags_removed_metric_as_stale(self):
        diff = cli.compare_to_baseline({"a": 1}, {"a": 1, "gone": 3})
        self.assertEqual(diff["improved"], {"gone": {"baseline": 3, "current": 0}})
        self.assertEqual(diff["regressed"], {})

    def test_load_baseline_missing_raises(self):
        with tempfile.TemporaryDirectory() as tmp:
            with self.assertRaises(HandledError):
                cli.load_baseline(Path(tmp) / "missing.json")


class CheckLegacyImportsEndToEndTest(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.root = Path(self._tmp.name)
        subprocess.run(["git", "init", "-q"], cwd=self.root, check=True)
        self._write_and_track("a.go", 'package a\n\nimport "golang/cmp/cmp"\n')
        self.baseline = self.root / "baseline.json"

    def tearDown(self):
        self._tmp.cleanup()

    def _write_and_track(self, rel, content):
        (self.root / rel).write_text(content)
        subprocess.run(["git", "add", rel], cwd=self.root, check=True)

    def _run(self, update=False, baseline="default"):
        args = argparse.Namespace(
            repo_root=str(self.root),
            baseline=str(self.baseline) if baseline == "default" else baseline,
            update_baseline=update,
        )
        return cli.check_legacy_imports(args)

    def test_update_then_check_passes(self):
        self._run(update=True)
        result = self._run()
        self.assertEqual(result["metrics"]["go_legacy_root_imports"], 1)
        self.assertEqual(result["stale_baseline"], {})

    def test_regression_fails(self):
        self._run(update=True)
        self._write_and_track("b.go", 'package a\n\nimport "task/go/status"\n')
        with self.assertRaisesRegex(HandledError, "increased"):
            self._run()

    def test_stale_baseline_passes_and_is_reported(self):
        self._run(update=True)
        self._write_and_track("a.go", "package a\n")
        result = self._run()
        self.assertEqual(
            result["stale_baseline"],
            {"go_legacy_root_imports": {"baseline": 1, "current": 0}},
        )

    def test_default_baseline_resolves_under_repo_root(self):
        (self.root / cli.BASELINE_RELPATH).parent.mkdir(parents=True)
        self._run(update=True, baseline=None)
        self.assertTrue((self.root / cli.BASELINE_RELPATH).is_file())


if __name__ == "__main__":
    unittest.main()
