# Review Plan: Kueue Scheduling Pause Feature

This document tracks observations, questions, and next steps for refining the [dd.md](file:///usr/local/google/home/andrzejfudro/Documents/Development/xmc/dd.md) design document, based on the context in [dd-context.md](file:///usr/local/google/home/andrzejfudro/Documents/Development/xmc/dd-context.md).

## 1. Rules for Agents

When executing the actions below, agents must adhere to these rules:

1.  **Do Not Change Formatting:** Do not reformat the design document (`dd.md`) beyond the required content updates. Preserve existing markdown styles, tables, and spacing.

## 2. Proposed Actions (Doc Refinement)

These actions focus on finalizing the design document.

1.  **Update `dd.md` with Actuator Reconnection Logic:** Add detailed design for the reconnection and re-sync flow (re-reading Spanner and catching up on Redis stream) when Actuator loses connection to Redis during runtime.
2.  **Update `dd.md` with Pause/Resume Signaling:** Add detailed design for a signaling mechanism (e.g., Go channels) to wake up the worker immediately upon resume, replacing the sleep-based check.
3.  **Update `dd.md` with Redis Stream Cleanup:** Add details about using `MAXLEN` (e.g., `MAXLEN ~ 100`) for `xmc-sys-config-stream` to prevent indefinite growth.
4.  **Update `dd.md` with Spanner Schema Decision:** Refine the Spanner schema section based on the decision between a singleton table and a key-value format (after resolving 3.6).

## 3. Questions & Points to Revisit/Evaluate

These points need to be evaluated and resolved to refine the design document before implementation begins.

### 3.1. Spanner & Redis Consistency (Split-Brain Risk)
*   **Topic:** If Spanner updates but Redis publish fails, the system enters a split-brain state.
*   **Evaluation Needed:** How do we handle this? Should we implement a periodic fallback sync in Actuator, or rely on admin retry? How should the UI reflect this?

### 3.2. Redis Stream Cleanup
*   **Topic:** Preventing `xmc-sys-config-stream` from growing indefinitely.
*   **Evaluation Needed:** Confirm if using `MAXLEN` (e.g., `MAXLEN ~ 100`) is the agreed approach for cleanup.

### 3.3. Actuator Runtime Reconnection
*   **Topic:** Actuator behavior when it loses connection to Redis during runtime.
*   **Evaluation Needed:** Define the reconnection and re-sync flow (re-reading Spanner and catching up on Redis stream) in the design doc.

### 3.4. Pause/Resume Latency & Efficiency
*   **Topic:** Avoiding busy-sleeping in the Actuator worker loop when paused.
*   **Evaluation Needed:** Detail a signaling mechanism (e.g., Go channels) in the design doc to wake up the worker immediately upon resume.

### 3.5. Reconciler Activity during Incident
*   **Topic:** Reconciler continues to run and remove finalizers during a pause.
*   **Evaluation Needed:** Is it safe to assume Reconciler load is negligible during a control plane incident? (See TODO).

### 3.6. Spanner Schema Extensibility
*   **Topic:** `SystemConfig` singleton table (`PRIMARY KEY ()`).
*   **Evaluation Needed:** Revisit if we should use a singleton table or a more extensible key-value format for system configurations.

### 3.7. Scale of Finalizer Removals
*   **Topic:** What is the expected scale of finalizer removals during peak times?
*   **Evaluation Needed:** Gather data to validate if keeping Reconciler active is safe.

*Note: We assume that health check endpoints in Actuator that monitor stream consumption exist and will need adjustment to not report unhealthy when paused.*

## 4. TODOs

*   [ ] **@andrzejfudro**: Clarify if Reconciler activity (finalizer removal) is acceptable during K8s/Kueue incidents.
