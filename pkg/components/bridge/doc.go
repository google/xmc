// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package bridge implements the GitOps Bridge subsystem and core reconciliation
// state machine for XManager on Cloud (XMC) Components.
//
// # Architecture & Overview
//
// The GitOps Bridge serves as the bi-directional synchronizer between Kubernetes
// Custom Resource Definitions (Component CRDs under xmc.dev/v1alpha1) and the
// Component Manager Service (CMS) in the Experiment State Server (ESS).
//
// # Reconciliation Lifecycle
//
// The Reconciler evaluates each Component CR through a linear state machine:
//
//  1. Deletion Check:
//     - If metadata.deletionTimestamp is set and spec.deletionProtection is true:
//     Emits ConditionTypeDeletionBlocked (True) and halts deletion.
//     - If metadata.deletionTimestamp is set and unprotected:
//     Executes child workload pruning and calls CMS ExecuteDeletion, then removes
//     the finalizers.xmc.dev/deletion-protection finalizer.
//
//  2. Deletion Protection Finalizer Lifecycle:
//     - When spec.deletionProtection is enabled, attaches finalizers.xmc.dev/deletion-protection.
//
//  3. Intent Application:
//     - Propagates spec to CMS via SetComponentIntent, updating CMS state to
//     COMPONENT_STATE_PENDING_RECONCILIATION.
//
//  4. Enablement State:
//     - If spec.enabled is false: transitions state to COMPONENT_STATE_DISABLED and
//     updates observed generation with CMS.
//     - If spec.enabled is true: reconciles 1P workloads (Deployments/Services) or 3P CR
//     status probers, evaluates health, and updates observed generation with CMS.
//
// # Condition Types
//
// The Reconciler maintains standard metav1.Condition objects:
//   - ConditionTypeReady ("Ready"): Indicates overall component operational readiness.
//   - ConditionTypeDegraded ("Degraded"): Indicates probe or provisioning errors.
//   - ConditionTypeDeletionBlocked ("DeletionBlocked"): Indicates deletion was blocked
//     due to active deletion protection.
package bridge
