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

"""Tests for check_image_binaries."""

import argparse
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import HandledError  # noqa: E402
from tools.lint import check_image_binaries as cli  # noqa: E402

_SERVICE_BUILD = """
load("//:oci.bzl", "oci_image_for_service")

go_binary(name = "svc", embed = [":lib"])

go_binary(name = "svc_dev", embed = [":lib"], gotags = ["mockauth"])

go_binary(name = "svc_tagged", embed = [":lib"], gotags = ["mockauth"])

go_binary(name = "svc_select", gotags = select({"//conditions:default": []}))

go_binary(name = "svc_unhashable", gotags = {["mockauth"]: 1})

go_binary(name = "svc_kwargs", embed = [":lib"], **DEV_KWARGS)

go_binary(name = "svc_string_tags", gotags = "mockauth")

go_binary(name = "svc_nested_tags", gotags = [["mockauth"]])

oci_image_for_service(name = "{image}", binary = "{binary}")
"""


def _build_files(binary: str, image: str = "svc_image") -> dict[str, str]:
    return {
        "cmd/svc/BUILD.bazel": _SERVICE_BUILD.replace("{image}", image).replace("{binary}", binary)
    }


def _violations(files: dict[str, str]) -> list[dict[str, str]]:
    return cli.find_violations(cli.collect_targets(files))


class FindViolationsTest(unittest.TestCase):
    def test_untagged_binary_passes(self) -> None:
        self.assertEqual(_violations(_build_files(":svc")), [])

    def test_resolve_label_forms(self) -> None:
        cases = {
            ":svc": "//cmd/svc:svc",
            "svc": "//cmd/svc:svc",
            "//cmd/svc": "//cmd/svc:svc",
            "//cmd/other:bin": "//cmd/other:bin",
            "@external//:svc": "@external//:svc",
        }
        for label, want in cases.items():
            with self.subTest(label=label):
                self.assertEqual(cli.resolve_label("cmd/svc", label), want)

    def test_package_relative_shorthand_labels(self) -> None:
        self.assertEqual(_violations(_build_files("svc")), [])
        self.assertEqual(
            _violations(_build_files("svc_dev")),
            [
                {
                    "image": "//cmd/svc:svc_image",
                    "binary": "//cmd/svc:svc_dev",
                    "reason": "binary name ends with _dev",
                }
            ],
        )

    def test_absolute_and_shorthand_labels_resolve(self) -> None:
        self.assertEqual(_violations(_build_files("//cmd/svc:svc")), [])
        files = {
            "cmd/svc/BUILD.bazel": 'go_binary(name = "svc")\noci_image_for_service(name = "i", binary = "//cmd/svc")\n'
        }
        self.assertEqual(_violations(files), [])

    def test_rejects_development_binaries(self) -> None:
        cases = {
            ":svc_dev": "binary name ends with _dev",
            ":svc_tagged": "binary is built with development gotags ['mockauth']",
            ":svc_select": "binary gotags are not a literal list: select({'//conditions:default': []})",
            ":svc_unhashable": "binary gotags are not a literal list: {['mockauth']: 1}",
            ":svc_kwargs": "binary gotags are not a literal list: **DEV_KWARGS",
            ":svc_string_tags": "binary gotags are not a literal list of strings: 'mockauth'",
            ":svc_nested_tags": "binary gotags are not a literal list of strings: [['mockauth']]",
            ":missing": "binary is not a go_binary declared in this repository",
            "@external//:svc": "binary is not a go_binary declared in this repository",
        }
        for binary, reason in cases.items():
            with self.subTest(binary=binary):
                self.assertEqual(
                    _violations(_build_files(binary)),
                    [
                        {
                            "image": "//cmd/svc:svc_image",
                            "binary": cli.resolve_label("cmd/svc", binary),
                            "reason": reason,
                        }
                    ],
                )

    def test_rejects_non_literal_or_missing_binary(self) -> None:
        files = {
            "BUILD.bazel": (
                'oci_image_for_service(name = "i", binary = BIN)\n'
                'oci_image_for_service(name = "j")\n'
                'oci_image_for_service(name = "k", binary = ":svc", **IMAGE_KWARGS)\n'
                'go_binary(name = "svc")\n'
            )
        }
        reason = "binary is not a go_binary declared in this repository"
        self.assertEqual(
            _violations(files),
            [
                {"image": "//:i", "binary": "<non-literal: BIN>", "reason": reason},
                {"image": "//:j", "binary": "<invalid: None>", "reason": reason},
                {"image": "//:k", "binary": "<non-literal: **IMAGE_KWARGS>", "reason": reason},
            ],
        )

    def test_ignores_image_call_text_in_comments_and_strings(self) -> None:
        source = (
            '# oci_image_for_service(name = "old", binary = ":svc_dev")\n'
            'DOC = "oci_image_for_service(binary = \\":svc_dev\\")"\n'
            + _build_files(":svc")["cmd/svc/BUILD.bazel"]
        )
        self.assertEqual(_violations({"cmd/svc/BUILD.bazel": source}), [])

    def test_rejects_image_calls_it_cannot_parse(self) -> None:
        cases = {
            "comprehension": '[oci_image_for_service(name = n, binary = ":svc_dev") for n in ["a"]]\n',
            "non-literal name": 'oci_image_for_service(name = NAME, binary = ":svc_dev")\n',
            "aliased load": 'load("//:oci.bzl", img = "oci_image_for_service")\nimg(name = "i", binary = ":svc_dev")\n',
        }
        for name, source in cases.items():
            with self.subTest(name):
                self.assertEqual(
                    _violations({"cmd/svc/BUILD.bazel": source}),
                    [
                        {
                            "build_file": "cmd/svc/BUILD.bazel",
                            "reason": "oci_image_for_service is called in a form this check cannot parse",
                        }
                    ],
                )

    def test_ignores_non_call_statements_and_unnamed_calls(self) -> None:
        files = {
            "BUILD.bazel": 'X = 1\npkg.rule(name = "a")\ngo_binary(**kwargs)\ngo_binary(name = NAME)\n'
        }
        self.assertEqual(cli.collect_targets(files), cli.Targets())


class CheckImageBinariesTest(unittest.TestCase):
    def _git_repo(self, files: dict[str, str]) -> Path:
        root = Path(self.enterContext(tempfile.TemporaryDirectory()))
        for rel, content in files.items():
            (root / rel).parent.mkdir(parents=True, exist_ok=True)
            (root / rel).write_text(content, encoding="utf-8")
        subprocess.run(["git", "init", "-q"], cwd=root, check=True)
        subprocess.run(["git", "add", "-A"], cwd=root, check=True)
        return root

    def test_passes_on_clean_repo(self) -> None:
        root = self._git_repo({**_build_files(":svc"), "README.md": "x"})
        self.assertEqual(
            cli.check_image_binaries(argparse.Namespace(repo_root=str(root))), {"images_checked": 1}
        )

    def test_raises_when_no_images_found(self) -> None:
        root = self._git_repo({"cmd/svc/BUILD.bazel": 'go_binary(name = "svc")\n'})
        with self.assertRaisesRegex(HandledError, "no oci_image_for_service targets found"):
            cli.check_image_binaries(argparse.Namespace(repo_root=str(root)))

    def test_raises_handled_error_outside_git_repository(self) -> None:
        root = Path(self.enterContext(tempfile.TemporaryDirectory()))
        cases = {"not a git repository": root, "missing directory": root / "missing"}
        for name, repo_root in cases.items():
            with (
                self.subTest(name),
                mock.patch.dict(os.environ, {"GIT_CEILING_DIRECTORIES": str(root.parent)}),
            ):
                with self.assertRaises(HandledError) as ctx:
                    cli.check_image_binaries(argparse.Namespace(repo_root=str(repo_root)))
                self.assertIn("git ls-files failed", ctx.exception.message)
                self.assertTrue((ctx.exception.details or {}).get("stderr"))

    def test_raises_on_violation(self) -> None:
        root = self._git_repo(_build_files(":svc_dev"))
        with self.assertRaises(HandledError) as ctx:
            cli.check_image_binaries(argparse.Namespace(repo_root=str(root)))
        self.assertEqual(
            ctx.exception.details,
            {
                "violations": [
                    {
                        "image": "//cmd/svc:svc_image",
                        "binary": "//cmd/svc:svc_dev",
                        "reason": "binary name ends with _dev",
                    }
                ]
            },
        )


if __name__ == "__main__":
    unittest.main()
