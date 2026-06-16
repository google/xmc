Description from expectations:
```
Workload stop/start functionality
Problem statement: a XMC admin needs to be able to pause admission of new workloads (workunits) to K4 quickly and resume workloads admission. Deliverable: Admin panel button for pausing/unpausing scheduling to k4 + logic in actuator to implement the actual pause.
```

Description from task:
```
in case when XMC or the underlying infrastructure becomes un-healthy (unstable, overloaded, etc) we need to give a XMC admin a way to temporarily pause all scheduling = effectively stop the actuator from interacting with kueue/Ccontrol plane

this information (toggle) needs to be persisted in DB so that after any restarts (since cluster is in unstable condition) the pause is continued.

Manual un-pausing should be required to bring back scheduling.

paused state should not affect healthchecks (paused actuator can be a healthy actuator).
```