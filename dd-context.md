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

I asked my PM the question:
"""
Can you share what you think is the expected user experience when the pause is active? For example, if a user tries to create a new experiment via the client, should we:

Queue jobset creation: XMC creates an experiment in the Spanner database. The experiment is visible on the list but the experiment state doesn't show as running at this point (its state can be something like "initializing" or "paused", example). XMC does not create jobsets yet but holds information about their creation so they can be handled later. When the admin disables the pause, the actuator will process the queued jobset creations, allowing experiments created during the pause to run.
OR

Reject the request: The API returns an error with a message that no new experiments can be created when the pause is active. The user needs to wait for admins to switch the pause off to be able to create/update/restart experiments.
Both options solve the main problem of not scheduling new things during the incident, but they do differ in complexity (option 2 is significantly simpler as it doesn't require persisting paused messages). Can you clarify if we should prefer one option over the other?
"""

got answer:
"""
Hi Andrzej,
Thanks for asking. Currently pausing is an local workload feature - ie. it corresponds to the single workload and researcher ability to pause it. But regarding those more admin centric features definitely option no. 1 seems more robust from the researcher perspective as XMC can still serve as a single point of experimentation we allow so they don't need to be tracking infra pause consistently or during pause keep separate tracking of what they want to run next.
"""

asked my PM what is expected time of the pause - they said it might be around 24 hours. During this time there might be hundreds of jobs scheduled. While it is not that much we need to also plan for HPO, then each of those hundred experiment might schedule another hundreds of jobsets. So in the worst case we can expect thousands of jobsets to be created while the pause is active.



---


health service design doc: health-service-dd.md