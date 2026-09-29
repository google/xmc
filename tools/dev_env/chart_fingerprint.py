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

"""Computes a deterministic content fingerprint of a local Helm chart.

Terraform's `helm_release` only detects changes to the chart path, version,
and values, so template-only edits (including deleted templates) never
trigger a Helm upgrade. Feeding this fingerprint into the release inputs
makes every chart content change produce a real upgrade, which lets Helm
prune objects removed from the chart.
"""

import hashlib
import sys
from pathlib import Path


def compute_chart_content_hash(chart_dir: Path) -> str:
    """Returns a SHA-256 hex digest over all regular files in a chart.

    The digest covers each file's POSIX path relative to `chart_dir` and its
    bytes, visited in sorted path order, so it is independent of filesystem
    iteration order and changes on any file addition, edit, rename, or removal.

    Args:
        chart_dir: Root directory of the Helm chart (e.g. `deploy/helm`).

    Returns:
        Lowercase hexadecimal SHA-256 digest.

    Raises:
        FileNotFoundError: If `chart_dir` is not a directory.
    """
    if not chart_dir.is_dir():
        raise FileNotFoundError(f"Helm chart directory not found: {chart_dir}")
    digest = hashlib.sha256()
    files = sorted(p for p in chart_dir.rglob("*") if p.is_file())
    for path in files:
        digest.update(path.relative_to(chart_dir).as_posix().encode() + b"\0")
        digest.update(hashlib.sha256(path.read_bytes()).digest())
    return digest.hexdigest()


def main(argv: list[str]) -> int:
    """Prints the content hash of the chart at `argv[1]` (default `deploy/helm`).

    Used by manual deployments:
    `--vars helm_chart_content_hash=$(python3 tools/dev_env/chart_fingerprint.py deploy/helm)`.

    Args:
        argv: Command-line arguments, including the program name.

    Returns:
        Process exit code.
    """
    chart_dir = Path(argv[1]) if len(argv) > 1 else Path("deploy/helm")
    print(compute_chart_content_hash(chart_dir))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
