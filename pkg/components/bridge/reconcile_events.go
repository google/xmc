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

package bridge

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
)

// eventSourceComponent is the source component of the Events the bridge emits.
const eventSourceComponent = "xmc-bridge"

// NewEventRecorder returns a recorder that writes Events, as component xmc-bridge, through client,
// and a function that stops the underlying broadcaster. Call the stop function at exit: it hands
// queued Events to the sink, but Events still being written when the process exits may be lost.
func NewEventRecorder(client kubernetes.Interface) (record.EventRecorder, func()) {
	broadcaster := record.NewBroadcaster()
	broadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: client.CoreV1().Events("")})
	return broadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: eventSourceComponent}), broadcaster.Shutdown
}

// EmitReconcileEvents records res.WarningEvent, if any, as a Warning Event on item; a nil recorder
// emits nothing. Call it before PersistReconcileResult, because Events outlive a custom resource
// whose finalizer that call releases. The recorder sends Events asynchronously and aggregates
// repeats into one Event; a failed send is only logged by the broadcaster.
func EmitReconcileEvents(recorder record.EventRecorder, item runtime.Object, res *ReconcileResult) {
	if recorder == nil || item == nil || res == nil || res.WarningEvent == nil {
		return
	}
	recorder.Event(item, corev1.EventTypeWarning, res.WarningEvent.Reason, res.WarningEvent.Message)
}
