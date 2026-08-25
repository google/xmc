# PR Review Checklist: google/xmc#539

Total Unresolved Comments: 1

## Unresolved Review Comments

- [ ] **[pkg/components/cms/server.go:99](https://github.com/google/xmc/pull/539#discussion_r3850682947)** (@andrzejfudro) (Comment ID 3850682947)
  > Why don't we return NotFound if an existing resource is not found when an update_mask is provided?
  > 
  > From looking at the code, if an existing resource is not found, it attempts to upsert and create it. Are we sure we can safely create the component spec based on the update_mask alone, given that
  > required fields like type and version might be omitted from the mask?
