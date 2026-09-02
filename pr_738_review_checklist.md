# PR Review Checklist: google/xmc#738

Total Unresolved Comments: 12 (High/Critical: 2)

## Unresolved Review Comments

- [ ] **[tools/check_go_determinism.py](https://github.com/google/xmc/pull/738#discussion_r3913885727)** [MEDIUM] (@gemini-code-assist) (Comment ID 3913885727)
  > ![medium](https://www.gstatic.com/codereviewagent/medium-priority.svg)
  > 
  > The current linter implementation only skips single-line comments starting with `//`. It does not handle block comments (`/* ... */`), which can lead to false positives if `os.Setenv` or `time.Sleep` are mentioned inside multi-line block comments.
  > 
  > We can track the block comment state to make the linter more robust and prevent false positives.
  > 
  > ```python
  >     in_block_comment = False
  >     for idx, line in enumerate(lines):
  >         line_num = idx + 1
  >         stripped = line.strip()
  >         if in_block_comment:
  >             if "*/" in stripped:
  >                 in_block_comment = False
  >             continue
  >         if stripped.startswith("/*"):
  >             if "*/" not in stripped:
  >                 in_block_comment = True
  >             continue
  >         if stripped.startswith("//"):
  >             continue
  > ```

- [ ] **[tools/check_go_determinism_test.py](https://github.com/google/xmc/pull/738#discussion_r3913885741)** [MEDIUM] (@gemini-code-assist) (Comment ID 3913885741)
  > ![medium](https://www.gstatic.com/codereviewagent/medium-priority.svg)
  > 
  > Add a unit test to verify that block comments are correctly bypassed by the linter and do not trigger false positive violations.
  > 
  > ```suggestion
  >     def test_block_comment_bypasses(self) -> None:
  >         file_path = self.test_dir / "block_comment_test.go"
  >         file_path.write_text(
  >             "package main\n\nfunc TestBlock(t *testing.T) {\n\t/*\n\tos.Setenv(\"FOO\", \"BAR\")\n\t*/\n}",
  >             encoding="utf-8",
  >         )
  >         violations = check_file(file_path)
  >         self.assertEqual(violations, [])
  > 
  > ```

- [ ] **[cmd/health_service/server_test.go](https://github.com/google/xmc/pull/738#discussion_r3913970274)** [HIGH] (@gemini-code-assist) (Comment ID 3913970274)
  > ![high](https://www.gstatic.com/codereviewagent/high-priority.svg)
  > 
  > Using extremely short real-world sleep durations (e.g., `50*time.Millisecond` cache TTL and `30*time.Millisecond` rate limit window) in a test that runs in real wall-clock time is highly prone to flakiness on busy CI runners.
  > 
  > While the test uses `synctest.Test`, the fact that reducing these durations from 15s to 50ms reduced the wall-clock test duration from 22.5s to 0.4s indicates that the test is actually running in real-time (likely because `synctest` cannot virtualize the gRPC server's network I/O or background tasks).
  > 
  > On a loaded CI runner, CPU scheduling delays can easily exceed 50ms, which will cause the cache to expire prematurely between the first and second calls, leading to non-deterministic test failures.
  > 
  > **Recommendation:**
  > 1. Refactor the server to accept an injected mock clock interface (e.g., `clockwork.Clock`) so that the test can manually and deterministically advance time without any real-world sleeps.
  > 2. If a mock clock is not feasible, increase the durations to safer values (e.g., `500ms` or `1s`) to provide a sufficient buffer against CPU scheduling jitter, or ensure that `synctest` is fully virtualizing the time by avoiding real network listeners in the test.

- [ ] **[tools/check_go_determinism.py](https://github.com/google/xmc/pull/738#discussion_r3913970285)** [MEDIUM] (@gemini-code-assist) (Comment ID 3913970285)
  > ![medium](https://www.gstatic.com/codereviewagent/medium-priority.svg)
  > 
  > Scanning Go files line-by-line with regular expressions is fragile and easily bypassed by multi-line formatting. For example, if `time.Sleep` and its argument are split across multiple lines (which is common with `gofmt` or when comments are added), the linter will completely miss the violation:
  > 
  > ```go
  > time.Sleep(
  >     15 * time.Second,
  > )
  > ```
  > 
  > **Recommendation:**
  > Consider running the regular expression search on the entire file content using `re.finditer` instead of line-by-line, and calculate the line numbers of matches dynamically:
  > 
  > ```python
  > for match in LONG_SLEEP_PATTERN.finditer(content):
  >     line_num = content[:match.start()].count("\n") + 1
  >     # report violation...
  > ```
  > 
  > Alternatively, writing this linter as a native Go static analyzer using the `go/analysis` framework would be much more robust, as it parses the actual AST and is immune to formatting variations.

- [ ] **[test/dataengine/env_test.go](https://github.com/google/xmc/pull/738#discussion_r3914229917)** [MEDIUM] (@gemini-code-assist) (Comment ID 3914229917)
  > ![medium](https://www.gstatic.com/codereviewagent/medium-priority.svg)
  > 
  > Unconditionally calling `os.Unsetenv` in `t.Cleanup` can pollute or destroy pre-existing environment variables in the test runner's environment if they were already set before the test started. To prevent this environment pollution and ensure hermetic test execution, we should capture the original values of these environment variables and restore them to their exact original state (or unset them only if they did not previously exist).
  > 
  > ```go
  > 	origPort, portExists := os.LookupEnv("TEST_CLICKHOUSE_PORT")
  > 	origUser, userExists := os.LookupEnv("TEST_CLICKHOUSE_USER")
  > 	t.Cleanup(func() {
  > 		if portExists {
  > 			_ = os.Setenv("TEST_CLICKHOUSE_PORT", origPort)
  > 		} else {
  > 			_ = os.Unsetenv("TEST_CLICKHOUSE_PORT")
  > 		}
  > 		if userExists {
  > 			_ = os.Setenv("TEST_CLICKHOUSE_USER", origUser)
  > 		} else {
  > 			_ = os.Unsetenv("TEST_CLICKHOUSE_USER")
  > 		}
  > 	})
  > ```

- [ ] **[test/dataengine/env_test.go](https://github.com/google/xmc/pull/738#discussion_r3914270328)** [MEDIUM] (@gemini-code-assist) (Comment ID 3914270328)
  > ![medium](https://www.gstatic.com/codereviewagent/medium-priority.svg)
  > 
  > Instead of manually saving, restoring, and unsetting environment variables using `t.Cleanup` and `os.Setenv`/`os.Unsetenv` (which requires disabling the determinism linter), you can use `t.Setenv` to automatically handle the backup and restore lifecycle.
  > 
  > For variables that might be unset originally, you can call `t.Setenv` with an empty string to register the cleanup hook, and then immediately call `os.Unsetenv` to ensure they are unset when the test runs. This is much cleaner and avoids any linter bypasses.
  > 
  > ```go
  > 	for _, envVar := range []string{"TEST_CLICKHOUSE_PORT", "TEST_CLICKHOUSE_USER"} {
  > 		if val, ok := os.LookupEnv(envVar); ok {
  > 			t.Setenv(envVar, val)
  > 		} else {
  > 			t.Setenv(envVar, "")
  > 			_ = os.Unsetenv(envVar)
  > 		}
  > 	}
  > ```

- [ ] **[tools/check_go_determinism.py](https://github.com/google/xmc/pull/738#discussion_r3914270385)** [MEDIUM] (@gemini-code-assist) (Comment ID 3914270385)
  > ![medium](https://www.gstatic.com/codereviewagent/medium-priority.svg)
  > 
  > Currently, if a developer passes a directory to the script (e.g., `python3 tools/check_go_determinism.py cmd/health_service`), it will be ignored because the script only checks files ending with `_test.go` directly.
  > 
  > We can improve the usability of this tool by supporting directory arguments. If an argument is a directory, we can recurse and find all test files inside it using `find_test_files`.
  > 
  > ```suggestion
  >     if args.files:
  >         for f in args.files:
  >             if f.exists() and not is_excluded(f):
  >                 if f.is_dir():
  >                     files_to_check.extend(find_test_files(f))
  >                 elif f.name.endswith("_test.go"):
  >                     files_to_check.append(f)
  > ```

- [ ] **[test/dataengine/env_test.go](https://github.com/google/xmc/pull/738#discussion_r3914311068)** [MEDIUM] (@gemini-code-assist) (Comment ID 3914311068)
  > ![medium](https://www.gstatic.com/codereviewagent/medium-priority.svg)
  > 
  > Using `t.Setenv` to set a variable to `""` and then immediately calling `os.Unsetenv` is a non-idiomatic workaround to simulate unsetting an environment variable with automatic cleanup.
  > 
  > In Go, the standard and idiomatic way to handle custom environment variable backup and restoration (especially when the function under test like `LoadEnvFile` overwrites them using `os.Setenv`) is to use `t.Cleanup` directly. This is much cleaner and more explicit.
  > 
  > ```go
  > 	// Ensure port and user env vars are backed up and restored after the test
  > 	for _, envVar := range []string{"TEST_CLICKHOUSE_PORT", "TEST_CLICKHOUSE_USER"} {
  > 		if val, ok := os.LookupEnv(envVar); ok {
  > 			t.Cleanup(func() { os.Setenv(envVar, val) })
  > 		} else {
  > 			t.Cleanup(func() { os.Unsetenv(envVar) })
  > 		}
  > 	}
  > ```

- [ ] **[tools/check_go_determinism/main.go:81](https://github.com/google/xmc/pull/738#discussion_r3914689032)** [HIGH] (@gemini-code-assist) (Comment ID 3914689032)
  > ![high](https://www.gstatic.com/codereviewagent/high-priority.svg)
  > 
  > The static analyzer fails to detect nested multiplication expressions such as `time.Sleep(5 * time.Second * 2)` because `isLongSleepExpr` only checks if the immediate operands of `token.MUL` are time units using `isTimeUnit`. Since `5 * time.Second` is a `BinaryExpr` and not a `SelectorExpr`, `isTimeUnit` returns `false`.\n\nWe should recursively call `isLongSleepExpr` on both operands of `token.MUL` just like we do for `token.ADD` to ensure nested expressions are correctly analyzed.
  > 
  > ```go
  > \tcase *ast.BinaryExpr:\n\t\tif e.Op == token.MUL {\n\t\t\treturn isLongSleepExpr(e.X) || isLongSleepExpr(e.Y)\n\t\t}\n\t\tif e.Op == token.ADD {\n\t\t\treturn isLongSleepExpr(e.X) || isLongSleepExpr(e.Y)\n\t\t}
  > ```

- [ ] **[tools/check_go_determinism/main_test.go:113](https://github.com/google/xmc/pull/738#discussion_r3914689046)** [MEDIUM] (@gemini-code-assist) (Comment ID 3914689046)
  > ![medium](https://www.gstatic.com/codereviewagent/medium-priority.svg)
  > 
  > Add a test case to verify that nested multiplication expressions (e.g., `5 * time.Second * 2`) are correctly flagged by the static analyzer.
  > 
  > ```go
  > `,\n\t\twantViolation: true,\n\t\twantSubstr:    \"arbitrary second-level time.Sleep in test\",\n\t\t},\n\t\t{\n\t\t\tname: \"nested multiplication sleep flags violation\",\n\t\t\tsrc: `package main\nimport (\n\t\"testing\"\n\t\"time\"\n)\nfunc TestNested(t *testing.T) {\n\ttime.Sleep(5 * time.Second * 2)\n}\n`,\n\t\t\twantViolation: true,\n\t\t\twantSubstr:    \"arbitrary second-level time.Sleep in test\",\n\t\t},
  > ```

- [ ] **[tools/check_go_determinism/main.go:52](https://github.com/google/xmc/pull/738#discussion_r3914689051)** [MEDIUM] (@gemini-code-assist) (Comment ID 3914689051)
  > ![medium](https://www.gstatic.com/codereviewagent/medium-priority.svg)
  > 
  > Bazel creates symlinks in the workspace root that start with `bazel-` (e.g., `bazel-<workspace_name>`). Since the workspace name can vary or change, hardcoding specific directories like `"bazel-bin"` or `"bazel-out"` is fragile and can lead to the tool traversing into other generated Bazel directories, causing significant performance degradation or false positives.\n\nWe should exclude any path component that starts with `bazel-` to make the exclusion robust.
  > 
  > ```go
  > func isExcluded(path string) bool {\n\tparts := strings.Split(filepath.Clean(path), string(filepath.Separator))\n\tfor _, part := range parts {\n\t\tif excludedDirs[part] || strings.HasPrefix(part, \"bazel-\") {\n\t\t\treturn true\n\t\t}\n\t}\n\treturn false\n}
  > ```

- [ ] **[tools/check_go_determinism/main_test.go:262](https://github.com/google/xmc/pull/738#discussion_r3914689059)** [MEDIUM] (@gemini-code-assist) (Comment ID 3914689059)
  > ![medium](https://www.gstatic.com/codereviewagent/medium-priority.svg)
  > 
  > Add a test case to verify that custom Bazel workspace symlinks (e.g., `bazel-my-custom-workspace`) are correctly excluded.
  > 
  > ```go
  > \t\t{\"bazel-bin/cmd/foo_test.go\", true},\n\t\t{\"bazel-out/cmd/foo_test.go\", true},\n\t\t{\"bazel-my-custom-workspace/foo_test.go\", true},
  > ```
