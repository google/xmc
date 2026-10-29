#!/usr/bin/env python3
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

"""Test entrypoint asserting whether a genquery result lists development-only go_binary targets.

Used by the `dev_binary_guard_test` macro in //:oci.bzl. The genquery output contains one label
per line for every go_binary in the image binary's transitive deps that is a `*_dev` binary or
is built with development gotags (for example `mockauth`). Published images must never contain
one, so the test fails unless the result matches `--expect-dev-binary`.
"""

import argparse
import sys
from pathlib import Path


def main() -> int:
    """Returns 0 when the query result matches the expectation, 1 otherwise."""
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("query_result", type=Path, help="genquery output file.")
    parser.add_argument(
        "--expect-dev-binary",
        action="store_true",
        help="Expect at least one development-only binary (negative regression check).",
    )
    args = parser.parse_args()
    dev_binaries = args.query_result.read_text(encoding="utf-8").split()
    if bool(dev_binaries) == args.expect_dev_binary:
        return 0
    if dev_binaries:
        print(
            "Image packages development-only go_binary targets:",
            *dev_binaries,
            sep="\n  ",
            file=sys.stderr,
        )
        print("Point oci_image_for_service at the untagged go_binary.", file=sys.stderr)
    else:
        print(
            "Expected a development-only go_binary in the query result, found none.",
            file=sys.stderr,
        )
    return 1


if __name__ == "__main__":
    sys.exit(main())
