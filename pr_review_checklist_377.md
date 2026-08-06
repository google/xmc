# PR Review Checklist: google/xmc#377

Total Unresolved Comments: 1

## Unresolved Review Comments

- [ ] **[cmd/actuator/k8s_client/jobset.go:623](https://github.com/google/xmc/pull/377#discussion_r3729225189)** (@gemini-code-assist) (Comment ID 3729225189)
  > ![high](https://www.gstatic.com/codereviewagent/high-priority.svg)
  > 
  > Returning `nil, err` from `constructJobSet` when a contract violation is detected will trigger a nil-pointer panic in `createJobSet` (at line 78). In `createJobSet`, `logging.Info(ctx, "Create JobSet", "jobSet", jobSet.GetName())` is called immediately after `constructJobSet` without checking if `err` is non-nil.
  > 
  > To prevent this panic, please ensure that `createJobSet` is updated to check the error before calling `jobSet.GetName()`. For example:
  > 
  > ```go
  > jobSet, err := c.constructJobSet(ctx, workUnit, target, author, namespace, operationTime, priorities)
  > if err != nil {
  >     logging.Warn(ctx, "Failed to construct JobSet manifest", "error", err)
  >     return fmt.Errorf("failed to construct JobSet manifest: %w", err)
  > }
  > logging.Info(ctx, "Create JobSet", "jobSet", jobSet.GetName())
  > ```
