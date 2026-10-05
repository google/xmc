// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"cmp"
	"context"
	"fmt"
	"sort"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var ComponentGVR = schema.GroupVersionResource{Group: "xmc.dev", Version: "v1alpha1", Resource: "components"}

type ComponentChecker struct {
	Client  dynamic.Interface
	ConnErr error
}

func NewDynamicClient(kubeconfig string) (dynamic.Interface, error) {
	if kubeconfig == "" {
		if cfg, err := rest.InClusterConfig(); err == nil {
			return dynamic.NewForConfig(cfg)
		}
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = kubeconfig
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("building kubeconfig: %w", err)
	}
	return dynamic.NewForConfig(cfg)
}

func CheckComponents(ctx context.Context, checker *ComponentChecker, cfg *Config) ComponentsReport {
	ns := cmp.Or(cfg.Namespace, "xmc")
	if !cfg.CheckComponents {
		return ComponentsReport{Status: StatusHealthy, Healthy: true, Skipped: true, Namespace: ns, Reason: "component checks disabled"}
	}
	if rep, ok := validateComponentChecker(checker, cfg, ns); !ok {
		return rep
	}
	list, err := checker.Client.Resource(ComponentGVR).Namespace(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) && !cfg.CheckComponentsExplicit {
			return ComponentsReport{Status: StatusHealthy, Healthy: true, Skipped: true, Namespace: ns, Reason: "components.xmc.dev CRD not installed in cluster"}
		}
		return ComponentsReport{Status: StatusFailed, Namespace: ns, Reason: "failed to list Component CRs", Error: err.Error()}
	}
	if len(list.Items) == 0 {
		return ComponentsReport{Status: StatusFailed, Namespace: ns, Reason: fmt.Sprintf("no Component CRs found in namespace %q", ns)}
	}
	sort.Slice(list.Items, func(i, j int) bool { return list.Items[i].GetName() < list.Items[j].GetName() })
	statuses, unhealthy := make([]ComponentStatus, 0, len(list.Items)), []string(nil)
	for _, item := range list.Items {
		st := deriveComponentStatus(item)
		statuses = append(statuses, st)
		if !st.Healthy {
			unhealthy = append(unhealthy, fmt.Sprintf("%s (%s)", st.Name, st.State))
		}
	}
	if len(unhealthy) > 0 {
		return ComponentsReport{Status: StatusFailed, Namespace: ns, Components: statuses, Reason: "unhealthy components: " + strings.Join(unhealthy, ", ")}
	}
	return ComponentsReport{Status: StatusHealthy, Healthy: true, Namespace: ns, Components: statuses}
}

func validateComponentChecker(checker *ComponentChecker, cfg *Config, ns string) (ComponentsReport, bool) {
	if checker != nil && checker.ConnErr == nil && checker.Client != nil {
		return ComponentsReport{}, true
	}
	if !cfg.CheckComponentsExplicit {
		return ComponentsReport{Status: StatusHealthy, Healthy: true, Skipped: true, Namespace: ns, Reason: "kubernetes cluster not configured; skipping component checks"}, false
	}
	errMsg := "kubernetes dynamic client is nil"
	if checker != nil && checker.ConnErr != nil {
		errMsg = checker.ConnErr.Error()
	}
	return ComponentsReport{Status: StatusFailed, Namespace: ns, Reason: "failed to connect to Kubernetes cluster", Error: errMsg}, false
}

type condSummary struct{ readyStatus, readyReason, readyMsg, degStatus, degMsg string }

func parseConditions(item unstructured.Unstructured) condSummary {
	var c condSummary
	conds, _, _ := unstructured.NestedSlice(item.Object, "status", "conditions")
	for _, raw := range conds {
		m, ok := raw.(map[string]any)
		if ok && m["type"] == "Ready" {
			c.readyStatus, _ = m["status"].(string)
			c.readyReason, _ = m["reason"].(string)
			c.readyMsg, _ = m["message"].(string)
		} else if ok && m["type"] == "Degraded" {
			c.degStatus, _ = m["status"].(string)
			c.degMsg, _ = m["message"].(string)
		}
	}
	return c
}

func deriveComponentStatus(item unstructured.Unstructured) ComponentStatus {
	name := item.GetName()
	enabled, hasEnabled, _ := unstructured.NestedBool(item.Object, "spec", "enabled")
	enabled = enabled || !hasEnabled
	obsGen, hasObsGen, _ := unstructured.NestedInt64(item.Object, "status", "observedGeneration")
	state, _, _ := unstructured.NestedString(item.Object, "status", "state")
	genStale := (hasObsGen && obsGen != item.GetGeneration()) || (!hasObsGen && item.GetGeneration() > 0)
	c := parseConditions(item)
	if c.degStatus == "True" {
		return ComponentStatus{Name: name, Enabled: enabled, State: "STATE_DEGRADED", Message: c.degMsg}
	}
	if !enabled {
		return deriveDisabledStatus(name, state, hasObsGen, genStale, c)
	}
	if !genStale && c.readyStatus == "True" {
		return ComponentStatus{Name: name, Enabled: true, State: "STATE_READY", Ready: true, Healthy: true, Message: c.readyMsg}
	}
	if genStale || c.readyStatus != "" || c.degStatus != "" || state != "" {
		return ComponentStatus{Name: name, Enabled: true, State: "STATE_RECONCILING", Message: c.readyMsg}
	}
	return ComponentStatus{Name: name, Enabled: true, State: "STATE_UNSPECIFIED"}
}

func deriveDisabledStatus(name, state string, hasObsGen, genStale bool, c condSummary) ComponentStatus {
	if c.readyStatus == "True" {
		return ComponentStatus{Name: name, Enabled: false, State: "STATE_READY", Ready: true, Message: c.readyMsg}
	}
	validReason := c.readyReason == "" || c.readyReason == "Disabled" || c.readyReason == "ComponentDisabled"
	validState := state == "" || state == "STATE_DISABLED" || state == "COMPONENT_STATE_DISABLED"
	if (!hasObsGen && c.readyStatus == "") || genStale || (c.readyStatus == "False" && !validReason) || !validState {
		return ComponentStatus{Name: name, Enabled: false, State: "STATE_RECONCILING", Message: c.readyMsg}
	}
	return ComponentStatus{Name: name, Enabled: false, State: "STATE_DISABLED", Healthy: true, Message: c.readyMsg}
}
