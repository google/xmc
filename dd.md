“Blue doc” design doc template

| \#begin-approvals-addon-section See [go/g3a-approvals](http://goto.google.com/g3a-approvals) for instructions on how to add reviewers. Do not edit this section manually. |
| :---: |

**Self link:** [go/bluedoc](http://goto.google.com/bluedoc)

**Visibility**:  

**Status**: 

**Authors**: 

**Team**: [*team/2041806362319*](http://team/2041806362319) 

**Tracking Buganizer** **issue****/hotlist**: [b/492437425](http://b/492437425) 

**UI Design:** [go/uxd-xmanager-adminpanel](http://goto.google.com/uxd-xmanager-adminpanel) 

**Last major revision**: 

# Context

## Objective

The objective of this feature is to allow admins to enable a temporary pause of communication with Kueue and the Control Plane from XMC.

## Background

There were past client incidents with overflowing Kueue instances. Adding an option to pause traffic to the control plane from XMC will allow admins to analyze the incident and stop adding to the problem from the XMC side.

## Current state

### Communication with Kueue/Control Plane in XMC

Here is a breakdown of the existing communications between XMC services and Kueue/Kubernetes:

#### Actuator

Actuator currently takes 4 types of operations ([ref](http://cs/google3/third_party/xmanager_cloud/task_queue/proto/task.proto?l=64)) from the Redis stream xmc-tasks-stream:

- CreateAction
- DeleteAction
- RestartAction
- UpdateAction
These are handled in [workunit\_task\_handler](http://cs/google3/third_party/xmanager_cloud/kubernetes/actuator/workunit_task_handler.go?l=102):

- **CreateAction**
    - **Triggered by:** Creation of the work unit in ESS.
    - **Effect:** [Creates JobSet](http://cs/google3/third_party/xmanager_cloud/kubernetes/actuator/k8s_client/client.go?l=118) using a [dynamic client](http://cs/google3/third_party/xmanager_cloud/kubernetes/actuator/k8s_client/jobset.go?l=98). When creating a JobSet, it adds Kueue-specific labels (e.g., kueue.x-k8s.io/queue-name) to specify the target Kueue queue and priority. This action effectively enqueues the job within Kueue's system. Kueue then manages the lifecycle of this JobSet based on resource availability and queue configuration.
- **DeleteAction**
    - **Triggered by:** BatchStopWorkUnits in ESS.
    - **Effect:** Invokes the dynamic client to delete the JobSet ([ref](http://cs/google3/third_party/xmanager_cloud/kubernetes/actuator/k8s_client/jobset.go?l=152)).
- **RestartAction**
    - **Triggered by:** BatchRestartWorkUnits in ESS.
    - **Effect:**
        - [Deletes JobSet](http://cs/google3/third_party/xmanager_cloud/kubernetes/actuator/k8s_client/jobset.go?l=208).
        - [Creates new JobSet](http://cs/google3/third_party/xmanager_cloud/kubernetes/actuator/k8s_client/jobset.go?l=221).
- **UpdateAction**
    - **Triggered by:** [Update work unit](http://cs/google3/third_party/xmanager_cloud/experiment_state_server/server/updateworkunit.go?l=77) in ESS.
    - **Effect:** Updates JobSet priority by calling a dynamic client patch on the JobSet ([ref](http://cs/google3/third_party/xmanager_cloud/kubernetes/actuator/k8s_client/jobset.go?l=258)). This modifies existing JobSet resources to update Kueue-related labels or annotations.
#### Reconciler

The Reconciler watches Kubernetes resources and updates Spanner:

- **JobSet Reconciler:** Watches JobSets and performs patches to remove finalizers ([reconciler.go](http://cs/google3/third_party/xmanager_cloud/kubernetes/reconciler/jobset/reconciler.go?l=252), [svg](http://flows/artifacts/reconcile_jobset.svg)).
- **Kueue Sync Worker:** Periodically fetches information from Kueue about the status of workloads (queue position, admission status) to update WorkUnitExecution status in Spanner.
- **ClusterQueue Reconciler:** Watches Kueue's ClusterQueue resources to maintain a cache of active ClusterQueue names for the Kueue Sync Worker.
- **Workload Reconciler:** Watches Kueue Workload objects (created by Kueue to manage pods within a JobSet) to extract scheduling information, admission status, and potential issues (such as being inadmissible), converting them into updates for WorkUnitExecution in Spanner.
**Summary of Interactions:**

- **Actuator -> Kueue:** Submits and manages jobs by creating/updating JobSets tagged for Kueue.
- **Reconciler -> Kueue:** Reads data from Kueue's API (Workload status, ClusterQueue list) to monitor job state and queue status.
# Requirements

## Functional Requirements

- The system **must** implement a Global "STOP" Feature (e.g., a button or toggle in the Admin Panel) that allows an XMC administrator to temporarily halt all Actuator scheduling operations, effectively pausing all interaction with Kueue and the Kubernetes control plane.
- The system **must** durably persist the "STOP" state in a database, ensuring the system remains paused even across service restarts or pod rescheduling.
- The system **must** require explicit manual action from an administrator to un-pause the system and resume normal scheduling operations.
- The system **must** ensure that operations enqueued during an active pause are retained safely in the Redis stream and automatically picked up and processed once the pause is lifted.
- The system **must** pause all operation types (CreateAction, DeleteAction, RestartAction, UpdateAction) uniformly to completely halt traffic to Kubernetes during an incident.
## Non-Functional Requirements

- **Access Control:** Access to the Pause Scheduling toggle **must** be restricted to authorized administrators. User-facing endpoints **must** validate user permissions.
- **Reliability & Health Separation:** The system **must** ensure that a "paused" service is still considered "healthy" by the Kubernetes liveness/readiness probes, as pausing is an intended operational state.
- **Performance:** The system should employ caching mechanisms for the pause state to minimize database lookups on task processing.
## Out of Scope

- **Automatic Pause/Resume or Throttling:** The system **will not** automatically pause or resume scheduling based on health conditions or queue depth; this is strictly a manual admin function.
# Design

## Overview

- We will create a new Spanner table SystemConfig to persist the state of the Pause toggle.
- A new API endpoint will be exposed on the Health Service to allow admins to change the setting. Health Service will persist the state in Spanner SystemConfig and publish a pause event to a dedicated Redis broadcast stream (e.g., xmc-sys-config-stream) via XADD.
- The existing single Redis stream xmc-tasks-stream will be retained without structural changes.
- Actuator instances will independently consume the configuration stream via XREAD (without consumer groups) to update their in-memory pause state before pulling or processing incoming operations from xmc-tasks-stream.
## Spanner changes

We will introduce a new Spanner table in the experiment-db database:

CREATE TABLE SystemConfig (

  KueueSchedulingPaused BOOL NOT NULL DEFAULT (FALSE)

) PRIMARY KEY ();


The table will not contain any primary keys, which by design enforces it to contain at most one row ([ref](https://docs.cloud.google.com/spanner/docs/schema-and-data-model#choose_a_primary_key)).

The SystemConfig table will be empty until the first configuration is saved into the database; in this case, the code will return the default value (false). After the data is persisted via the update endpoint, queries will return the persisted state.

A migration script will be added to create the table on upgrade and drop it on downgrade.

## Update Kueue Scheduling Pause State Endpoint

The Health Service will expose an endpoint to control the Kueue scheduling pause state. This endpoint will verify admin credentials, persist the new state in Spanner, and broadcast the change to Actuator instances via Redis.

### Health Service Endpoint

- **Endpoint:** /api/admin/kueue\_scheduling\_pause
- **HTTP Method:** PATCH
- **Description:** Initiates the process to set the global state for pausing or resuming the scheduling of operations by the Actuator.
- **Authentication:** Requires Admin privileges, enforced by the Health Service.
**Request Body:**

{ "paused": true }


- **paused (boolean, required):** Set to true to pause scheduling, false to resume.
**Response Body (Health Service):**

- **Success (200 OK):**
{ "message": "Pause state updated in Spanner and broadcasted to Actuator instances.", "requested\_state": true }


- **Error:** Standard HTTP error codes:
    - **400 Bad Request:** Invalid request body.
    - **500 Internal Server Error:** Failed to update Spanner or publish to Redis stream.
    - **503 Service Unavailable:** Spanner or Redis service is unreachable.
### Health Service Handler Logic

- Receive the PATCH request on /api/admin/kueue\_scheduling\_pause.
- Validate the caller's admin membership using Authorizer.IsUserInGroup.
- Perform an atomic update on the KueueSchedulingPaused column in the SystemConfig table in Spanner.
- If the Spanner update succeeds, publish a pause configuration event (XADD) to the Redis broadcast stream xmc-sys-config-stream.
- If both operations succeed, return a 200 OK. Otherwise, return an appropriate HTTP error status.
### Actuator Redis Broadcast Listener

Actuator no longer exposes an internal HTTP endpoint for pause state changes. Instead, each Actuator instance consumes real-time configuration updates from Redis.

#### Actuator Listener Logic

- On pod startup, Actuator performs a synchronous read of the SystemConfig table in Spanner to initialize its in-memory currentPauseState.
- Actuator spawns a background listener goroutine that calls XREAD on the xmc-sys-config-stream Redis stream.
    - The XREAD call is made without consumer groups, using $ as the starting ID to listen for any new configuration events broadcasted after startup.
- When a pause configuration event is received from the stream, the listener parses the new state and updates the Actuator's internal in-memory state variable (protected by sync.RWMutex).
// Example in-memory state in Actuator

type PauseState struct {

    mu     sync.RWMutex

    Paused bool

}

var currentPauseState PauseState

// In XREAD listener goroutine upon receiving an event:

currentPauseState.mu.Lock()

currentPauseState.Paused = event.Paused

currentPauseState.mu.Unlock()

## Notify actuator about change of Pause state

### Selected: Redis Streams with XREAD (no consumer groups)

- **Mechanism:** Health Service publishes pause state change events to a dedicated Redis stream (xmc-sys-config-stream) using XADD. Each Actuator pod runs an independent background goroutine calling XREAD (without consumer groups, starting from $) on this stream to receive broadcast events and update its local in-memory mutex.
- **Pros:**
    - Natively supports multi-instance fan-out broadcast to all running Actuator pods.
    - Completely decouples Health Service from Actuator instances (no need to track pod IPs or manage internal HTTP routing).
    - Extremely simple consumer setup with no consumer group management or cleanup overhead.
- **Cons:**
    - Requires Actuator pods to read initial baseline state from Spanner on startup to ensure they don't miss events while offline.
## Pause button behavior

Enabling KueueSchedulingPaused stops the Actuator from interacting with Kueue and the Kubernetes control plane. To achieve this cleanly without modifying publisher or stream topologies, we implement a direct pause on stream consumption.

### Selected: Pause Stream Consumption

- **Mechanism:** We retain the existing single Redis stream xmc-tasks-stream. In the Actuator's main stream consumer worker loop within [workunit\_task\_handler](http://cs/google3/third_party/xmanager_cloud/kubernetes/actuator/workunit_task_handler.go?l=102) (specifically before calling XReadGroup to pull new tasks), the worker checks currentPauseState.Paused via a read lock (currentPauseState.mu.RLock()).
- If Paused is true, the worker pauses pulling new messages from the stream (e.g. sleeps for a short interval before retrying), leaving all pending tasks safely queued on xmc-tasks-stream.
- Once toggled false, the worker immediately resumes pulling and dispatching messages to worker goroutines in their original FIFO order.
- **Pros:**
    - Extreme simplicity: zero changes required to ESS publisher logic, zero changes to Redis stream or consumer group topologies, and zero migration overhead.
    - Preserves strict task ordering across all operation types.
- **Cons:** Deletions and updates are paused alongside creations during an active incident pause.
**Reasoning for Selection:** Pausing stream consumption directly in the worker loop ([ref](http://cs/google3/third_party/xmanager_cloud/kubernetes/actuator/workunit_task_handler.go?l=102)) is selected because halting all Kubernetes communication is the exact desired behavior during a Control Plane/Kueue incident. Retaining a single stream drastically simplifies backend architecture and avoids the risk and overhead of stream migrations.

# Reliability

To ensure the Pause Scheduling mechanism remains robust and consistent during service deployments or database blips, the system implements the following safeguards:

## 1. In-Memory State Decoupling

The Actuator leverages an in-memory currentPauseState struct protected by a sync.RWMutex. The stream consumer loop ([ref](http://cs/google3/third_party/xmanager_cloud/kubernetes/actuator/workunit_task_handler.go?l=102)) checks this memory cache via a read lock (currentPauseState.mu.RLock()) on every task evaluation. This completely decouples stream processing from Spanner availability, ensuring that Spanner latency or temporary database unreachability never blocks or slows down task processing.

## 2. Startup Synchronization & Recovery

When an Actuator pod starts up, it must perform a synchronous read of the SystemConfig table in Spanner to initialize currentPauseState.Paused. If Spanner is unreachable during bootstrap, Actuator will retry initialization with exponential backoff before starting its Redis stream listeners, guaranteeing it never processes tasks under an incorrect pause state.

# Rejected alternatives

## Split Redis Streams by Action Type

Split the unified stream into 4 dedicated Redis streams (xmc-create-tasks-stream, xmc-delete-tasks-stream, etc.) to allow selective pausing of create actions while letting deletions and updates proceed.

- **Reason for rejection:** Rejected because during a Kueue or Kubernetes Control Plane incident, the goal is to halt all traffic to the cluster. Furthermore, splitting streams introduces significant publisher refactoring in ESS, requires managing 4 consumer groups in Actuator, and complicates task ordering.
## Secondary Paused Tasks Stream

Keep the unified xmc-tasks-stream and republish paused CreateAction tasks to an xmc-paused-tasks-stream.

- **Reason for rejection:** Rejected because it introduces extra republishing latency, complicates task ordering, and requires complex coordination between the main consumer and the paused consumer upon resumption.
## Polling Spanner in Actuator

Instead of Health Service publishing real-time events to a Redis XREAD stream, Actuator could poll the SystemConfig table in Spanner every 5–10 seconds to update its local cache.

- **Reason for rejection:** Rejected because periodic polling introduces an undesirable delay (up to 10 seconds) between an admin clicking "STOP" in the UI and the Actuator actually halting task processing, while also generating unnecessary continuous background read traffic against Spanner.
## Redis PubSub

- **Mechanism:** Each Actuator pod subscribes to a specific Redis channel (e.g., actuator-sys-broadcast) upon startup. When ESS receives the trigger request, it publishes a message to this channel. Redis instantly pushes the message to all active subscribers.
- **Pros:**
    - Zero New Infrastructure: Reuses the existing Redis instance and go-redis client.
    - Extremely Low Latency: Instantaneous push-based delivery.
    - Decoupled: ESS does not need to know how many Actuator pods exist or track their individual IP addresses.
- **Cons:**
    - At-Most-Once Delivery: Pub/Sub messages are not persisted. If an Actuator pod is restarting or temporarily disconnected during the broadcast, it misses the message. (Mitigation: Actuator pods can pull the latest baseline state from Spanner/ConfigMap on startup).
- **Reason for rejection:** Redis PubSub provides at-most-once delivery semantics without persistence. If an Actuator pod is temporarily restarting, being rescheduled, or experiencing a brief network disconnection exactly when the Health Service broadcasts the pause event, that pod will completely miss the message. Relying on unpersisted broadcast messages introduces a race condition and leaves the system vulnerable to inconsistent pause states across replicas during network blips.
## Headless Service + HTTP

- **Mechanism:** Create a Kubernetes Headless Service (clusterIP: None) selecting the Actuator pods. When ESS wants to broadcast, it performs a DNS A-record lookup against the headless service FQDN to get the IP addresses of all running Actuator pods. ESS then iterates over the IPs and makes a direct HTTP request to each pod.
- **Pros:**
    - Explicit Confirmation: ESS receives an HTTP status code from each pod, knowing exactly which pods succeeded or failed.
    - No External Dependency: Operates entirely within Kubernetes networking.
- **Cons:**
    - Complexity & Brittle Error Handling: If 4 pods succeed and 2 fail/timeout, ESS must implement complex retry logic.
    - DNS Caching / Latency: Kubernetes DNS propagation/caching can cause ESS to attempt reaching terminating pods or miss newly spawned pods.
- **Reason for rejection:** Managing direct pod-to-pod HTTP communication introduces significant architectural complexity and tight coupling. The Health Service would need to handle partial failures (e.g., some pods returning 200 while others timeout), implementing complex retry loops and fallback mechanisms. Furthermore, Kubernetes DNS propagation delays and caching mean the Health Service might attempt to send requests to terminating pods while missing newly spawned replicas, leading to unreliable state synchronization.
## Kubernetes Watch API

- **Mechanism:** Create a dedicated Kubernetes Custom Resource Definition (CRD) or ConfigMap to store the pause state. Actuator pods would use the Kubernetes client-go Watch API to maintain an active, persistent watch on this resource, receiving immediate push notifications from the Kubernetes API server whenever the Health Service updates it.
- **Pros:**
    - Native Kubernetes integration with built-in retry and re-watch capabilities.
    - Push-based delivery directly from the Kubernetes Control Plane.
- **Cons:**
    - Introduces a heavy dependency on the Kubernetes API server for application-level feature toggling.
    - Requires granting Actuator additional RBAC permissions (ClusterRole/RoleBinding) to watch cluster resources.
- **Reason for rejection:** The primary objective of the XMC Pause feature is to halt traffic during a Kubernetes Control Plane or Kueue incident. Relying on the Kubernetes API server's Watch mechanism to transmit the pause signal creates a dangerous circular dependency: if the Kubernetes Control Plane is degraded, unresponsive, or overwhelmed during an incident, the Actuator pods may be unable to receive the critical pause event intended to relieve pressure on the cluster.
## Redis Streams Consumer Groups Per Pod

- **Mechanism:** Create a separate Redis Stream consumer group for each Actuator pod (e.g., dynamically naming the consumer group using the pod's HOSTNAME or unique ID). The Health Service publishes events to the stream, and Redis delivers a copy to each consumer group.
- **Pros:**
    - Provides reliable, acknowledged, at-least-once delivery to every pod.
- **Cons:**
    - Substantial management overhead: as Kubernetes dynamically scales, restarts, or reschedules pods, old consumer groups are left orphaned in Redis.
- **Reason for rejection:** While consumer groups per pod provide reliable delivery, they require implementing complex, non-trivial garbage collection logic to detect and delete orphaned consumer groups for terminated pods (otherwise, unconsumed messages would accumulate indefinitely in Redis memory). Using Redis XREAD without consumer groups achieves the exact same fan-out broadcast reliability with significantly simpler logic and zero cleanup overhead.
# Future considerations

- **Actuator Multi-Instance Scaling:** Natively supported. Because each Actuator pod independently calls XREAD on the Redis configuration stream, any number of Actuator replicas will seamlessly receive pause state broadcasts without requiring additional synchronization mechanisms.
