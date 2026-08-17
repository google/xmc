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

// Command xmc-bridge implements the GitOps Bridge Controller for XMC Components.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/timestamppb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/bridge"
)

var componentGVR = schema.GroupVersionResource{
	Group:    "xmc.dev",
	Version:  "v1alpha1",
	Resource: "components",
}

func main() {
	var (
		cmsAddr       = flag.String("cms-address", "experiment-state-server:50051", "Address of the CMS gRPC control plane service")
		namespace     = flag.String("namespace", "xmc", "Kubernetes namespace to watch for Component custom resources")
		probeInterval = flag.Duration("probe-interval", 15*time.Second, "Interval between active health probes")
	)
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.Printf("Starting XMC GitOps Bridge Controller in namespace %q connecting to CMS at %q...", *namespace, *cmsAddr)

	conn, err := grpc.NewClient(*cmsAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to dial CMS service at %q: %v", *cmsAddr, err)
	}
	defer conn.Close()

	k8sConfig, err := rest.InClusterConfig()
	if err != nil {
		k8sConfig, err = clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
		if err != nil {
			log.Fatalf("Failed to load kubernetes config: %v", err)
		}
	}

	clientset, err := kubernetes.NewForConfig(k8sConfig)
	if err != nil {
		log.Fatalf("Failed to create kubernetes clientset: %v", err)
	}

	dynamicClient, err := dynamic.NewForConfig(k8sConfig)
	if err != nil {
		log.Fatalf("Failed to create dynamic kubernetes client: %v", err)
	}

	cmsClient := componentspb.NewComponentManagerServiceClient(conn)
	prober := bridge.NewNetworkProber(5 * time.Second)
	workloadManager := bridge.NewK8sWorkloadManager(clientset)

	reconciler, err := bridge.NewReconciler(cmsClient, prober, workloadManager)
	if err != nil {
		log.Fatalf("Failed to initialize reconciler: %v", err)
	}

	log.Printf("GitOps Bridge Controller initialized successfully. Active reconciliation running every %v", *probeInterval)

	// Run periodic reconciliation loop.
	go func() {
		ticker := time.NewTicker(*probeInterval)
		defer ticker.Stop()

		// Run immediate initial reconciliation pass.
		runReconcilePass(ctx, dynamicClient, reconciler, *namespace)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runReconcilePass(ctx, dynamicClient, reconciler, *namespace)
			}
		}
	}()

	// Graceful shutdown handling.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-sigCh:
		log.Println("Received termination signal. Shutting down Bridge Controller gracefully.")
	case <-ctx.Done():
	}
}

func runReconcilePass(ctx context.Context, dynamicClient dynamic.Interface, reconciler *bridge.Reconciler, namespace string) {
	list, err := dynamicClient.Resource(componentGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Printf("Failed to list Component resources in namespace %q: %v", namespace, err)
		return
	}

	for _, item := range list.Items {
		name := item.GetName()
		gen := item.GetGeneration()
		isDeleting := item.GetDeletionTimestamp() != nil

		hasFinalizer := false
		for _, f := range item.GetFinalizers() {
			if f == bridge.FinalizerDeletionProtection {
				hasFinalizer = true
				break
			}
		}

		spec := parseComponentSpec(item.Object["spec"])
		if spec.Parameters == nil {
			spec.Parameters = make(map[string]string)
		}
		if string(item.GetUID()) != "" {
			spec.Parameters["ownerUID"] = string(item.GetUID())
		}
		existingConds := parseExistingConditions(item.Object["status"])
		res, err := reconciler.ReconcileComponent(ctx, namespace, name, gen, spec, isDeleting, hasFinalizer, existingConds)
		if err != nil {
			log.Printf("Error reconciling component %q: %v", name, err)
			continue
		}
		log.Printf("Reconciled component %q: state=%v, readyCond=%d", name, res.State, len(res.Conditions))
		for _, c := range res.Conditions {
			log.Printf("  -> Condition %s: Type=%s Status=%s Reason=%s Message=%s", name, c.GetType(), c.GetStatus(), c.GetReason(), c.GetMessage())
		}

		// Handle Finalizers
		if res.AddFinalizer && !hasFinalizer {
			finalizers := append(item.GetFinalizers(), bridge.FinalizerDeletionProtection)
			item.SetFinalizers(finalizers)
			updated, err := dynamicClient.Resource(componentGVR).Namespace(namespace).Update(ctx, &item, metav1.UpdateOptions{})
			if err != nil {
				log.Printf("Failed to add finalizer to %q: %v", name, err)
			} else {
				item = *updated
			}
		} else if res.RemoveFinalizer && hasFinalizer {
			var updatedFinalizers []string
			for _, f := range item.GetFinalizers() {
				if f != bridge.FinalizerDeletionProtection {
					updatedFinalizers = append(updatedFinalizers, f)
				}
			}
			item.SetFinalizers(updatedFinalizers)
			updated, err := dynamicClient.Resource(componentGVR).Namespace(namespace).Update(ctx, &item, metav1.UpdateOptions{})
			if err != nil {
				log.Printf("Failed to remove finalizer from %q: %v", name, err)
			} else {
				item = *updated
			}
		}

		// Update Kubernetes Custom Resource Status
		statusMap := map[string]interface{}{
			"observedGeneration": res.ObservedGeneration,
			"state":              res.State.String(),
		}
		if res.Access != nil && len(res.Access.GetEnvVars()) > 0 {
			statusMap["access"] = map[string]interface{}{
				"envVars": res.Access.GetEnvVars(),
			}
		}
		if len(res.Conditions) > 0 {
			var conds []interface{}
			for _, c := range res.Conditions {
				condMap := map[string]interface{}{
					"type":               c.GetType(),
					"status":             c.GetStatus(),
					"observedGeneration": c.GetObservedGeneration(),
					"reason":             c.GetReason(),
					"message":            c.GetMessage(),
				}
				if c.GetLastTransitionTime() != nil {
					condMap["lastTransitionTime"] = c.GetLastTransitionTime().AsTime().Format(time.RFC3339)
				}
				conds = append(conds, condMap)
			}
			statusMap["conditions"] = conds
		}

		freshItem, err := dynamicClient.Resource(componentGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			freshItem.Object["status"] = statusMap
			_, err = dynamicClient.Resource(componentGVR).Namespace(namespace).UpdateStatus(ctx, freshItem, metav1.UpdateOptions{})
			if err != nil {
				log.Printf("Failed to update status for %q: %v", name, err)
			}
		}
	}
}

func parseComponentSpec(specObj interface{}) *componentspb.ComponentSpecProto {
	specMap, ok := specObj.(map[string]interface{})
	if !ok || specMap == nil {
		return &componentspb.ComponentSpecProto{Enabled: true}
	}

	proto := &componentspb.ComponentSpecProto{
		Enabled:    true,
		Parameters: make(map[string]string),
	}

	if en, ok := specMap["enabled"].(bool); ok {
		proto.Enabled = en
	}
	if ct, ok := specMap["componentType"].(string); ok {
		switch ct {
		case "COMPONENT_TYPE_CUSTOM_1P_COMPONENT":
			proto.ComponentType = componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT
		case "COMPONENT_TYPE_CUSTOM_3P_WRAPPER":
			proto.ComponentType = componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER
		}
	}
	if cc, ok := specMap["componentClass"].(string); ok {
		proto.ComponentClass = cc
	}
	if dp, ok := specMap["deletionProtection"].(bool); ok {
		proto.DeletionProtection = dp
	}
	if dpStr, ok := specMap["deletionPolicy"].(string); ok {
		if dpStr == "Orphan" || dpStr == "DELETION_POLICY_ORPHAN" {
			proto.DeletionPolicy = componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN
		} else {
			proto.DeletionPolicy = componentspb.DeletionPolicy_DELETION_POLICY_DELETE
		}
	}
	if it, ok := specMap["imageTag"].(string); ok {
		proto.ImageTag = it
	}
	if cv, ok := specMap["chartVersion"].(string); ok {
		proto.ChartVersion = cv
	}

	if deps, ok := specMap["dependencies"].([]interface{}); ok {
		for _, d := range deps {
			if dStr, ok := d.(string); ok {
				proto.Dependencies = append(proto.Dependencies, dStr)
			}
		}
	}

	if params, ok := specMap["parameters"].(map[string]interface{}); ok {
		for k, v := range params {
			proto.Parameters[k] = fmt.Sprintf("%v", v)
		}
	}

	if wr, ok := specMap["wrapperRef"].(map[string]interface{}); ok {
		ref := &componentspb.WrapperRefProto{}
		if g, ok := wr["group"].(string); ok {
			ref.Group = g
		}
		if v, ok := wr["version"].(string); ok {
			ref.Version = v
		}
		if k, ok := wr["kind"].(string); ok {
			ref.Kind = k
		}
		if n, ok := wr["name"].(string); ok {
			ref.Name = n
		}
		if ns, ok := wr["namespace"].(string); ok {
			ref.Namespace = ns
		}
		if ogp, ok := wr["observedGenerationPath"].(string); ok {
			ref.ObservedGenerationPath = ogp
		}
		if rcp, ok := wr["readyConditionPath"].(string); ok {
			ref.ReadyConditionPath = rcp
		}
		if erv, ok := wr["expectedReadyValue"].(string); ok {
			ref.ExpectedReadyValue = erv
		}
		proto.WrapperRef = ref
	}

	if lc, ok := specMap["lifecycleControl"].(map[string]interface{}); ok {
		proto.LifecycleControl = &componentspb.LifecycleControlProto{}
		if cascade, ok := lc["cascade"].(bool); ok {
			proto.LifecycleControl.Cascade = cascade
		}
		if drain, ok := lc["drainTimeoutSeconds"].(int64); ok {
			proto.LifecycleControl.DrainTimeoutSeconds = int32(drain)
		} else if drainF, ok := lc["drainTimeoutSeconds"].(float64); ok {
			proto.LifecycleControl.DrainTimeoutSeconds = int32(drainF)
		}
		if force, ok := lc["forceUpgrade"].(bool); ok {
			proto.LifecycleControl.ForceUpgrade = force
		}
	}

	return proto
}

func parseExistingConditions(statusObj interface{}) []*componentspb.ConditionProto {
	statusMap, ok := statusObj.(map[string]interface{})
	if !ok || statusMap == nil {
		return nil
	}
	condsList, ok := statusMap["conditions"].([]interface{})
	if !ok {
		return nil
	}
	var res []*componentspb.ConditionProto
	for _, cObj := range condsList {
		cMap, ok := cObj.(map[string]interface{})
		if !ok {
			continue
		}
		c := &componentspb.ConditionProto{}
		if t, ok := cMap["type"].(string); ok {
			c.Type = t
		}
		if s, ok := cMap["status"].(string); ok {
			c.Status = s
		}
		if r, ok := cMap["reason"].(string); ok {
			c.Reason = r
		}
		if m, ok := cMap["message"].(string); ok {
			c.Message = m
		}
		if ltt, ok := cMap["lastTransitionTime"].(string); ok {
			if t, err := time.Parse(time.RFC3339, ltt); err == nil {
				c.LastTransitionTime = timestamppb.New(t)
			}
		}
		res = append(res, c)
	}
	return res
}
