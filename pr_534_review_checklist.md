# PR Review Checklist: google/xmc#534

Total Unresolved Comments: 1

## Unresolved Review Comments

- [ ] **[pkg/components/cms/server.go:183](https://github.com/google/xmc/pull/534#discussion_r3851230102)** (@pachopepe) (Comment ID 3851230102)
  > Can this call here be avoided, asking AI appears it can be:
  > 
  > ```
  > While this workaround avoids the 404 error, applying it "apart" has clear design trade-offs:                                                                                
  >                                                                                                                                                                               
  >   1. Violation of DRY:                                                                                                                                                        
  >   server.go:198-215 in cms/server.go is an exact duplicate of the private memory_store.go:225-239 helper in store/memory_store.go.                                            
  >   2. Stale Metadata (Etag & UpdateTime) Bug:                                                                                                                                  
  >   Inside memory_store.go:202-203, the store updates:                                                                                                                          
  >     rec.UpdateTime = time.Now().UTC()                                                                                                                                         
  >     rec.Etag = generateEtag(rec)
  >   However, server.go:198-215 does not recalculate the Etag or refresh UpdateTime. Consequently, when deleting a component, the returned protobuf has a stale etag and stale
  >   update_time.
  >   3. Double Store Lookups:
  >   For normal (non-deleted) operations, the server executes two sequential store operations (UpdateObservedGeneration followed by GetComponent), taking locks twice.
  >     
  >   │ Note
  >   │ Cleaner Alternative: If the store.go:87-120 interface was defined as:
  >   │
  >   │   UpdateObservedGeneration(ctx context.Context, ...) (*ComponentRecord, error)
  >   │
  >   │ (matching SetComponentIntent), the store itself could return the updated record directly (with fresh Etag and UpdateTime, regardless of whether State == DELETED). This
  >   │ would completely eliminate the pre-fetch logic, the duplicate helper, and the stale metadata issue.
  > ```
