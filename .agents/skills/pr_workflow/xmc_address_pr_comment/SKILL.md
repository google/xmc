---
name: XMC Address PR Comment
description: Automates addressing PR comments (fetching context, running tests, measuring coverage, and GKE redeployment).
---

# PR Comment-Driven Development Workflow

Automates addressing GitHub Pull Request comments while enforcing comment integrity, programmatic enumeration, and 1-to-1 thread resolution verification.

## 1. Automated Review Checklist Generation
Generate a checklist of all unresolved comments directly from GitHub API:
```bash
./tools/generate_review_checklist.py <owner> <repo> <pr_number> [-o pr_review_checklist.md]
```

## 2. Context & Details Fetching
Retrieve comment details and diff context using:
```bash
./tools/fetch_pr_comment.py "<github_comment_url>"
```

## 3. Target Dependency Mapping
Run Bazel query to identify affected binary targets and GKE deployments. Refer to target lookup table in `references/target_mapping.md`.

## 4. Test Verification & Coverage Check
Run unit tests and check test coverage:
```bash
bazel test //pkg/...
bazel coverage --combined_report=lcov //pkg/...
```

## 5. Local Commit & GitHub Reply
Build binaries, stage changes, commit, and post GitHub comment reply:
```bash
bazel build //cmd/...
git commit -m "<type>(<scope>): address review comment"
./tools/reply_pr_comment.py "<github_comment_url>" "code changed"
```

## 6. GKE Redeployment & Rollout
Build/push container images and restart GKE deployments:
```bash
bazel run //cmd/<target>:<target>_image_push
kubectl rollout restart deployment/<deployment_name> -n xmc
```

## 7. 1-to-1 Thread Resolution Verification
Verify 100% of review thread URLs received dedicated replies before completing:
```bash
./tools/verify_pr_comment_resolution.py <owner> <repo> <pull_number> <path_to_checklist_file>
```
