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

"""Unit tests for commit classification and conventional commit parser."""

import sys
import unittest
from pathlib import Path

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

try:
    from tools.release.commit_classifier import (
        _is_dev_velocity_files,
        _is_infra_or_auth_files,
        classify_commit,
        parse_conventional_commit,
    )
    from tools.release.models import CommitInfo
except ImportError:
    from commit_classifier import (  # type: ignore[no-redef]
        _is_dev_velocity_files,
        _is_infra_or_auth_files,
        classify_commit,
        parse_conventional_commit,
    )
    from models import CommitInfo  # type: ignore[no-redef]


class CommitClassifierTest(unittest.TestCase):
    """Tests for commit classification into release categories."""

    def test_parse_conventional_commit(self) -> None:
        c_type, scope, desc, pr = parse_conventional_commit("feat(ui): add dashboard (#123)")
        self.assertEqual(c_type, "feat")
        self.assertEqual(scope, "ui")
        self.assertEqual(desc, "add dashboard")
        self.assertEqual(pr, 123)

        c_type, scope, desc, pr = parse_conventional_commit("fix: resolve crash")
        self.assertEqual(c_type, "fix")
        self.assertEqual(scope, "")
        self.assertEqual(desc, "resolve crash")
        self.assertIsNone(pr)

        c_type, scope, desc, pr = parse_conventional_commit(
            "security-fix(auth): patch vulnerability (#124)"
        )
        self.assertEqual(c_type, "security-fix")
        self.assertEqual(scope, "auth")
        self.assertEqual(desc, "patch vulnerability")
        self.assertEqual(pr, 124)

        c_type, scope, desc, pr = parse_conventional_commit("v2(api): new endpoint")
        self.assertEqual(c_type, "v2")
        self.assertEqual(scope, "api")
        self.assertEqual(desc, "new endpoint")
        self.assertIsNone(pr)

        c_type, scope, desc, pr = parse_conventional_commit("Non-conventional raw commit summary")
        self.assertEqual(c_type, "other")
        self.assertEqual(scope, "")
        self.assertEqual(desc, "Non-conventional raw commit summary")

    def test_classify_commit_developer_velocity(self) -> None:
        commit = CommitInfo(
            commit_hash="2222",
            short_hash="2222",
            subject="chore(deps): bump go-cmp (#400)",
            author="Dev",
            date="2026-08-21",
        )
        classify_commit(commit)
        self.assertEqual(commit.category, "developer_velocity")

        commit_dev_files = CommitInfo(
            commit_hash="2223",
            short_hash="2223",
            subject="update tooling and agent rules",
            author="Dev",
            date="2026-08-21",
            files_changed=[
                ".github/workflows/ci.yaml",
                ".agents/rules/golang.md",
                "tools/release/models.py",
                "docs/README.md",
                "pkg/server/server_test.go",
                "BUILD.bazel",
            ],
        )
        classify_commit(commit_dev_files)
        self.assertEqual(commit_dev_files.category, "developer_velocity")

    def test_classify_commit_infra_and_auth(self) -> None:
        commit_auth = CommitInfo(
            commit_hash="3331",
            short_hash="3331",
            subject="refactor auth token verification (#401)",
            author="Dev",
            date="2026-08-21",
            files_changed=["pkg/auth/token.go"],
        )
        classify_commit(commit_auth)
        self.assertEqual(commit_auth.category, "operational_and_infrastructure")

        commit_deploy = CommitInfo(
            commit_hash="3332",
            short_hash="3332",
            subject="update spanner migrations (#402)",
            author="Dev",
            date="2026-08-21",
            files_changed=["pkg/spanner/schema.sql", "deploy/helm/values.yaml"],
        )
        classify_commit(commit_deploy)
        self.assertEqual(commit_deploy.category, "operational_and_infrastructure")

    def test_classify_commit_public_feature_and_fix(self) -> None:
        commit_feat = CommitInfo(
            commit_hash="4444",
            short_hash="4444",
            subject="feat(ui): add search filter (#410)",
            author="Dev",
            date="2026-08-21",
            files_changed=["ui/app/search.ts"],
        )
        classify_commit(commit_feat)
        self.assertEqual(commit_feat.category, "user_facing_features")

        commit_fix = CommitInfo(
            commit_hash="5555",
            short_hash="5555",
            subject="fix(ui): handle null queue (#411)",
            author="Dev",
            date="2026-08-21",
            files_changed=["ui/app/search.ts"],
        )
        classify_commit(commit_fix)
        self.assertEqual(commit_fix.category, "user_facing_fixes")

        commit_fallback = CommitInfo(
            commit_hash="6666",
            short_hash="6666",
            subject="misc update without recognized type",
            author="Dev",
            date="2026-08-21",
        )
        classify_commit(commit_fallback)
        self.assertEqual(commit_fallback.category, "developer_velocity")

    def test_classify_commit_pre_parsed(self) -> None:
        commit = CommitInfo(
            commit_hash="7777",
            short_hash="7777",
            subject="manual subject",
            author="Dev",
            date="2026-08-21",
            commit_type="feat",
            scope="ui",
            description="pre-parsed feature",
            pr_number=500,
        )
        classify_commit(commit)
        self.assertEqual(commit.category, "user_facing_features")
        self.assertEqual(commit.description, "pre-parsed feature")
        self.assertEqual(commit.pr_number, 500)

    def test_helper_predicates(self) -> None:
        self.assertFalse(_is_dev_velocity_files([]))
        self.assertTrue(_is_dev_velocity_files(["scripts/deploy.py", "docs/guide.md"]))
        self.assertTrue(_is_dev_velocity_files(["tests/integration/test_pipeline.py"]))
        self.assertTrue(_is_dev_velocity_files(["pkg/server/go.mod", "pkg/server/BUILD.bazel"]))
        self.assertFalse(_is_dev_velocity_files(["pkg/server/handler.go"]))
        self.assertTrue(_is_infra_or_auth_files(["config/default.json"]))
        self.assertTrue(_is_infra_or_auth_files(["pkg/auth/token.go", "tools/release/BUILD.bazel"]))
        self.assertFalse(_is_infra_or_auth_files(["pkg/auth/token.go", "pkg/server/handler.go"]))
        self.assertFalse(_is_infra_or_auth_files(["ui/index.html"]))


if __name__ == "__main__":
    unittest.main()
