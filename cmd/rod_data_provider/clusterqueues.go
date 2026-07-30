// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	metav1 "golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/v1"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	spb "xmanager_cloud/api/protos/rod"
)

// FetchClusterQueues queries ClusterQueues directly from the in-memory KueueCache with zero network List calls.
func FetchClusterQueues(ctx context.Context, cache *KueueCache, req *spb.GetFittingQueuesRequest) ([]*spb.ClusterQueueInfo, error) {
	if cache == nil {
		return []*spb.ClusterQueueInfo{}, nil
	}

	cqList := cache.GetClusterQueues()
	lqList := cache.GetLocalQueues()
	rfMap := cache.GetResourceFlavors()

	_, _, cqToLqMap := buildLocalQueueMaps(lqList)

	var results []*spb.ClusterQueueInfo
	for _, cq := range cqList {
		cqInfo := buildClusterQueueInfoFromTyped(cq, cqToLqMap, rfMap)
		results = append(results, cqInfo)
	}

	filteredResults := filterFittingQueues(results, req)
	return filteredResults, nil
}

func buildLocalQueueMaps(lqList []*kueuev1beta1.LocalQueue) (map[string]string, map[string]string, map[string][]*spb.LocalQueueRef) {
	lqToCqMap := make(map[string]string)
	lqNameToCqMap := make(map[string]string)
	cqToLqMap := make(map[string][]*spb.LocalQueueRef)

	for _, lq := range lqList {
		cqRef := string(lq.Spec.ClusterQueue)
		if cqRef == "" {
			continue
		}
		lqName := lq.Name
		lqNamespace := lq.Namespace
		lqToCqMap[lqNamespace+"/"+lqName] = cqRef
		lqNameToCqMap[lqName] = cqRef
		cqToLqMap[cqRef] = append(cqToLqMap[cqRef], &spb.LocalQueueRef{
			Name:      lqName,
			Namespace: lqNamespace,
		})
	}
	return lqToCqMap, lqNameToCqMap, cqToLqMap
}

func buildClusterQueueInfoFromTyped(cq *kueuev1beta1.ClusterQueue, cqToLqMap map[string][]*spb.LocalQueueRef, rfMap map[string]*kueuev1beta1.ResourceFlavor) *spb.ClusterQueueInfo {
	name := cq.Name
	strategy, cohort := parseStrategyAndCohortTyped(&cq.Spec)
	statusStr, statusMsg, lastTransitionTime := parseClusterQueueStatusTyped(&cq.Status)

	localQueues := cqToLqMap[name]
	if localQueues == nil {
		localQueues = []*spb.LocalQueueRef{}
	}

	admittedCount := int64(cq.Status.AdmittedWorkloads)
	pendingCount := int64(cq.Status.PendingWorkloads)
	reservingCount := int64(cq.Status.ReservingWorkloads)

	requestedResources := "0"
	if pendingCount > 0 {
		requestedResources = fmt.Sprintf("%d workloads", pendingCount)
	}

	usageMap := extractResourceTotalsFromStatus(cq.Status.FlavorsUsage)
	resMap := extractResourceTotalsFromStatus(cq.Status.FlavorsReservation)
	flavors := parseClusterQueueFlavorsTyped(&cq.Spec, usageMap, resMap)

	dominantResource, domUnitName := determineDominantResource(flavors, rfMap)
	resourceVelocity := fmt.Sprintf("~0 %s/hr", domUnitName)
	jobVelocity := "0 jobs/hr"

	return &spb.ClusterQueueInfo{
		Name:                  name,
		Strategy:              strategy,
		Cohort:                cohort,
		Status:                statusStr,
		StatusMessage:         statusMsg,
		LastTransitionTime:    lastTransitionTime,
		AdmittedWorkloads:     admittedCount,
		ReservingWorkloads:    reservingCount,
		PendingWorkloads:      pendingCount,
		PendingJobsCount:      pendingCount,
		RequestedResources:    requestedResources,
		DominantResource:      dominantResource,
		DominantResourceUnits: 0,
		AdmittedJobsCount:     admittedCount,
		ResourceVelocity:      resourceVelocity,
		JobVelocity:           jobVelocity,
		LocalQueues:           localQueues,
		Flavors:               flavors,
	}
}

func parseStrategyAndCohortTyped(spec *kueuev1beta1.ClusterQueueSpec) (strategy, cohort string) {
	strategy = "BestEffortFIFO"
	cohort = "None"
	if spec == nil {
		return strategy, cohort
	}
	if spec.QueueingStrategy != "" {
		strategy = string(spec.QueueingStrategy)
	}
	if spec.Cohort != "" {
		cohort = string(spec.Cohort)
	}
	return strategy, cohort
}

func parseClusterQueueStatusTyped(status *kueuev1beta1.ClusterQueueStatus) (statusStr, statusMsg, lastTransitionTime string) {
	statusStr = "INACTIVE"
	if status == nil {
		return statusStr, "", ""
	}
	for _, c := range status.Conditions {
		if c.Type == kueuev1beta1.ClusterQueueActive {
			if c.Status == metav1.ConditionTrue {
				statusStr = "ACTIVE"
			}
			statusMsg = c.Message
			if !c.LastTransitionTime.IsZero() {
				lastTransitionTime = c.LastTransitionTime.Format(time.RFC3339)
			}
		}
	}
	return statusStr, statusMsg, lastTransitionTime
}

func extractResourceTotalsFromStatus(usage []kueuev1beta1.FlavorUsage) map[string]string {
	resTotals := make(map[string]string)
	for _, flv := range usage {
		for _, res := range flv.Resources {
			resName := string(res.Name)
			resTotals[resName] = res.Total.String()
		}
	}
	return resTotals
}

func parseClusterQueueFlavorsTyped(spec *kueuev1beta1.ClusterQueueSpec, usageMap, resMap map[string]string) []*spb.ResourceFlavorMetric {
	var flavors []*spb.ResourceFlavorMetric
	if spec == nil {
		return flavors
	}
	for _, rg := range spec.ResourceGroups {
		for _, f := range rg.Flavors {
			flavorName := string(f.Name)
			for _, res := range f.Resources {
				resName := string(res.Name)
				nominal := res.NominalQuota.String()
				inUse := defaultString(usageMap[resName], "0")
				reserved := defaultString(resMap[resName], "0")
				freeCap := computeFreeCapacityStr(nominal, reserved, inUse)
				largestSlot := "-"
				if nominal != "" && nominal != "0" && nominal != "-" {
					largestSlot = fmt.Sprintf("%s -> %s", freeCap, nominal)
				}
				flavors = append(flavors, &spb.ResourceFlavorMetric{
					Name:         flavorName,
					ResourceName: resName,
					NominalQuota: nominal,
					InUse:        inUse,
					Reserved:     reserved,
					FreeCapacity: freeCap,
					LargestSlot:  largestSlot,
				})
			}
		}
	}
	return flavors
}

func defaultString(val, fallback string) string {
	if val == "" {
		return fallback
	}
	return val
}

func determineDominantResource(flavorMetrics []*spb.ResourceFlavorMetric, flavorMap map[string]*kueuev1beta1.ResourceFlavor) (dominantResource string, domUnitName string) {
	dominantResource = "CPU"
	domUnitName = "CPUs"
	for _, f := range flavorMetrics {
		if rf, ok := flavorMap[f.Name]; ok && rf != nil {
			for k, v := range rf.Spec.NodeLabels {
				vLower := strings.ToLower(v)
				if strings.Contains(vLower, "h100") || strings.Contains(k, "h100") {
					return "GPU H100", "GPUs"
				}
				if strings.Contains(vLower, "a100") {
					return "GPU A100", "GPUs"
				}
				if strings.Contains(vLower, "l4") {
					return "GPU L4", "GPUs"
				}
				if strings.Contains(vLower, "tpu-v5") || strings.Contains(vLower, "v5e") || strings.Contains(vLower, "v5p") {
					return "TPU", "TPUs"
				}
			}
		}
		rLower := strings.ToLower(f.ResourceName)
		if strings.Contains(rLower, "gpu") || strings.Contains(rLower, "nvidia") {
			return "GPU H100", "GPUs"
		}
		if strings.Contains(rLower, "tpu") || strings.Contains(rLower, "google.com/tpu") {
			return "TPU", "TPUs"
		}
	}
	return dominantResource, domUnitName
}

func parseK8sResourceVal(s string) (float64, string) {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0, ""
	}
	unit := ""
	for _, u := range []string{"Gi", "Mi", "Ki", "m", "M", "k", "G"} {
		if strings.HasSuffix(s, u) {
			unit = u
			break
		}
	}
	numStr := strings.TrimSuffix(s, unit)
	v, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, unit
	}
	if unit == "m" {
		return v / 1000.0, unit
	}
	if unit == "Mi" {
		return v / 1024.0, unit
	}
	return v, unit
}

func computeFreeCapacityStr(nominalStr, reservedStr, inUseStr string) string {
	if nominalStr == "" || nominalStr == "-" {
		return "0"
	}
	nom, unit := parseK8sResourceVal(nominalStr)
	res, _ := parseK8sResourceVal(reservedStr)
	use, _ := parseK8sResourceVal(inUseStr)

	used := res
	if use > used {
		used = use
	}

	free := nom - used
	if free <= 0 {
		return "0"
	}
	if used == 0 {
		return nominalStr
	}

	if unit == "m" {
		return fmt.Sprintf("%.0fm", free*1000.0)
	}
	if unit == "Gi" || unit == "Mi" {
		return fmt.Sprintf("%.0f%s", free, unit)
	}
	if unit != "" {
		return fmt.Sprintf("%.0f%s", free, unit)
	}
	return fmt.Sprintf("%.0f", free)
}

func filterFittingQueues(queues []*spb.ClusterQueueInfo, req *spb.GetFittingQueuesRequest) []*spb.ClusterQueueInfo {
	if req == nil {
		return queues
	}
	var filtered []*spb.ClusterQueueInfo
	for _, q := range queues {
		if !matchesQueuePattern(q.Name, req.QueueNamePattern) {
			continue
		}
		if !satisfiesResourceRequirements(q, req.ResourceRequirements) {
			continue
		}
		if !satisfiesMinAvailable(q, req.MinAvailable) {
			continue
		}
		filtered = append(filtered, q)
	}
	return filtered
}

func matchesQueuePattern(name, pattern string) bool {
	if pattern == "" {
		return true
	}
	return strings.Contains(strings.ToLower(name), strings.ToLower(pattern))
}

func satisfiesResourceRequirements(q *spb.ClusterQueueInfo, reqs []*spb.ResourceRequirement) bool {
	if len(reqs) == 0 {
		return true
	}
	for _, req := range reqs {
		if !flavorSatisfiesRequirement(q.Flavors, req) {
			return false
		}
	}
	return true
}

func flavorSatisfiesRequirement(flavors []*spb.ResourceFlavorMetric, req *spb.ResourceRequirement) bool {
	reqVal, _ := parseK8sResourceVal(req.Quantity)
	for _, f := range flavors {
		if strings.EqualFold(f.ResourceName, req.ResourceName) {
			freeVal, _ := parseK8sResourceVal(f.FreeCapacity)
			if freeVal >= reqVal {
				return true
			}
		}
	}
	return false
}

func satisfiesMinAvailable(q *spb.ClusterQueueInfo, minAvailable int32) bool {
	if minAvailable <= 0 {
		return true
	}
	for _, f := range q.Flavors {
		freeVal, _ := parseK8sResourceVal(f.FreeCapacity)
		if freeVal >= float64(minAvailable) {
			return true
		}
	}
	return false
}
