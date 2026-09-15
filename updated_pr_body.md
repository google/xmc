## Summary
Adds an automated failure reporting and alerting mechanism to the nightly test suite workflow (`nightly-test-suite.yml`). When any nightly workflow job fails, GitHub Actions automatically resolves active repository owners from `OWNERS` (excluding any absent members listed in `ABSENCE.md`), searches for open `nightly-failure` issues to prevent notification spam, and either creates a new issue or posts a recurring failure comment pinging all active owners.

## Motivation & Context
Nightly test suite runs (such as the demo workload verification against the `xmc-nightly` deployment) run asynchronously on a cron schedule. When infrastructure or reconciliation failures occur (e.g. WorkUnits stuck in `STARTING` or cluster timeouts), there was previously no proactive notification mechanism alerting repository maintainers. Maintainers had to manually inspect GitHub Actions run history to discover breakages.

## Key Changes
- **Alerting Script (`.github/scripts/nightly-failure-alert.js`)**:
  - Pure parsing functions `parseUsernames` and `filterActiveOwners` to extract candidate usernames from `OWNERS` and filter out members from `ABSENCE.md` (case-insensitive).
  - Pure markdown formatting functions `formatFailureIssueBody` and `formatRecurringCommentBody` producing structured tables with commit SHA, workflow run link, Experiment XID, and `@owner` mentions.
  - Idempotent GitHub REST API orchestrator: checks for existing open issues labeled `nightly-failure` to append a recurrence comment instead of creating duplicate issues.
- **Unit Tests (`.github/scripts/nightly-failure-alert.test.js`)**:
  - 11 unit test assertions verifying username parsing, markdown bullet cleaning, absence filtering, comment/issue markdown formatting, and mock GitHub API interactions with generic placeholder fixtures.
- **Workflow Update (`.github/workflows/nightly-test-suite.yml`)**:
  - Added Job 3 `report-nightly-failure` (`3. Report Failure & Alert OWNERS`) configured with `if: always() && (needs.trigger-demo-workload.result == 'failure' || needs.validate-workload-results.result == 'failure')` and `permissions: issues: write`.

## Example Alert Issue
- **Example Issue**: See [google/xmc#885](https://github.com/google/xmc/issues/885) for an example of the created alert issue and recurring failure comment.

## Assumptions
None — requirements were fully specified.

## Verification & Testing
- **Unit Tests**:
  - Executed `node --test .github/scripts/nightly-failure-alert.test.js` (11/11 tests passing).
  - Executed `node --test .github/scripts/*.test.js` (141/141 total repository script tests passing).
- **Pre-Commit Checks**:
  - Ran `./.githooks/pre-commit` (Buildifier, gofmt, golangci-lint, determinism check, and addlicense all passed cleanly).
- **Diff Sizing Check**:
  - Ran `./tools/analyze_diff_size.py --base-ref origin/main...` (within thresholds, no split required).

## Related Issues / Bugs
N/A

TAG=agy
CONV=acf882de-1561-404c-ba2e-ae0940451165
