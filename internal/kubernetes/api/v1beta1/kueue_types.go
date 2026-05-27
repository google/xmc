/*
Copyright The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1beta1

import (
	"strings"

	corev1 "golang/k8s_io/api/v/v0_23/core/v1/v1"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/api/resource/resource"
	metav1 "golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/v1"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/runtime/runtime"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/runtime/schema/schema"
	"golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/scheme/scheme"
)

var (
	// GroupVersion is group version used to register these objects
	GroupVersion = schema.GroupVersion{Group: "kueue.x-k8s.io", Version: "v1beta1"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

func init() {
	SchemeBuilder.Register(&Workload{}, &WorkloadList{})
	SchemeBuilder.Register(&ResourceFlavor{}, &ResourceFlavorList{})
	SchemeBuilder.Register(&MultiKueueCluster{}, &MultiKueueClusterList{})
	SchemeBuilder.Register(&AdmissionCheck{}, &AdmissionCheckList{})
	SchemeBuilder.Register(&ClusterQueue{}, &ClusterQueueList{})
	SchemeBuilder.Register(&LocalQueue{}, &LocalQueueList{})
	SchemeBuilder.Register(&MultiKueueConfig{}, &MultiKueueConfigList{})
}

type CheckState string

const (
	// CheckStateRetry means that the check cannot pass at this moment, back off (possibly
	// allowing other to try, unblock quota) and retry.
	// A workload having at least one check in this state will be evicted if admitted and
	// will not be considered for admission while the check is in this state.
	CheckStateRetry CheckState = "Retry"

	// CheckStateRejected means that the check will not pass in the near future. It is not worth
	// to retry.
	// A workload having at least one check in this state will be evicted if admitted and deactivated.
	CheckStateRejected CheckState = "Rejected"

	// CheckStatePending means that the check still hasn't been performed and the state can be
	// 1. Unknown, the condition was added by kueue and its controller was not able to evaluate it.
	// 2. Set by its controller and reevaluated after quota is reserved.
	CheckStatePending CheckState = "Pending"

	// CheckStateReady means that the check has passed.
	// A workload having all its checks ready, and quota reserved can begin execution.
	CheckStateReady CheckState = "Ready"
)

// AdmissionCheckSpec defines the desired state of AdmissionCheck
type AdmissionCheckSpec struct {
	// controllerName identifies the controller that processes the AdmissionCheck,
	// not necessarily a Kubernetes Pod or Deployment name. Cannot be empty.
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="field is immutable"
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:MinLength=1
	ControllerName string `json:"controllerName"`

	// parameters identifies a configuration with additional parameters for the
	// check.
	// +optional
	Parameters *AdmissionCheckParametersReference `json:"parameters,omitempty"`
}

type AdmissionCheckParametersReference struct {
	// apiGroup is the group for the resource being referenced.
	// +required
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern="^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$"
	APIGroup string `json:"apiGroup,omitempty"`
	// kind is the type of the resource being referenced.
	// +required
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern="^(?i)[a-z]([-a-z0-9]*[a-z0-9])?$"
	Kind string `json:"kind,omitempty"`
	// name is the name of the resource being referenced.
	// +required
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern="^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"
	Name string `json:"name,omitempty"`
}

// AdmissionCheckStatus defines the observed state of AdmissionCheck
type AdmissionCheckStatus struct {
	// conditions hold the latest available observations of the AdmissionCheck
	// current state.
	// This is limited to at most 16 separate conditions.
	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	// +kubebuilder:validation:MaxItems=16
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

const (
	// AdmissionCheckActive indicates that the controller of the admission check is
	// ready to evaluate the checks states
	AdmissionCheckActive string = "Active"
)

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// AdmissionCheck is the Schema for the admissionchecks API
type AdmissionCheck struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is the metadata of the AdmissionCheck.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec is the specification of the AdmissionCheck.
	// +optional
	Spec AdmissionCheckSpec `json:"spec"`

	// status is the status of the AdmissionCheck.
	// +optional
	Status AdmissionCheckStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AdmissionCheckList contains a list of AdmissionCheck
type AdmissionCheckList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AdmissionCheck `json:"items"`
}

const (
	ResourceInUseFinalizerName                 = "kueue.x-k8s.io/resource-in-use"
	DefaultPodSetName          PodSetReference = "main"

	// ElasticJobSchedulingGate is the name of the scheduling gate applied to Pods
	// to delay their scheduling until the associated workload slice has been admitted.
	// This gate ensures that Pods do not begin scheduling prematurely, maintaining
	// proper sequencing in workload processing.
	ElasticJobSchedulingGate = "kueue.x-k8s.io/elastic-job"
)

type StopPolicy string

const (
	None         StopPolicy = "None"
	HoldAndDrain StopPolicy = "HoldAndDrain"
	Hold         StopPolicy = "Hold"
)

// ClusterQueue Active condition reasons.
const (
	ClusterQueueActiveReasonTerminating                              = "Terminating"
	ClusterQueueActiveReasonStopped                                  = "Stopped"
	ClusterQueueActiveReasonFlavorNotFound                           = "FlavorNotFound"
	ClusterQueueActiveReasonAdmissionCheckNotFound                   = "AdmissionCheckNotFound"
	ClusterQueueActiveReasonAdmissionCheckInactive                   = "AdmissionCheckInactive"
	ClusterQueueActiveReasonMultipleMultiKueueAdmissionChecks        = "MultipleMultiKueueAdmissionChecks"
	ClusterQueueActiveReasonMultiKueueAdmissionCheckAppliedPerFlavor = "MultiKueueAdmissionCheckAppliedPerFlavor"
	ClusterQueueActiveReasonTopologyNotFound                         = "TopologyNotFound"
	ClusterQueueActiveReasonUnknown                                  = "Unknown"
	ClusterQueueActiveReasonReady                                    = "Ready"
)

// FairSharing contains the properties of the ClusterQueue or Cohort,
// when participating in FairSharing.
//
// Fair Sharing is compatible with Hierarchical Cohorts (any Cohort
// which has a parent) as of v0.11. Using these features together in
// V0.9 and V0.10 is unsupported, and results in undefined behavior.
type FairSharing struct {
	// weight gives a comparative advantage to this ClusterQueue
	// or Cohort when competing for unused resources in the
	// Cohort.  The share is based on the dominant resource usage
	// above nominal quotas for each resource, divided by the
	// weight.  Admission prioritizes scheduling workloads from
	// ClusterQueues and Cohorts with the lowest share and
	// preempting workloads from the ClusterQueues and Cohorts
	// with the highest share.  A zero weight implies infinite
	// share value, meaning that this Node will always be at
	// disadvantage against other ClusterQueues and Cohorts.
	// When not 0, Weight must be greater than 10^-9.
	// +kubebuilder:default=1
	// +optional
	Weight *resource.Quantity `json:"weight,omitempty"`
}

// FairSharingStatus contains the information about the current status of Fair Sharing.
type FairSharingStatus struct {
	// weightedShare represents the maximum of the ratios of usage
	// above nominal quota to the lendable resources in the
	// Cohort, among all the resources provided by the Node, and
	// divided by the weight.  If zero, it means that the usage of
	// the Node is below the nominal quota.  If the Node has a
	// weight of zero and is borrowing, this will return
	// 9223372036854775807, the maximum possible share value.
	// +required
	WeightedShare int64 `json:"weightedShare"`
}

type AdmissionScope struct {
	// admissionMode indicates which mode for AdmissionFairSharing should be used
	// in the AdmissionScope. Possible values are:
	// - UsageBasedAdmissionFairSharing
	// - NoAdmissionFairSharing
	//
	// +kubebuilder:validation:Enum=UsageBasedAdmissionFairSharing;NoAdmissionFairSharing
	// +required
	AdmissionMode AdmissionMode `json:"admissionMode,omitempty"`
}

type AdmissionMode string

const (
	// AdmissionFairSharing based on usage, with QueuingStrategy as defined in CQ.
	UsageBasedAdmissionFairSharing AdmissionMode = "UsageBasedAdmissionFairSharing"

	// AdmissionFairSharing is disabled for this CQ
	NoAdmissionFairSharing AdmissionMode = "NoAdmissionFairSharing"
)

// ClusterQueueReference is the name of the ClusterQueue.
// It must be a DNS (RFC 1123) and has the maximum length of 253 characters.
//
// +kubebuilder:validation:MaxLength=253
// +kubebuilder:validation:Pattern="^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$"
type ClusterQueueReference string

// CohortReference is the name of the Cohort.
//
// Validation of a cohort name is equivalent to that of object names:
// subdomain in DNS (RFC 1123).
// +kubebuilder:validation:MaxLength=253
// +kubebuilder:validation:Pattern="^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$"
type CohortReference string

// ClusterQueueSpec defines the desired state of ClusterQueue
// +kubebuilder:validation:XValidation:rule="!has(self.cohortName) && has(self.resourceGroups) ? self.resourceGroups.all(rg, rg.flavors.all(f, f.resources.all(r, !has(r.borrowingLimit)))) : true", message="borrowingLimit must be nil when cohort is empty"
type ClusterQueueSpec struct {
	// resourceGroups describes groups of resources.
	// Each resource group defines the list of resources and a list of flavors
	// that provide quotas for these resources.
	// Each resource and each flavor can only form part of one resource group.
	// resourceGroups can be up to 16, with a max of 256 total flavors across all groups.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=16
	// +optional
	ResourceGroups []ResourceGroup `json:"resourceGroups,omitempty"`

	// cohortName that this ClusterQueue belongs to. CQs that belong to the
	// same cohort can borrow unused resources from each other.
	//
	// A CQ can be a member of a single borrowing cohort. A workload submitted
	// to a queue referencing this CQ can borrow quota from any CQ in the cohort.
	// Only quota for the [resource, flavor] pairs listed in the CQ can be
	// borrowed.
	// If empty, this ClusterQueue cannot borrow from any other ClusterQueue and
	// vice versa.
	//
	// A cohort is a name that links CQs together, but it doesn't reference any
	// object.
	// +optional
	CohortName CohortReference `json:"cohortName,omitempty"`

	// queueingStrategy indicates the queueing strategy of the workloads
	// across the queues in this ClusterQueue.
	// Current Supported Strategies:
	//
	// - StrictFIFO: workloads are ordered strictly by creation time.
	// Older workloads that can't be admitted will block admitting newer
	// workloads even if they fit available quota.
	// - BestEffortFIFO: workloads are ordered by creation time,
	// however older workloads that can't be admitted will not block
	// admitting newer workloads that fit existing quota.
	//
	// +optional
	// +kubebuilder:default=BestEffortFIFO
	// +kubebuilder:validation:Enum=StrictFIFO;BestEffortFIFO
	QueueingStrategy QueueingStrategy `json:"queueingStrategy,omitempty"`

	// namespaceSelector defines which namespaces are allowed to submit workloads to
	// this clusterQueue. Beyond this basic support for policy, a policy agent like
	// Gatekeeper should be used to enforce more advanced policies.
	// Defaults to null which is a nothing selector (no namespaces eligible).
	// If set to an empty selector `{}`, then all namespaces are eligible.
	// +optional
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`

	// flavorFungibility defines whether a workload should try the next flavor
	// before borrowing or preempting in the flavor being evaluated.
	// +optional
	// +kubebuilder:default={}
	FlavorFungibility *FlavorFungibility `json:"flavorFungibility,omitempty"`

	// preemption defines the preemption policies.
	// +kubebuilder:default={}
	// +optional
	Preemption *ClusterQueuePreemption `json:"preemption,omitempty"`

	// admissionChecksStrategy defines a list of strategies to determine which ResourceFlavors require AdmissionChecks.
	// +optional
	AdmissionChecksStrategy *AdmissionChecksStrategy `json:"admissionChecksStrategy,omitempty"`

	// stopPolicy - if set to a value different from None, the ClusterQueue is considered Inactive, no new reservation being
	// made.
	//
	// Depending on its value, its associated workloads will:
	//
	// - None - Workloads are admitted
	// - HoldAndDrain - Admitted workloads are evicted and Reserving workloads will cancel the reservation.
	// - Hold - Admitted workloads will run to completion and Reserving workloads will cancel the reservation.
	//
	// +kubebuilder:validation:Enum=None;Hold;HoldAndDrain
	// +kubebuilder:default="None"
	// +optional
	StopPolicy *StopPolicy `json:"stopPolicy,omitempty"`

	// fairSharing defines the properties of the ClusterQueue when
	// participating in FairSharing.  The values are only relevant
	// if FairSharing is enabled in the Kueue configuration.
	// +optional
	FairSharing *FairSharing `json:"fairSharing,omitempty"`

	// admissionScope indicates whether ClusterQueue uses the Admission Fair Sharing
	// +optional
	AdmissionScope *AdmissionScope `json:"admissionScope,omitempty"`
}

// AdmissionChecksStrategy defines a strategy for a AdmissionCheck.
type AdmissionChecksStrategy struct {
	// admissionChecks is a list of strategies for AdmissionChecks
	// +listType=map
	// +listMapKey=name
	// +required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	AdmissionChecks []AdmissionCheckStrategyRule `json:"admissionChecks,omitempty"`
}

// AdmissionCheckStrategyRule defines rules for a single AdmissionCheck
type AdmissionCheckStrategyRule struct {
	// name is an AdmissionCheck's name.
	// +required
	Name AdmissionCheckReference `json:"name,omitempty"`

	// onFlavors is a list of ResourceFlavors' names that this AdmissionCheck should run for.
	// If empty, the AdmissionCheck will run for all workloads submitted to the ClusterQueue.
	// +optional
	// +listType=set
	// +kubebuilder:validation:MaxItems=64
	OnFlavors []ResourceFlavorReference `json:"onFlavors,omitempty"`
}

type QueueingStrategy string

const (
	// StrictFIFO means that workloads of the same priority are ordered strictly by creation time.
	// Older workloads that can't be admitted will block admitting newer
	// workloads even if they fit available quota.
	StrictFIFO QueueingStrategy = "StrictFIFO"

	// BestEffortFIFO means that workloads of the same priority are ordered by creation time,
	// however older workloads that can't be admitted will not block
	// admitting newer workloads that fit existing quota.
	BestEffortFIFO QueueingStrategy = "BestEffortFIFO"
)

// +kubebuilder:validation:XValidation:rule="self.flavors.all(x, size(x.resources) == size(self.coveredResources))", message="flavors must have the same number of resources as the coveredResources"
type ResourceGroup struct {
	// coveredResources is the list of resources covered by the flavors in this
	// group.
	// Examples: cpu, memory, vendor.com/gpu.
	// The list cannot be empty and it can contain up to 64 resources. With a total
	// of up to 256 covered resources across all resource groups in the ClusterQueue.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +listType=atomic
	// +required
	CoveredResources []corev1.ResourceName `json:"coveredResources,omitempty"`

	// flavors is the list of flavors that provide the resources of this group.
	// Typically, different flavors represent different hardware models
	// (e.g., gpu models, cpu architectures) or pricing models (on-demand vs spot
	// cpus).
	// Each flavor MUST list all the resources listed for this group in the same
	// order as the .resources field.
	// The list cannot be empty and it can contain up to 64 flavors, with a max of
	// 256 total flavors across all resource groups in the ClusterQueue.
	// +listType=map
	// +required
	// +listMapKey=name
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	Flavors []FlavorQuotas `json:"flavors,omitempty"`
}

type FlavorQuotas struct {
	// name of this flavor. The name should match the .metadata.name of a
	// ResourceFlavor. If a matching ResourceFlavor does not exist, the
	// ClusterQueue will have an Active condition set to False.
	// +required
	Name ResourceFlavorReference `json:"name"`

	// resources is the list of quotas for this flavor per resource.
	// There could be up to 64 resources.
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +required
	Resources []ResourceQuota `json:"resources,omitempty"`
}

type ResourceQuota struct {
	// name of this resource.
	// +required
	Name corev1.ResourceName `json:"name,omitempty"`

	// nominalQuota is the quantity of this resource that is available for
	// Workloads admitted by this ClusterQueue at a point in time.
	// The nominalQuota must be non-negative.
	// nominalQuota should represent the resources in the cluster available for
	// running jobs (after discounting resources consumed by system components
	// and pods not managed by kueue). In an autoscaled cluster, nominalQuota
	// should account for resources that can be provided by a component such as
	// Kubernetes cluster-autoscaler.
	//
	// If the ClusterQueue belongs to a cohort, the sum of the quotas for each
	// (flavor, resource) combination defines the maximum quantity that can be
	// allocated by a ClusterQueue in the cohort.
	// +required
	NominalQuota resource.Quantity `json:"nominalQuota,omitempty"`

	// borrowingLimit is the maximum amount of quota for the [flavor, resource]
	// combination that this ClusterQueue is allowed to borrow from the unused
	// quota of other ClusterQueues in the same cohort.
	// In total, at a given time, Workloads in a ClusterQueue can consume a
	// quantity of quota equal to nominalQuota+borrowingLimit, assuming the other
	// ClusterQueues in the cohort have enough unused quota.
	// If null, it means that there is no borrowing limit.
	// If not null, it must be non-negative.
	// borrowingLimit must be null if spec.cohort is empty.
	// +optional
	BorrowingLimit *resource.Quantity `json:"borrowingLimit,omitempty"`

	// lendingLimit is the maximum amount of unused quota for the [flavor, resource]
	// combination that this ClusterQueue can lend to other ClusterQueues in the same cohort.
	// In total, at a given time, ClusterQueue reserves for its exclusive use
	// a quantity of quota equals to nominalQuota - lendingLimit.
	// If null, it means that there is no lending limit, meaning that
	// all the nominalQuota can be borrowed by other clusterQueues in the cohort.
	// If not null, it must be non-negative.
	// lendingLimit must be null if spec.cohort is empty.
	// This field is in beta stage and is enabled by default.
	// +optional
	LendingLimit *resource.Quantity `json:"lendingLimit,omitempty"`
}

// ResourceFlavorReference is the name of the ResourceFlavor.
// +kubebuilder:validation:MaxLength=253
// +kubebuilder:validation:Pattern="^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$"
type ResourceFlavorReference string

// ClusterQueueStatus defines the observed state of ClusterQueue
type ClusterQueueStatus struct {
	// conditions hold the latest available observations of the ClusterQueue
	// current state.
	// conditions are limited to 16 elements.
	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	// +kubebuilder:validation:MaxItems=16
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
	// flavorsReservation are the reserved quotas, by flavor, currently in use by the
	// workloads assigned to this ClusterQueue.
	// flavorsReservation are limited to 64 elements.
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=64
	// +optional
	FlavorsReservation []FlavorUsage `json:"flavorsReservation,omitempty"`

	// flavorsUsage are the used quotas, by flavor, currently in use by the
	// workloads admitted in this ClusterQueue.
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=64
	// +optional
	FlavorsUsage []FlavorUsage `json:"flavorsUsage,omitempty"`

	// pendingWorkloads is the number of workloads currently waiting to be
	// admitted to this clusterQueue.
	// +optional
	PendingWorkloads int32 `json:"pendingWorkloads"`

	// reservingWorkloads is the number of workloads currently reserving quota in this
	// clusterQueue.
	// +optional
	ReservingWorkloads int32 `json:"reservingWorkloads"`

	// admittedWorkloads is the number of workloads currently admitted to this
	// clusterQueue and haven't finished yet.
	// +optional
	AdmittedWorkloads int32 `json:"admittedWorkloads"`

	// fairSharing contains the current state for this ClusterQueue
	// when participating in Fair Sharing.
	// This is recorded only when Fair Sharing is enabled in the Kueue configuration.
	// +optional
	FairSharing *FairSharingStatus `json:"fairSharing,omitempty"`
}

type FlavorUsage struct {
	// name of the flavor.
	// +required
	Name ResourceFlavorReference `json:"name,omitempty"`

	// resources lists the quota usage for the resources in this flavor.
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=64
	// +required
	Resources []ResourceUsage `json:"resources,omitempty"`
}

type ResourceUsage struct {
	// name of the resource
	// +required
	Name corev1.ResourceName `json:"name,omitempty"`

	// total is the total quantity of used quota, including the amount borrowed
	// from the cohort.
	// +optional
	Total resource.Quantity `json:"total,omitempty"`

	// borrowed is quantity of quota that is borrowed from the cohort. In other
	// words, it's the used quota that is over the nominalQuota.
	// +optional
	Borrowed resource.Quantity `json:"borrowed,omitempty"`
}

const (
	// ClusterQueueActive indicates that the ClusterQueue can admit new workloads and its quota
	// can be borrowed by other ClusterQueues in the same cohort.
	ClusterQueueActive string = "Active"
)

type PreemptionPolicy string

const (
	PreemptionPolicyNever                     PreemptionPolicy = "Never"
	PreemptionPolicyAny                       PreemptionPolicy = "Any"
	PreemptionPolicyLowerPriority             PreemptionPolicy = "LowerPriority"
	PreemptionPolicyLowerOrNewerEqualPriority PreemptionPolicy = "LowerOrNewerEqualPriority"
)

type FlavorFungibilityPolicy string

const (
	MayStopSearch FlavorFungibilityPolicy = "MayStopSearch"
	TryNextFlavor FlavorFungibilityPolicy = "TryNextFlavor"
)

type FlavorFungibilityPreference string

const (
	BorrowingOverPreemption FlavorFungibilityPreference = "BorrowingOverPreemption"
	PreemptionOverBorrowing FlavorFungibilityPreference = "PreemptionOverBorrowing"
)

// FlavorFungibility determines whether a workload should try the next flavor
// before borrowing or preempting in current flavor.
// +kubebuilder:validation:XValidation:rule="!has(self.preference) || (self.whenCanBorrow == 'TryNextFlavor' && self.whenCanPreempt == 'TryNextFlavor')",message="preference can only be set when both whenCanBorrow and whenCanPreempt are TryNextFlavor"
type FlavorFungibility struct {
	// whenCanBorrow determines whether a workload should try the next flavor
	// before borrowing in current flavor. The possible values are:
	//
	// - `MayStopSearch` (default): stop the search for candidate flavors if workload
	//   fits or requires borrowing to fit.
	// - `TryNextFlavor`: try next flavor if workload requires borrowing to fit.
	//
	// +kubebuilder:validation:Enum={MayStopSearch,TryNextFlavor}
	// +kubebuilder:default="MayStopSearch"
	// +optional
	WhenCanBorrow FlavorFungibilityPolicy `json:"whenCanBorrow,omitempty"`
	// whenCanPreempt determines whether a workload should try the next flavor
	// before borrowing in current flavor. The possible values are:
	//
	// - `MayStopSearch`: stop the search for candidate flavors if workload fits or requires
	//   preemption to fit.
	// - `TryNextFlavor` (default): try next flavor if workload requires preemption
	//   to fit in current flavor.
	//
	// +kubebuilder:validation:Enum={MayStopSearch,TryNextFlavor}
	// +kubebuilder:default="TryNextFlavor"
	// +optional
	WhenCanPreempt FlavorFungibilityPolicy `json:"whenCanPreempt,omitempty"`
	// preference guides the choosing of the flavor for admission in case all candidate flavors
	// require either preemption, borrowing, or both. The possible values are:
	// - `BorrowingOverPreemption` (default): prefer to use borrowing rather than preemption
	// when such a choice is possible. More technically it minimizes the borrowing distance
	// in the cohort tree, and solves tie-breaks by preferring better preemption mode
	// (reclaim over preemption within ClusterQueue).
	// - `PreemptionOverBorrowing`: prefer to use preemption rather than borrowing
	// when such a choice is possible.  More technically it optimizes the preemption mode
	// (reclaim over preemption within ClusterQueue), and solves tie-breaks by minimizing
	// the borrowing distance in the cohort tree.
	//
	// +kubebuilder:validation:Enum={BorrowingOverPreemption,PreemptionOverBorrowing}
	// +optional
	Preference *FlavorFungibilityPreference `json:"preference,omitempty"`
}

// ClusterQueuePreemption contains policies to preempt Workloads from this
// ClusterQueue or the ClusterQueue's cohort.
//
// Preemption may be configured to work in the following scenarios:
//
//   - When a Workload fits within the nominal quota of the ClusterQueue, but
//     the quota is currently borrowed by other ClusterQueues in the cohort.
//     We preempt workloads in other ClusterQueues to allow this ClusterQueue to
//     reclaim its nominal quota. Configured using reclaimWithinCohort.
//   - When a Workload doesn't fit within the nominal quota of the ClusterQueue
//     and there are admitted Workloads in the ClusterQueue with lower priority.
//     Configured using withinClusterQueue.
//   - When a Workload may fit while both borrowing and preempting
//     low priority workloads in the Cohort. Configured using borrowWithinCohort.
//   - When FairSharing is enabled, to maintain fair distribution of
//     unused resources. See FairSharing documentation.
//
// The preemption algorithm tries to find a minimal set of Workloads to
// preempt to accomomdate the pending Workload, preempting Workloads with
// lower priority first.
// +kubebuilder:validation:XValidation:rule="!(self.reclaimWithinCohort == 'Never' && has(self.borrowWithinCohort) &&  self.borrowWithinCohort.policy != 'Never')", message="reclaimWithinCohort=Never and borrowWithinCohort.Policy!=Never"
type ClusterQueuePreemption struct {
	// reclaimWithinCohort determines whether a pending Workload can preempt
	// Workloads from other ClusterQueues in the cohort that are using more than
	// their nominal quota. The possible values are:
	//
	// - `Never` (default): do not preempt Workloads in the cohort.
	// - `LowerPriority`: **Classic Preemption** if the pending Workload
	//   fits within the nominal quota of its ClusterQueue, only preempt
	//   Workloads in the cohort that have lower priority than the pending
	//   Workload. **Fair Sharing** only preempt Workloads in the cohort that
	//   have lower priority than the pending Workload and that satisfy the
	//   Fair Sharing preemptionStategies.
	// - `Any`: **Classic Preemption** if the pending Workload fits within
	//    the nominal quota of its ClusterQueue, preempt any Workload in the
	//    cohort, irrespective of priority. **Fair Sharing** preempt Workloads
	//    in the cohort that satisfy the Fair Sharing preemptionStrategies.
	//
	// +optional
	// +kubebuilder:default=Never
	// +kubebuilder:validation:Enum=Never;LowerPriority;Any
	ReclaimWithinCohort PreemptionPolicy `json:"reclaimWithinCohort,omitempty"`

	// borrowWithinCohort determines whether a pending Workload can preempt
	// Workloads from other ClusterQueues in the cohort if the workload requires borrowing.
	// May only be configured with Classical Preemption, and __not__ with Fair Sharing.
	// +kubebuilder:default={}
	// +optional
	BorrowWithinCohort *BorrowWithinCohort `json:"borrowWithinCohort,omitempty"`

	// withinClusterQueue determines whether a pending Workload that doesn't fit
	// within the nominal quota for its ClusterQueue, can preempt active Workloads in
	// the ClusterQueue. The possible values are:
	//
	// - `Never` (default): do not preempt Workloads in the ClusterQueue.
	// - `LowerPriority`: only preempt Workloads in the ClusterQueue that have
	//   lower priority than the pending Workload.
	// - `LowerOrNewerEqualPriority`: only preempt Workloads in the ClusterQueue that
	//   either have a lower priority than the pending workload or equal priority
	//   and are newer than the pending workload.
	//
	// +kubebuilder:default=Never
	// +kubebuilder:validation:Enum=Never;LowerPriority;LowerOrNewerEqualPriority
	// +optional
	WithinClusterQueue PreemptionPolicy `json:"withinClusterQueue,omitempty"`
}

type BorrowWithinCohortPolicy string

const (
	BorrowWithinCohortPolicyNever         BorrowWithinCohortPolicy = "Never"
	BorrowWithinCohortPolicyLowerPriority BorrowWithinCohortPolicy = "LowerPriority"
)

// BorrowWithinCohort contains configuration which allows to preempt workloads
// within cohort while borrowing. It only works with Classical Preemption,
// __not__ with Fair Sharing.
type BorrowWithinCohort struct {
	// policy determines the policy for preemption to reclaim quota within cohort while borrowing.
	// Possible values are:
	// - `Never` (default): do not allow for preemption, in other
	//    ClusterQueues within the cohort, for a borrowing workload.
	// - `LowerPriority`: allow preemption, in other ClusterQueues
	//    within the cohort, for a borrowing workload, but only if
	//    the preempted workloads are of lower priority.
	//
	// +kubebuilder:default=Never
	// +kubebuilder:validation:Enum=Never;LowerPriority
	// +optional
	Policy BorrowWithinCohortPolicy `json:"policy,omitempty"`
	// maxPriorityThreshold allows to restrict the set of workloads which
	// might be preempted by a borrowing workload, to only workloads with
	// priority less than or equal to the specified threshold priority.
	// When the threshold is not specified, then any workload satisfying the
	// policy can be preempted by the borrowing workload.
	//
	// +optional
	MaxPriorityThreshold *int32 `json:"maxPriorityThreshold,omitempty"`
}

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName={cq}
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Cohort",JSONPath=".spec.cohort",type=string,description="Cohort that this ClusterQueue belongs to"
// +kubebuilder:printcolumn:name="Strategy",JSONPath=".spec.queueingStrategy",type=string,description="The queueing strategy used to prioritize workloads",priority=1
// +kubebuilder:printcolumn:name="Pending Workloads",JSONPath=".status.pendingWorkloads",type=integer,description="Number of pending workloads"
// +kubebuilder:printcolumn:name="Admitted Workloads",JSONPath=".status.admittedWorkloads",type=integer,description="Number of admitted workloads that haven't finished yet",priority=1

// ClusterQueue is the Schema for the clusterQueue API.
type ClusterQueue struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is the metadata of the ClusterQueue.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec is the specification of the ClusterQueue.
	// +optional
	Spec ClusterQueueSpec `json:"spec,omitempty"`
	// status is the status of the ClusterQueue.
	// +optional
	Status ClusterQueueStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterQueueList contains a list of ClusterQueue
type ClusterQueueList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterQueue `json:"items"`
}

// LocalQueueName is the name of the LocalQueue.
// It must be a DNS (RFC 1123) and has the maximum length of 253 characters.
//
// +kubebuilder:validation:MaxLength=253
// +kubebuilder:validation:Pattern="^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$"
type LocalQueueName string

// LocalQueueSpec defines the desired state of LocalQueue
type LocalQueueSpec struct {
	// clusterQueue is a reference to a clusterQueue that backs this localQueue.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="field is immutable"
	// +optional
	ClusterQueue ClusterQueueReference `json:"clusterQueue,omitempty"`

	// stopPolicy - if set to a value different from None, the LocalQueue is considered Inactive,
	// no new reservation being made.
	//
	// Depending on its value, its associated workloads will:
	//
	// - None - Workloads are admitted
	// - HoldAndDrain - Admitted workloads are evicted and Reserving workloads will cancel the reservation.
	// - Hold - Admitted workloads will run to completion and Reserving workloads will cancel the reservation.
	//
	// +optional
	// +kubebuilder:validation:Enum=None;Hold;HoldAndDrain
	// +kubebuilder:default="None"
	StopPolicy *StopPolicy `json:"stopPolicy,omitempty"`

	// fairSharing defines the properties of the LocalQueue when
	// participating in AdmissionFairSharing.  The values are only relevant
	// if AdmissionFairSharing is enabled in the Kueue configuration.
	// +optional
	FairSharing *FairSharing `json:"fairSharing,omitempty"`
}

type TopologyInfo struct {
	// name is the name of the topology.
	//
	// +required
	Name TopologyReference `json:"name"`

	// levels define the levels of topology.
	//
	// +required
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=317
	Levels []string `json:"levels,omitempty"`
}

// LocalQueueStatus defines the observed state of LocalQueue
type LocalQueueStatus struct {
	// conditions hold the latest available observations of the LocalQueue
	// current state.
	// conditions are limited to 16 items.
	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	// +kubebuilder:validation:MaxItems=16
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// pendingWorkloads is the number of Workloads in the LocalQueue not yet admitted to a ClusterQueue
	// +optional
	PendingWorkloads int32 `json:"pendingWorkloads"`

	// reservingWorkloads is the number of workloads in this LocalQueue
	// reserving quota in a ClusterQueue and that haven't finished yet.
	// +optional
	ReservingWorkloads int32 `json:"reservingWorkloads"`

	// admittedWorkloads is the number of workloads in this LocalQueue
	// admitted to a ClusterQueue and that haven't finished yet.
	// +optional
	AdmittedWorkloads int32 `json:"admittedWorkloads"`

	// flavorsReservation are the reserved quotas, by flavor currently in use by the
	// workloads assigned to this LocalQueue.
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=16
	// +optional
	FlavorsReservation []LocalQueueFlavorUsage `json:"flavorsReservation,omitempty"`

	// flavorsUsage are the used quotas, by flavor currently in use by the
	// workloads assigned to this LocalQueue.
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=16
	// +optional
	FlavorsUsage []LocalQueueFlavorUsage `json:"flavorsUsage,omitempty"`

	// fairSharing contains the information about the current status of fair sharing.
	// +optional
	FairSharing *LocalQueueFairSharingStatus `json:"fairSharing,omitempty"`
}

// LocalQueueFairSharingStatus contains the information about the current status of Fair Sharing.
type LocalQueueFairSharingStatus struct {
	// weightedShare represents the maximum of the ratios of usage
	// above nominal quota to the lendable resources in the
	// Cohort, among all the resources provided by the Node, and
	// divided by the weight.  If zero, it means that the usage of
	// the Node is below the nominal quota.  If the Node has a
	// weight of zero and is borrowing, this will return
	// 9223372036854775807, the maximum possible share value.
	// +required
	WeightedShare int64 `json:"weightedShare"`

	// admissionFairSharingStatus represents information relevant to the Admission Fair Sharing
	// +optional
	AdmissionFairSharingStatus *LocalQueueAdmissionFairSharingStatus `json:"admissionFairSharingStatus,omitempty"`
}

type LocalQueueAdmissionFairSharingStatus struct {
	// consumedResources represents the aggregated usage of resources over time,
	// with decaying function applied.
	// The value is populated if usage consumption functionality is enabled in Kueue config.
	// +required
	ConsumedResources corev1.ResourceList `json:"consumedResources"`

	// lastUpdate is the time when share and consumed resources were updated.
	// +required
	LastUpdate metav1.Time `json:"lastUpdate"`
}

const (
	// LocalQueueActive indicates that the ClusterQueue that backs the LocalQueue is active and
	// the LocalQueue can submit new workloads to its ClusterQueue.
	LocalQueueActive string = "Active"
)

type LocalQueueFlavorUsage struct {
	// name of the flavor.
	// +required
	Name ResourceFlavorReference `json:"name"`

	// resources lists the quota usage for the resources in this flavor.
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=16
	// +required
	Resources []LocalQueueResourceUsage `json:"resources,omitempty"`
}

type LocalQueueResourceUsage struct {
	// name of the resource.
	// +required
	Name corev1.ResourceName `json:"name,omitempty"`

	// total is the total quantity of used quota.
	// +optional
	Total resource.Quantity `json:"total,omitempty"`
}

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="ClusterQueue",JSONPath=".spec.clusterQueue",type=string,description="Backing ClusterQueue"
// +kubebuilder:printcolumn:name="Pending Workloads",JSONPath=".status.pendingWorkloads",type=integer,description="Number of pending workloads"
// +kubebuilder:printcolumn:name="Admitted Workloads",JSONPath=".status.admittedWorkloads",type=integer,description="Number of admitted workloads that haven't finished yet."
// +kubebuilder:resource:shortName={queue,queues,lq}

// LocalQueue is the Schema for the localQueues API
type LocalQueue struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is the metadata of the LocalQueue.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec is the specification of the LocalQueue.
	// +optional
	Spec LocalQueueSpec `json:"spec"`
	// status is the status of the LocalQueue.
	// +optional
	Status LocalQueueStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// LocalQueueList contains a list of LocalQueue
type LocalQueueList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LocalQueue `json:"items"`
}

const (
	MultiKueueConfigSecretKey = "kubeconfig"
	MultiKueueClusterActive   = "Active"

	// MultiKueueOriginLabel is a label used to track the creator
	// of multikueue remote objects.
	MultiKueueOriginLabel = "kueue.x-k8s.io/multikueue-origin"

	// MultiKueueControllerName is the name used by the MultiKueue
	// admission check controller.
	MultiKueueControllerName = "kueue.x-k8s.io/multikueue"
)

type LocationType string

const (
	// PathLocationType is the path on the disk of kueue-controller-manager.
	PathLocationType LocationType = "Path"

	// SecretLocationType is the name of the secret inside the namespace in which the kueue controller
	// manager is running. The config should be stored in the "kubeconfig" key.
	SecretLocationType LocationType = "Secret"
)

type KubeConfig struct {
	// location of the KubeConfig.
	//
	// If LocationType is Secret then Location is the name of the secret inside the namespace in
	// which the kueue controller manager is running. The config should be stored in the "kubeconfig" key.
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:MinLength=1
	// +required
	Location string `json:"location"`

	// locationType of the KubeConfig.
	//
	// +kubebuilder:default=Secret
	// +optional
	// +kubebuilder:validation:Enum=Secret;Path
	LocationType LocationType `json:"locationType,omitempty"`
}

type MultiKueueClusterSpec struct {
	// kubeConfig is information on how to connect to the cluster.
	// +optional
	KubeConfig *KubeConfig `json:"kubeConfig,omitempty"`
}

type MultiKueueClusterStatus struct {
	// conditions hold the latest available observations of the MultiKueueCluster
	// current state.
	// conditions are limited to 16 elements.
	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	// +kubebuilder:validation:MaxItems=16
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// +kubebuilder:printcolumn:name="Connected",JSONPath=".status.conditions[?(@.type=='Active')].status",type="string",description="MultiKueueCluster is connected"
// +kubebuilder:printcolumn:name="Age",JSONPath=".metadata.creationTimestamp",type="date",description="Time this workload was created"
// MultiKueueCluster is the Schema for the multikueue API
type MultiKueueCluster struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is the metadata of the MultiKueueCluster.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec is the specification of the MultiKueueCluster.
	// +optional
	Spec MultiKueueClusterSpec `json:"spec,omitempty,omitzero"`

	// status is the status of the MultiKueueCluster.
	// +optional
	Status MultiKueueClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MultiKueueClusterList contains a list of MultiKueueCluster
type MultiKueueClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MultiKueueCluster `json:"items"`
}

// MultiKueueConfigSpec defines the desired state of MultiKueueConfig
type MultiKueueConfigSpec struct {
	// clusters is a list of MultiKueueClusters names where the workloads from the ClusterQueue should be distributed.
	//
	// +listType=set
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=10
	// +kubebuilder:validation:items:MaxLength=256
	// +required
	Clusters []string `json:"clusters,omitempty,omitzero"`
}

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster

// MultiKueueConfig is the Schema for the multikueue API
type MultiKueueConfig struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is the metadata of the MultiKueueConfig.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec is the specification of the MultiKueueConfig.
	// +optional
	Spec MultiKueueConfigSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// MultiKueueConfigList contains a list of MultiKueueConfig
type MultiKueueConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MultiKueueConfig `json:"items"`
}

// +kubebuilder:object:root=true

// ResourceFlavorList contains a list of ResourceFlavor
type ResourceFlavorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ResourceFlavor `json:"items"`
}

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName={flavor,flavors,rf}

// ResourceFlavor is the Schema for the resourceflavors API.
type ResourceFlavor struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is the metadata of the ResourceFlavor.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec is the specification of the ResourceFlavor.
	// +optional
	Spec ResourceFlavorSpec `json:"spec,omitempty"`
}

// TopologyReference is the name of the Topology.
// +kubebuilder:validation:MaxLength=253
// +kubebuilder:validation:Pattern="^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$"
type TopologyReference string

// ResourceFlavorSpec defines the desired state of the ResourceFlavor
// +kubebuilder:validation:XValidation:rule="!has(self.topologyName) || self.nodeLabels.size() >= 1", message="at least one nodeLabel is required when topology is set"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.topologyName) || self == oldSelf", message="resourceFlavorSpec are immutable when topologyName is set"
type ResourceFlavorSpec struct {
	// nodeLabels are labels that associate the ResourceFlavor with Nodes that
	// have the same labels.
	// When a Workload is admitted, its podsets can only get assigned
	// ResourceFlavors whose nodeLabels match the nodeSelector and nodeAffinity
	// fields.
	// Once a ResourceFlavor is assigned to a podSet, the ResourceFlavor's
	// nodeLabels should be injected into the pods of the Workload by the
	// controller that integrates with the Workload object.
	//
	// nodeLabels can be up to 8 elements.
	// +optional
	// +mapType=atomic
	// +kubebuilder:validation:MaxProperties=8
	NodeLabels map[string]string `json:"nodeLabels,omitempty"`

	// nodeTaints are taints that the nodes associated with this ResourceFlavor
	// have.
	// Workloads' podsets must have tolerations for these nodeTaints in order to
	// get assigned this ResourceFlavor during admission.
	// When this ResourceFlavor has also set the matching tolerations (in .spec.tolerations),
	// then the nodeTaints are not considered during admission.
	// Only the 'NoSchedule' and 'NoExecute' taint effects are evaluated,
	// while 'PreferNoSchedule' is ignored.
	//
	// An example of a nodeTaint is
	// cloud.provider.com/preemptible="true":NoSchedule
	//
	// nodeTaints can be up to 8 elements.
	//
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=8
	// +kubebuilder:validation:XValidation:rule="self.all(x, x.effect in ['NoSchedule', 'PreferNoSchedule', 'NoExecute'])", message="supported taint effect values: 'NoSchedule', 'PreferNoSchedule', 'NoExecute'"
	NodeTaints []corev1.Taint `json:"nodeTaints,omitempty"`

	// tolerations are extra tolerations that will be added to the pods admitted in
	// the quota associated with this resource flavor.
	//
	// An example of a toleration is
	// cloud.provider.com/preemptible="true":NoSchedule
	//
	// tolerations can be up to 8 elements.
	//
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=8
	// +kubebuilder:validation:XValidation:rule="self.all(x, !has(x.key) ? x.operator == 'Exists' : true)", message="operator must be Exists when 'key' is empty, which means 'match all values and all keys'"
	// +kubebuilder:validation:XValidation:rule="self.all(x, has(x.tolerationSeconds) ? x.effect == 'NoExecute' : true)", message="effect must be 'NoExecute' when 'tolerationSeconds' is set"
	// +kubebuilder:validation:XValidation:rule="self.all(x, !has(x.operator) || x.operator in ['Equal', 'Exists'])", message="supported toleration values: 'Equal'(default), 'Exists'"
	// +kubebuilder:validation:XValidation:rule="self.all(x, has(x.operator) && x.operator == 'Exists' ? !has(x.value) : true)", message="a value must be empty when 'operator' is 'Exists'"
	// +kubebuilder:validation:XValidation:rule="self.all(x, !has(x.effect) || x.effect in ['NoSchedule', 'PreferNoSchedule', 'NoExecute'])", message="supported taint effect values: 'NoSchedule', 'PreferNoSchedule', 'NoExecute'"
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// topologyName indicates topology for the TAS ResourceFlavor.
	// When specified, it enables scraping of the topology information from the
	// nodes matching to the Resource Flavor node labels.
	//
	// +optional
	TopologyName *TopologyReference `json:"topologyName,omitempty"`
}

// WorkloadSpec defines the desired state of Workload
// +kubebuilder:validation:XValidation:rule="!has(self.priorityClassRef) || has(self.priority)", message="priority should not be nil when priorityClassRef is set"
type WorkloadSpec struct {
	// podSets is a list of sets of homogeneous pods, each described by a Pod spec
	// and a count.
	// There must be at least one element and at most 8.
	// podSets cannot be changed.
	//
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=8
	// +kubebuilder:validation:MinItems=1
	// +optional
	PodSets []PodSet `json:"podSets"`

	// queueName is the name of the LocalQueue the Workload is associated with.
	// queueName cannot be changed while .status.admission is not null.
	// +optional
	QueueName LocalQueueName `json:"queueName,omitempty"`

	// priorityClassRef references a PriorityClass object that defines the workload's priority.
	//
	// +optional
	PriorityClassRef *PriorityClassRef `json:"priorityClassRef,omitempty"`

	// priority determines the order of access to the resources managed by the
	// ClusterQueue where the workload is queued.
	// The priority value is populated from the referenced PriorityClass (via priorityClassRef).
	// The higher the value, the higher the priority.
	// If priorityClassRef is specified, priority must not be null.
	// +optional
	Priority *int32 `json:"priority,omitempty"`

	// active determines if a workload can be admitted into a queue.
	// Changing active from true to false will evict any running workloads.
	// Possible values are:
	//
	//   - false: indicates that a workload should never be admitted and evicts running workloads
	//   - true: indicates that a workload can be evaluated for admission into it's respective queue.
	//
	// Defaults to true
	// +kubebuilder:default=true
	Active *bool `json:"active,omitempty"`

	// maximumExecutionTimeSeconds if provided, determines the maximum time, in seconds,
	// the workload can be admitted before it's automatically deactivated.
	//
	// If unspecified, no execution time limit is enforced on the Workload.
	//
	// +optional
	// +kubebuilder:validation:Minimum=1
	MaximumExecutionTimeSeconds *int32 `json:"maximumExecutionTimeSeconds,omitempty"`
}

// PriorityClassGroup indicates the API group of the PriorityClass object.
//
// +enum
// +kubebuilder:validation:Enum=kueue.x-k8s.io;scheduling.k8s.io
type PriorityClassGroup string

const (
	// PodPriorityClassGroup represents the core Kubernetes scheduling group.
	PodPriorityClassGroup PriorityClassGroup = "scheduling.k8s.io"

	// WorkloadPriorityClassGroup represents the Kueue-specific priority group.
	WorkloadPriorityClassGroup PriorityClassGroup = "kueue.x-k8s.io"
)

// PriorityClassKind is the kind of the PriorityClass object.
//
// +enum
// +kubebuilder:validation:Enum=WorkloadPriorityClass;PriorityClass
type PriorityClassKind string

const (
	// PodPriorityClassKind represents the core Kubernetes PriorityClass kind.
	PodPriorityClassKind PriorityClassKind = "PriorityClass"

	// WorkloadPriorityClassKind represents the Kueue WorkloadPriorityClass kind.
	WorkloadPriorityClassKind PriorityClassKind = "WorkloadPriorityClass"
)

// NewPriorityClassRef creates a new PriorityClassRef with the specified group, kind, and name.
func NewPriorityClassRef(group PriorityClassGroup, kind PriorityClassKind, name string) *PriorityClassRef {
	return &PriorityClassRef{Group: group, Kind: kind, Name: name}
}

// NewWorkloadPriorityClassRef creates a PriorityClassRef for a WorkloadPriorityClass.
func NewWorkloadPriorityClassRef(name string) *PriorityClassRef {
	return NewPriorityClassRef(WorkloadPriorityClassGroup, WorkloadPriorityClassKind, name)
}

// NewPodPriorityClassRef creates a PriorityClassRef for a core Kubernetes PriorityClass.
func NewPodPriorityClassRef(name string) *PriorityClassRef {
	return NewPriorityClassRef(PodPriorityClassGroup, PodPriorityClassKind, name)
}

// PriorityClassRef references a PriorityClass in a specific API group.
//
// +kubebuilder:validation:XValidation:rule="(self.group == 'scheduling.k8s.io') ? self.kind == 'PriorityClass' : true", message="only the PriorityClass kind is allowed for the scheduling.k8s.io group"
// +kubebuilder:validation:XValidation:rule="(self.group == 'kueue.x-k8s.io') ? self.kind == 'WorkloadPriorityClass' : true", message="only the WorkloadPriorityClass kind is allowed for the kueue.x-k8s.io group"
type PriorityClassRef struct {
	// group is the API group of the PriorityClass object.
	// Use "kueue.x-k8s.io" for WorkloadPriorityClass.
	// Use "scheduling.k8s.io" for Pod PriorityClass.
	//
	// +required
	Group PriorityClassGroup `json:"group,omitempty"`

	// kind is the kind of the PriorityClass object.
	//
	// +required
	Kind PriorityClassKind `json:"kind,omitempty"`

	// name is the name of the PriorityClass the Workload is associated with.
	// If specified, indicates the workload's priority.
	// "system-node-critical" and "system-cluster-critical" are two special
	// keywords which indicate the highest priorities with the former being
	// the highest priority. Any other name must be defined by creating a
	// PriorityClass object with that name. If not specified, the workload
	// priority will be default or zero if there is no default.
	//
	// +required
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern="^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$"
	Name string `json:"name,omitempty"`
}

// PodSetTopologyRequest defines the topology request for a PodSet.
type PodSetTopologyRequest struct {
	// required indicates the topology level required by the PodSet, as
	// indicated by the `kueue.x-k8s.io/podset-required-topology` PodSet
	// annotation.
	// This is limited to 63 characters.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=63
	Required *string `json:"required,omitempty"`

	// preferred indicates the topology level preferred by the PodSet, as
	// indicated by the `kueue.x-k8s.io/podset-preferred-topology` PodSet
	// annotation.
	// This is limited to 63 characters.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=63
	Preferred *string `json:"preferred,omitempty"`

	// unconstrained indicates that Kueue has the freedom to schedule the PodSet within
	// the entire available capacity, without constraints on the compactness of the placement.
	// This is indicated by the `kueue.x-k8s.io/podset-unconstrained-topology` PodSet annotation.
	//
	// +optional
	// +kubebuilder:validation:Type=boolean
	Unconstrained *bool `json:"unconstrained,omitempty"`

	// podIndexLabel indicates the name of the label indexing the pods.
	// For example, in the context of
	// - kubernetes job this is: kubernetes.io/job-completion-index
	// - JobSet: kubernetes.io/job-completion-index (inherited from Job)
	// - Kubeflow: training.kubeflow.org/replica-index
	// 	This is limited to 317 characters.
	// +kubebuilder:validation:MaxLength=317
	// +optional
	PodIndexLabel *string `json:"podIndexLabel,omitempty"`

	// subGroupIndexLabel indicates the name of the label indexing the instances of replicated Jobs (groups)
	// within a PodSet. For example, in the context of JobSet this is jobset.sigs.k8s.io/job-index.
	// 	This is limited to 317 characters.
	// +kubebuilder:validation:MaxLength=317
	// +optional
	SubGroupIndexLabel *string `json:"subGroupIndexLabel,omitempty"`

	// subGroupCount indicates the count of replicated Jobs (groups) within a PodSet.
	// For example, in the context of JobSet this value is read from jobset.sigs.k8s.io/replicatedjob-replicas.
	// +optional
	SubGroupCount *int32 `json:"subGroupCount,omitempty"`

	// podSetGroupName indicates the name of the group of PodSets to which this PodSet belongs to.
	// PodSets with the same `PodSetGroupName` should be assigned the same ResourceFlavor
	//
	// +optional
	// +kubebuilder:validation:MaxLength=256
	PodSetGroupName *string `json:"podSetGroupName,omitempty"`

	// podSetSliceRequiredTopology indicates the topology level required by the PodSet slice, as
	// indicated by the `kueue.x-k8s.io/podset-slice-required-topology` annotation.
	//
	// This is limited to 63
	// +optional
	// +kubebuilder:validation:MaxLength=63
	PodSetSliceRequiredTopology *string `json:"podSetSliceRequiredTopology,omitempty"`

	// podSetSliceSize indicates the size of a subgroup of pods in a PodSet for which
	// Kueue finds a requested topology domain on a level defined
	// in `kueue.x-k8s.io/podset-slice-required-topology` annotation.
	//
	// +optional
	PodSetSliceSize *int32 `json:"podSetSliceSize,omitempty"`
}

type Admission struct {
	// clusterQueue is the name of the ClusterQueue that admitted this workload.
	// +optional
	ClusterQueue ClusterQueueReference `json:"clusterQueue"`

	// podSetAssignments hold the admission results for each of the .spec.podSets entries.
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=8
	// +optional
	PodSetAssignments []PodSetAssignment `json:"podSetAssignments"`
}

// PodSetReference is the name of a PodSet.
// +kubebuilder:validation:MaxLength=63
// +kubebuilder:validation:Pattern="^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"
type PodSetReference string

func NewPodSetReference(name string) PodSetReference {
	return PodSetReference(strings.ToLower(name))
}

type PodSetAssignment struct {
	// name is the name of the podSet. It should match one of the names in .spec.podSets.
	// +kubebuilder:default=main
	// +optional
	Name PodSetReference `json:"name"`

	// flavors are the flavors assigned to the workload for each resource.
	// +optional
	Flavors map[corev1.ResourceName]ResourceFlavorReference `json:"flavors,omitempty"`

	// resourceUsage keeps track of the total resources all the pods in the podset need to run.
	//
	// Beside what is provided in podSet's specs, this calculation takes into account
	// the LimitRange defaults and RuntimeClass overheads at the moment of admission.
	// This field will not change in case of quota reclaim.
	// +optional
	ResourceUsage corev1.ResourceList `json:"resourceUsage,omitempty"`

	// count is the number of pods taken into account at admission time.
	// This field will not change in case of quota reclaim.
	// Value could be missing for Workloads created before this field was added,
	// in that case spec.podSets[*].count value will be used.
	//
	// +optional
	// +kubebuilder:validation:Minimum=0
	Count *int32 `json:"count,omitempty"`

	// topologyAssignment indicates the topology assignment divided into
	// topology domains corresponding to the lowest level of the topology.
	// The assignment specifies the number of Pods to be scheduled per topology
	// domain and specifies the node selectors for each topology domain, in the
	// following way:
	// * `levels` specifies the node selector keys (same for all domains).
	//   - If the TopologySpec.Levels field contains "kubernetes.io/hostname" label,
	//     topologyAssignment will contain data only for this label,
	//     and omit higher levels in the topology.
	// * `slices` specifies the node selector values and pod counts for all domains
	//   (which may be partitioned into separate slices).
	//   - The node selector values are arranged first by topology level, only then by domain.
	//     (This allows "optimizing" similar values; see below).
	// * The format of `slices` supports the following variations
	//   (aimed to optimize the total bytesize for very large number of domains; see examples below):
	//   - When all node selector values (at a given topology level, in a given slice)
	//     share a common prefix and/or suffix, these may be stored
	//     in dedicated `prefix`/`suffix` fields.
	//     If so, the array of `roots` will only store the remaining parts of these strings.
	//   - When all node selector values (at a given topology level, in a given slice)
	//     are identical, this may be represented by `universal` value.
	//   - When all pod counts (in a given slice) are identical,
	//     this may be represented by `universal` pod count.
	//
	// Example 1:
	//
	// The following represents an assignment in which:
	// * 4 Pods are to be scheduled on nodes matching the node selector:
	//   - cloud.provider.com/topology-block: block-1
	//   - cloud.provider.com/topology-rack: rack-1
	// * 2 Pods are to be scheduled on nodes matching the node selector:
	//   - cloud.provider.com/topology-block: block-1
	//   - cloud.provider.com/topology-rack: rack-2
	//
	// topologyAssignment:
	//   levels:
	//   - cloud.provider.com/topology-block
	//   - cloud.provider.com/topology-rack
	//   slices:
	//   - domainCount: 2
	//     valuesPerLevel:
	//     - individual:
	//         roots: [block-1, block-1]
	//     - individual:
	//         roots: [rack-1, rack-2]
	//     podCounts:
	//       individual: [4, 2]
	//
	// Example 2:
	//
	// The following is equivalent to Example 1 - but using extracted prefix and universalValue.
	//
	// topologyAssignment:
	//   levels:
	//   - cloud.provider.com/topology-block
	//   - cloud.provider.com/topology-rack
	//   slices:
	//   - domainCount: 2
	//     valuesPerLevel:
	//     - universal: block-1
	//     - individual:
	//         prefix: rack-
	// 		   roots: [1, 2]
	//     podCounts:
	//       individual: [4, 2]
	//
	// Example 3:
	//
	// Now suppose that:
	// - the Topology object defines kubernetes.io/hostname as the lowest level
	//   (and hence, in the topologyAssignment, we omit all other levels
	//   since the hostname label suffices to explicitly identify a proper node),
	// - we assign 1 Pod per each node,
	// - the node naming scheme is `block-{blockId}-rack-{rackId}-node-{nodeId}`.
	// Then, using the "extraction of commons", the assignment from Examples 1-2 would look as follows:
	//
	// topologyAssignment:
	//   levels:
	//   - kubernetes.io/hostname
	//   slices:
	//   - domainCount: 6
	//     valuesPerLevel:
	//     - individual:
	//         prefix: block-1-rack-
	// 		   roots: [1-node-1, 1-node-2, 1-node-3, 1-node-4, 2-node-1, 2-node-2]
	//     podCounts:
	//       universal: 1
	//
	// Example 4:
	//
	// By using multiple slices, we can afford even longer common prefixes.
	// The assignment from Example 3 can be alternatively represented as follows:
	//
	// topologyAssignment:
	//   levels:
	//   - kubernetes.io/hostname
	//   slices:
	//   - domainCount: 4
	//     valuesPerLevel:
	//     - individual:
	//         prefix: block-1-rack-1-node-
	// 		   roots: [1, 2, 3, 4]
	//     podCounts:
	//       universal: 1
	//   - domainCount: 2
	//     valuesPerLevel:
	//     - individual:
	//         prefix: block-1-rack-2-node-
	// 		   roots: [1, 2]
	//     podCounts:
	//       universal: 1
	//
	// +optional
	TopologyAssignment *TopologyAssignment `json:"topologyAssignment,omitempty"`

	// delayedTopologyRequest indicates the topology assignment is delayed.
	// Topology assignment might be delayed in case there is ProvisioningRequest
	// AdmissionCheck used.
	// Kueue schedules the second pass of scheduling for each workload with at
	// least one PodSet which has delayedTopologyRequest=true and without
	// topologyAssignment.
	//
	// +optional
	DelayedTopologyRequest *DelayedTopologyRequestState `json:"delayedTopologyRequest,omitempty"`
}

// DelayedTopologyRequestState indicates the state of the delayed TopologyRequest.
// +enum
// +kubebuilder:validation:Enum=Pending;Ready
// +kubebuilder:validation:MaxLength=7
type DelayedTopologyRequestState string

const (
	// DelayedTopologyRequestStatePending indicates the delayed TopologyRequest is waiting for determining.
	DelayedTopologyRequestStatePending DelayedTopologyRequestState = "Pending"

	// DelayedTopologyRequestStateReady indicates the delayed TopologyRequest is was requested and completed.
	DelayedTopologyRequestStateReady DelayedTopologyRequestState = "Ready"
)

// +kubebuilder:validation:XValidation:rule="self.slices.all(x, size(x.valuesPerLevel) == size(self.levels))", message="valuesPerLevel must have the same length as the number of levels in this TopologyAssignment"
type TopologyAssignment struct {
	// levels is an ordered list of keys denoting the levels of the assigned
	// topology (i.e. node label keys), from the highest to the lowest level of
	// the topology.
	//
	// +required
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=317
	Levels []string `json:"levels,omitempty"`

	// slices represent topology assignments for subsets of pods of a workload.
	// The full assignment is obtained as a union of all slices.
	// +required
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=1000
	Slices []TopologyAssignmentSlice `json:"slices,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="!has(self.podCounts.individual) || size(self.podCounts.individual) == self.domainCount", message="podCounts.individual must have length equal to domainCount"
// +kubebuilder:validation:XValidation:rule="self.valuesPerLevel.all(x, !has(x.individual) || size(x.individual.roots) == self.domainCount)", message="valuesPerLevel.individual, if set, must have roots of length equal to domainCount of this TopologyAssignmentSlice"
type TopologyAssignmentSlice struct {
	// domainCount is the number of domains covered by this slice.
	// +required
	// +kubebuilder:validation:Minimum=1
	DomainCount int32 `json:"domainCount,omitempty"`

	// valuesPerLevel has one entry for each of the Levels specified in the TopologyAssignment.
	// The entry corresponding to a particular level specifies the placement of pods at that level.
	// +required
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	ValuesPerLevel []TopologyAssignmentSliceLevelValues `json:"valuesPerLevel,omitempty"`

	// podCounts specifies the number of pods allocated per each domain.
	// +required
	PodCounts TopologyAssignmentSlicePodCounts `json:"podCounts,omitempty"`
}

// +kubebuilder:validation:ExactlyOneOf=universal;individual
type TopologyAssignmentSliceLevelValues struct {
	// universal - if set - specifies a single topology placement value (at a particular topology level)
	// that applies to all pods in the current TopologyAssignmentSlice.
	// Exactly one of universal, individual must be set.
	// +optional
	// +kubebuilder:validation:MaxLength=63
	Universal *string `json:"universal,omitempty"`

	// individual - if set - specifies multiple topology placement values (at a particular topology level)
	// that apply to the pods in the current TopologyAssignmentSlice.
	// Exactly one of universal, individual must be set.
	// +optional
	Individual *TopologyAssignmentSliceLevelIndividualValues `json:"individual,omitempty"`
}

type TopologyAssignmentSliceLevelIndividualValues struct {
	// prefix specifies a common prefix for all values in this slice assignment.
	// It must be either nil pointer or a non-empty string.
	// +optional
	// +kubebuilder:validation:MaxLength=63
	Prefix *string `json:"prefix,omitempty"`
	// suffix specifies a common suffix for all values in this slice assignment.
	// It must be either nil pointer or a non-empty string.
	// +optional
	// +kubebuilder:validation:MaxLength=63
	Suffix *string `json:"suffix,omitempty"`

	// roots specifies the values in this assignment (excluding prefix and suffix, if non-empty).
	// Its length must be equal to the "domainCount" field of the TopologyAssignmentSlice.
	// +required
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=100000
	// +kubebuilder:validation:items:MaxLength=63
	Roots []string `json:"roots,omitempty"`
}

// +kubebuilder:validation:ExactlyOneOf=universal;individual
type TopologyAssignmentSlicePodCounts struct {
	// universal - if set - specifies the number of pods allocated in every domain in this slice.
	// Exactly one of universal, individual must be set.
	// +optional
	// +kubebuilder:validation:Minimum=1
	Universal *int32 `json:"universal,omitempty"`

	// individual - if set - specifies the number of pods allocated in each domain in this slice.
	// If set, its length must be equal to the "domainCount" field of the TopologyAssignmentSlice.
	// Exactly one of universal, individual must be set.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=100000
	// +kubebuilder:validation:items:Minimum=1
	Individual []int32 `json:"individual,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="has(self.minCount) ? self.minCount <= self.count : true", message="minCount should be positive and less or equal to count"
type PodSet struct {
	// name is the PodSet name.
	// +kubebuilder:default=main
	// +optional
	Name PodSetReference `json:"name,omitempty"`

	// template is the Pod template.
	//
	// The only allowed fields in template.metadata are labels and annotations.
	//
	// If requests are omitted for a container or initContainer,
	// they default to the limits if they are explicitly specified for the
	// container or initContainer.
	//
	// During admission, the rules in nodeSelector and
	// nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution that match
	// the keys in the nodeLabels from the ResourceFlavors considered for this
	// Workload are used to filter the ResourceFlavors that can be assigned to
	// this podSet.
	// +required
	Template corev1.PodTemplateSpec `json:"template,omitempty"`

	// count is the number of pods for the spec.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=0
	// +optional
	Count int32 `json:"count"`

	// minCount is the minimum number of pods for the spec acceptable
	// if the workload supports partial admission.
	//
	// If not provided, partial admission for the current PodSet is not
	// enabled.
	//
	// Only one podSet within the workload can use this.
	//
	// This is an alpha field and requires enabling PartialAdmission feature gate.
	//
	// +optional
	// +kubebuilder:validation:Minimum=1
	MinCount *int32 `json:"minCount,omitempty"`

	// topologyRequest defines the topology request for the PodSet.
	//
	// +optional
	TopologyRequest *PodSetTopologyRequest `json:"topologyRequest,omitempty"`
}

// WorkloadStatus defines the observed state of Workload
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.clusterName) || !has(self.clusterName) || oldSelf.clusterName == self.clusterName", message="clusterName is immutable once set"
// +kubebuilder:validation:XValidation:rule="!has(self.clusterName) || (!has(self.nominatedClusterNames) || (has(self.nominatedClusterNames) && size(self.nominatedClusterNames) == 0))", message="clusterName and nominatedClusterNames are mutually exclusive"
type WorkloadStatus struct {
	// conditions hold the latest available observations of the Workload
	// current state.
	//
	// The type of the condition could be:
	//
	// - Admitted: the Workload was admitted through a ClusterQueue.
	// - Finished: the associated workload finished running (failed or succeeded).
	// - PodsReady: at least `.spec.podSets[*].count` Pods are ready or have
	// succeeded.
	// conditions are limited to 16 items.
	//
	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	// +kubebuilder:validation:MaxItems=16
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// admission holds the parameters of the admission of the workload by a
	// ClusterQueue. admission can be set back to null, but its fields cannot be
	// changed once set.
	// +optional
	Admission *Admission `json:"admission,omitempty"`

	// requeueState holds the re-queue state
	// when a workload meets Eviction with PodsReadyTimeout reason.
	//
	// +optional
	RequeueState *RequeueState `json:"requeueState,omitempty"`

	// reclaimablePods keeps track of the number pods within a podset for which
	// the resource reservation is no longer needed.
	// +optional
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=8
	ReclaimablePods []ReclaimablePod `json:"reclaimablePods,omitempty"`

	// admissionChecks list all the admission checks required by the workload and the current status
	// +optional
	// +listType=map
	// +listMapKey=name
	// +patchStrategy=merge
	// +patchMergeKey=name
	// +kubebuilder:validation:MaxItems=8
	AdmissionChecks []AdmissionCheckState `json:"admissionChecks,omitempty" patchStrategy:"merge" patchMergeKey:"name"`

	// resourceRequests provides a detailed view of the resources that were
	// requested by a non-admitted workload when it was considered for admission.
	// If admission is non-null, resourceRequests will be empty because
	// admission.resourceUsage contains the detailed information.
	//
	// +optional
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=8
	ResourceRequests []PodSetRequest `json:"resourceRequests,omitempty"`

	// accumulatedPastExecutionTimeSeconds holds the total time, in seconds, the workload spent
	// in Admitted state, in the previous `Admit` - `Evict` cycles.
	//
	// +optional
	AccumulatedPastExecutionTimeSeconds *int32 `json:"accumulatedPastExecutionTimeSeconds,omitempty"`

	// schedulingStats tracks scheduling statistics
	//
	// +optional
	SchedulingStats *SchedulingStats `json:"schedulingStats,omitempty"`

	// nominatedClusterNames specifies the list of cluster names that have been nominated for scheduling.
	// This field is mutually exclusive with the `.status.clusterName` field, and is reset when
	// `status.clusterName` is set.
	// This field is optional.
	//
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=10
	// +kubebuilder:validation:items:MaxLength=256
	// +optional
	NominatedClusterNames []string `json:"nominatedClusterNames,omitempty"`

	// clusterName is the name of the cluster where the workload is currently assigned.
	//
	// With ElasticJobs, this field may also indicate the cluster where the original (old) workload
	// was assigned, providing placement context for new scaled-up workloads. This supports
	// affinity or propagation policies across workload slices.
	//
	// This field is reset after the Workload is evicted.
	// +optional
	// +kubebuilder:validation:MaxLength=256
	ClusterName *string `json:"clusterName,omitempty"`

	// unhealthyNodes holds the failed nodes running at least one pod of this workload
	// when Topology-Aware Scheduling is used. This field should not be set by the users.
	// It indicates Kueue's scheduler is searching for replacements of the failed nodes.
	// Requires enabling the TASFailedNodeReplacement feature gate.
	//
	// +optional
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=8
	UnhealthyNodes []UnhealthyNode `json:"unhealthyNodes,omitempty"`
}

type SchedulingStats struct {
	// evictions tracks eviction statistics by reason and underlyingCause.
	//
	// +optional
	// +listType=map
	// +listMapKey=reason
	// +listMapKey=underlyingCause
	// +patchStrategy=merge
	// +patchMergeKey=reason
	// +patchMergeKey=underlyingCause
	// +kubebuilder:validation:MaxItems=256
	Evictions []WorkloadSchedulingStatsEviction `json:"evictions,omitempty"`
}

// EvictionUnderlyingCause represents the underlying cause of a workload eviction.
// +kubebuilder:validation:MaxLength=316
type EvictionUnderlyingCause string

type WorkloadSchedulingStatsEviction struct {
	// reason specifies the programmatic identifier for the eviction cause.
	//
	// +required
	// +kubebuilder:validation:MaxLength=316
	Reason string `json:"reason"`

	// underlyingCause specifies a finer-grained explanation that complements the eviction reason.
	// This may be an empty string.
	//
	// +required
	UnderlyingCause EvictionUnderlyingCause `json:"underlyingCause"`

	// count tracks the number of evictions for this reason and detailed reason.
	//
	// +required
	// +kubebuilder:validation:Minimum=0
	Count int32 `json:"count"`
}

type UnhealthyNode struct {
	// name is the name of the unhealthy node.
	//
	// +required
	// +kubebuilder:validation:MaxLength=63
	Name string `json:"name"`
}

type RequeueState struct {
	// count records the number of times a workload has been re-queued
	// When a deactivated (`.spec.activate`=`false`) workload is reactivated (`.spec.activate`=`true`),
	// this count would be reset to null.
	//
	// +optional
	// +kubebuilder:validation:Minimum=0
	Count *int32 `json:"count,omitempty"`

	// requeueAt records the time when a workload will be re-queued.
	// When a deactivated (`.spec.activate`=`false`) workload is reactivated (`.spec.activate`=`true`),
	// this time would be reset to null.
	//
	// +optional
	RequeueAt *metav1.Time `json:"requeueAt,omitempty"`
}

// AdmissionCheckReference is the name of an AdmissionCheck.
// +kubebuilder:validation:MaxLength=316
// +kubebuilder:validation:MinLength=1
type AdmissionCheckReference string

type AdmissionCheckState struct {
	// name identifies the admission check.
	// +required
	Name AdmissionCheckReference `json:"name,omitempty"`
	// state of the admissionCheck, one of Pending, Ready, Retry, Rejected
	// +required
	// +kubebuilder:validation:Enum=Pending;Ready;Retry;Rejected
	State CheckState `json:"state,omitempty"`
	// lastTransitionTime is the last time the condition transitioned from one status to another.
	// This should be when the underlying condition changed.  If that is not known, then using the time when the API field changed is acceptable.
	// +required
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Format=date-time
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty,omitzero"`
	// message is a human readable message indicating details about the transition.
	// This may be an empty string.
	// +required
	// +kubebuilder:validation:MaxLength=32768
	Message string `json:"message" protobuf:"bytes,6,opt,name=message"`
	// requeueAfterSeconds indicates how long to wait at least before
	// retrying to admit the workload.
	// The admission check controllers can set this field when State=Retry
	// to implement delays between retry attempts.
	//
	// If nil when State=Retry, Kueue will retry immediately.
	// If set, Kueue will add the workload back to the queue after
	//   lastTransitionTime + RequeueAfterSeconds is over.
	//
	// +optional
	// +kubebuilder:validation:Minimum=0
	RequeueAfterSeconds *int32 `json:"requeueAfterSeconds,omitempty"`
	// retryCount tracks retry attempts for this admission check.
	// Kueue automatically increments the counter whenever the
	// state transitions to Retry.
	// +optional
	// +kubebuilder:validation:Minimum=0
	RetryCount *int32 `json:"retryCount,omitempty"`

	// podSetUpdates contains a list of pod set modifications suggested by AdmissionChecks.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=8
	PodSetUpdates []PodSetUpdate `json:"podSetUpdates,omitempty"`
}

// PodSetUpdate contains a list of pod set modifications suggested by AdmissionChecks.
// The modifications should be additive only - modifications of already existing keys
// or having the same key provided by multiple AdmissionChecks is not allowed and will
// result in failure during workload admission.
type PodSetUpdate struct {
	// name of the PodSet to modify. Should match to one of the Workload's PodSets.
	// +required
	Name PodSetReference `json:"name"`

	// labels of the PodSet to modify.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// annotations of the PodSet to modify.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// nodeSelector of the PodSet to modify.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// tolerations of the PodSet to modify.
	// +optional
	// +kubebuilder:validation:MaxItems=8
	// +listType=atomic
	// +kubebuilder:validation:XValidation:rule="self.all(x, !has(x.key) ? x.operator == 'Exists' : true)", message="operator must be Exists when 'key' is empty, which means 'match all values and all keys'"
	// +kubebuilder:validation:XValidation:rule="self.all(x, has(x.tolerationSeconds) ? x.effect == 'NoExecute' : true)", message="effect must be 'NoExecute' when 'tolerationSeconds' is set"
	// +kubebuilder:validation:XValidation:rule="self.all(x, !has(x.operator) || x.operator in ['Equal', 'Exists'])", message="supported toleration values: 'Equal'(default), 'Exists'"
	// +kubebuilder:validation:XValidation:rule="self.all(x, has(x.operator) && x.operator == 'Exists' ? !has(x.value) : true)", message="a value must be empty when 'operator' is 'Exists'"
	// +kubebuilder:validation:XValidation:rule="self.all(x, !has(x.effect) || x.effect in ['NoSchedule', 'PreferNoSchedule', 'NoExecute'])", message="supported taint effect values: 'NoSchedule', 'PreferNoSchedule', 'NoExecute'"
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
}

type ReclaimablePod struct {
	// name is the PodSet name.
	// +required
	Name PodSetReference `json:"name"`

	// count is the number of pods for which the requested resources are no longer needed.
	// +kubebuilder:validation:Minimum=0
	// +required
	Count int32 `json:"count"`
}

type PodSetRequest struct {
	// name is the name of the podSet. It should match one of the names in .spec.podSets.
	// +required
	Name PodSetReference `json:"name"`

	// resources is the total resources all the pods in the podset need to run.
	//
	// Beside what is provided in podSet's specs, this value also takes into account
	// the LimitRange defaults and RuntimeClass overheads at the moment of consideration
	// and the application of resource.excludeResourcePrefixes and resource.transformations.
	// +optional
	Resources corev1.ResourceList `json:"resources,omitempty"`
}

const (
	// WorkloadAdmitted means that the Workload has reserved quota and all the admissionChecks
	// defined in the ClusterQueue are satisfied.
	WorkloadAdmitted = "Admitted"

	// WorkloadQuotaReserved means that the Workload has reserved quota a ClusterQueue.
	WorkloadQuotaReserved = "QuotaReserved"

	// WorkloadFinished means that the workload associated to the
	// ResourceClaim finished running (failed or succeeded).
	WorkloadFinished = "Finished"

	// WorkloadPodsReady means that at least `.spec.podSets[*].count` Pods are
	// ready or have succeeded.
	WorkloadPodsReady = "PodsReady"

	// WorkloadEvicted means that the Workload was evicted. The possible reasons
	// for this condition are:
	// - "Preempted": the workload was preempted
	// - "PodsReadyTimeout": the workload exceeded the PodsReady timeout
	// - "AdmissionCheck": at least one admission check transitioned to False
	// - "ClusterQueueStopped": the ClusterQueue is stopped
	// - "Deactivated": the workload has spec.active set to false
	// When a workload is preempted, this condition is accompanied by the "Preempted"
	// condition which contains a more detailed reason for the preemption.
	WorkloadEvicted = "Evicted"

	// WorkloadPreempted means that the Workload was preempted.
	// The possible values of the reason field are "InClusterQueue", "InCohort".
	// In the future more reasons can be introduced, including those conveying
	// more detailed information. The more detailed reasons should be prefixed
	// by one of the "base" reasons.
	WorkloadPreempted = "Preempted"

	// WorkloadRequeued means that the Workload was requeued due to eviction.
	WorkloadRequeued = "Requeued"

	// WorkloadDeactivationTarget means that the Workload should be deactivated.
	// This condition is temporary, so it should be removed after deactivation.
	WorkloadDeactivationTarget = "DeactivationTarget"
)

// Reasons for the WorkloadPreempted condition.
const (
	// InClusterQueueReason indicates the Workload was preempted due to
	// prioritization in the ClusterQueue.
	InClusterQueueReason string = "InClusterQueue"

	// InCohortReclamationReason indicates the Workload was preempted due to
	// reclamation within the Cohort.
	InCohortReclamationReason string = "InCohortReclamation"

	// InCohortFairSharingReason indicates the Workload was preempted due to
	// Fair Sharing within the cohort.
	InCohortFairSharingReason string = "InCohortFairSharing"

	// InCohortReclaimWhileBorrowingReason indicates the Workload was preempted
	// due to reclamation within the cohort while borrowing.
	InCohortReclaimWhileBorrowingReason string = "InCohortReclaimWhileBorrowing"
)

const (
	// WorkloadInadmissible means that the Workload can't reserve quota
	// due to LocalQueue or ClusterQueue doesn't exist or inactive.
	WorkloadInadmissible = "Inadmissible"

	// WorkloadEvictedByPreemption indicates that the workload was evicted
	// in order to free resources for a workload with a higher priority.
	WorkloadEvictedByPreemption = "Preempted"

	// WorkloadEvictedByPodsReadyTimeout indicates that the eviction took
	// place due to a PodsReady timeout.
	WorkloadEvictedByPodsReadyTimeout = "PodsReadyTimeout"

	// WorkloadEvictedByAdmissionCheck indicates that the workload was evicted
	// because at least one admission check transitioned to False.
	WorkloadEvictedByAdmissionCheck = "AdmissionCheck"

	// WorkloadEvictedByClusterQueueStopped indicates that the workload was evicted
	// because the ClusterQueue is Stopped.
	WorkloadEvictedByClusterQueueStopped = "ClusterQueueStopped"

	// WorkloadEvictedByLocalQueueStopped indicates that the workload was evicted
	// because the LocalQueue is Stopped.
	WorkloadEvictedByLocalQueueStopped = "LocalQueueStopped"

	// WorkloadEvictedDueToNodeFailures indicates that the workload was evicted
	// due to non-recoverable node failures.
	WorkloadEvictedDueToNodeFailures = "NodeFailures"

	// WorkloadSliceReplaced indicates that the workload instance was
	// replaced with a new workload slice.
	WorkloadSliceReplaced = "WorkloadSliceReplaced"

	// WorkloadDeactivated indicates that the workload was evicted
	// because spec.active is set to false.
	WorkloadDeactivated = "Deactivated"

	// WorkloadReactivated indicates that the workload was requeued because
	// spec.active is set to true after deactivation.
	WorkloadReactivated = "Reactivated"

	// WorkloadBackoffFinished indicates that the workload was requeued because
	// backoff finished.
	WorkloadBackoffFinished = "BackoffFinished"

	// WorkloadClusterQueueRestarted indicates that the workload was requeued because
	// cluster queue was restarted after being stopped.
	WorkloadClusterQueueRestarted = "ClusterQueueRestarted"

	// WorkloadLocalQueueRestarted indicates that the workload was requeued because
	// local queue was restarted after being stopped.
	WorkloadLocalQueueRestarted = "LocalQueueRestarted"

	// WorkloadRequeuingLimitExceeded indicates that the workload exceeded max number
	// of re-queuing retries.
	WorkloadRequeuingLimitExceeded = "RequeuingLimitExceeded"

	// WorkloadMaximumExecutionTimeExceeded indicates that the workload exceeded its
	// maximum execution time.
	WorkloadMaximumExecutionTimeExceeded = "MaximumExecutionTimeExceeded"

	// WorkloadWaitForStart indicates the reason for PodsReady=False condition
	// when the pods have not been ready since admission, or the workload is not admitted.
	WorkloadWaitForStart = "WaitForStart"

	// WorkloadWaitForRecovery indicates the reason for the PodsReady=False condition
	// when the Pods were ready since the workload admission, but some pod has failed,
	// and workload waits for recovering.
	WorkloadWaitForRecovery = "WaitForRecovery"

	// WorkloadStarted indicates that all Pods are ready and the Workload has successfully started
	WorkloadStarted = "Started"

	// WorkloadRecovered indicates that after at least one Pod has failed, the Workload has recovered and is running
	WorkloadRecovered = "Recovered"
)

const (
	// WorkloadFinishedReasonSucceeded indicates that the workload's job finished successfully.
	WorkloadFinishedReasonSucceeded = "Succeeded"

	// WorkloadFinishedReasonFailed indicates that the workload's job finished with an error.
	WorkloadFinishedReasonFailed = "Failed"

	// WorkloadFinishedReasonOutOfSync indicates that the prebuilt workload is not in sync with its parent job.
	WorkloadFinishedReasonOutOfSync = "OutOfSync"
)

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Queue",JSONPath=".spec.queueName",type="string",description="Name of the queue this workload was submitted to"
// +kubebuilder:printcolumn:name="Reserved in",JSONPath=".status.admission.clusterQueue",type="string",description="Name of the ClusterQueue where the workload is reserving quota"
// +kubebuilder:printcolumn:name="Admitted",JSONPath=".status.conditions[?(@.type=='Admitted')].status",type="string",description="Admission status"
// +kubebuilder:printcolumn:name="Finished",JSONPath=".status.conditions[?(@.type=='Finished')].status",type="string",description="Workload finished"
// +kubebuilder:printcolumn:name="Age",JSONPath=".metadata.creationTimestamp",type="date",description="Time this workload was created"
// +kubebuilder:resource:shortName={wl}

// Workload is the Schema for the workloads API
// +kubebuilder:validation:XValidation:rule="has(self.status) && has(self.status.conditions) && self.status.conditions.exists(c, c.type == 'QuotaReserved' && c.status == 'True') && has(self.status.admission) ? size(self.spec.podSets) == size(self.status.admission.podSetAssignments) : true", message="podSetAssignments must have the same number of podSets as the spec"
// +kubebuilder:validation:XValidation:rule="(has(oldSelf.status) && has(oldSelf.status.conditions) && oldSelf.status.conditions.exists(c, c.type == 'QuotaReserved' && c.status == 'True')) ? (!has(oldSelf.spec.priorityClassRef) && !has(self.spec.priorityClassRef)) || (has(oldSelf.spec.priorityClassRef) && has(self.spec.priorityClassRef)) : true",message="priorityClassRef is immutable while workload quota reserved"
// +kubebuilder:validation:XValidation:rule="(has(oldSelf.status) && has(oldSelf.status.conditions) && oldSelf.status.conditions.exists(c, c.type == 'QuotaReserved' && c.status == 'True') && has(oldSelf.spec.priorityClassRef) && has(self.spec.priorityClassRef)) ? oldSelf.spec.priorityClassRef.group == self.spec.priorityClassRef.group : true",message="priorityClassRef.group is immutable while workload quota reserved"
// +kubebuilder:validation:XValidation:rule="(has(oldSelf.status) && has(oldSelf.status.conditions) && oldSelf.status.conditions.exists(c, c.type == 'QuotaReserved' && c.status == 'True') && has(oldSelf.spec.priorityClassRef) && has(self.spec.priorityClassRef)) ? oldSelf.spec.priorityClassRef.kind == self.spec.priorityClassRef.kind : true",message="priorityClassRef.kind is immutable while workload quota reserved"
// +kubebuilder:validation:XValidation:rule="(has(oldSelf.status) && has(oldSelf.status.conditions) && oldSelf.status.conditions.exists(c, c.type == 'QuotaReserved' && c.status == 'True') && has(oldSelf.spec.priorityClassRef) && has(self.spec.priorityClassRef) && self.spec.priorityClassRef.group == 'scheduling.k8s.io' && self.spec.priorityClassRef.kind == 'PriorityClass') ? oldSelf.spec.priorityClassRef.name == self.spec.priorityClassRef.name : true",message="priorityClassRef.name is immutable for scheduling.k8s.io/priorityclass while workload quota reserved"
// +kubebuilder:validation:XValidation:rule="(has(oldSelf.status) && has(oldSelf.status.conditions) && oldSelf.status.conditions.exists(c, c.type == 'QuotaReserved' && c.status == 'True')) && (has(self.status) && has(self.status.conditions) && self.status.conditions.exists(c, c.type == 'QuotaReserved' && c.status == 'True')) && has(oldSelf.spec.queueName) && has(self.spec.queueName) ? oldSelf.spec.queueName == self.spec.queueName : true", message="queueName is immutable while workload quota reserved"
// +kubebuilder:validation:XValidation:rule="((has(oldSelf.status) && has(oldSelf.status.conditions) && oldSelf.status.conditions.exists(c, c.type == 'Admitted' && c.status == 'True')) && (has(self.status) && has(self.status.conditions) && self.status.conditions.exists(c, c.type == 'Admitted' && c.status == 'True')))?((has(oldSelf.spec.maximumExecutionTimeSeconds)?oldSelf.spec.maximumExecutionTimeSeconds:0) ==  (has(self.spec.maximumExecutionTimeSeconds)?self.spec.maximumExecutionTimeSeconds:0)):true", message="maximumExecutionTimeSeconds is immutable while workload quota reserved"
type Workload struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is the metadata of the Workload.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec is the specification of the Workload.
	// +optional
	Spec WorkloadSpec `json:"spec"`
	// status is the status of the Workload.
	// +optional
	Status WorkloadStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// WorkloadList contains a list of ResourceClaim
type WorkloadList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Workload `json:"items"`
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *KubeConfig) DeepCopyInto(out *KubeConfig) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new KubeConfig.
func (in *KubeConfig) DeepCopy() *KubeConfig {
	if in == nil {
		return nil
	}
	out := new(KubeConfig)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *MultiKueueClusterSpec) DeepCopyInto(out *MultiKueueClusterSpec) {
	*out = *in
	if in.KubeConfig != nil {
		in, out := &in.KubeConfig, &out.KubeConfig
		*out = new(KubeConfig)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new MultiKueueClusterSpec.
func (in *MultiKueueClusterSpec) DeepCopy() *MultiKueueClusterSpec {
	if in == nil {
		return nil
	}
	out := new(MultiKueueClusterSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *MultiKueueClusterStatus) DeepCopyInto(out *MultiKueueClusterStatus) {
	*out = *in
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]metav1.Condition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new MultiKueueClusterStatus.
func (in *MultiKueueClusterStatus) DeepCopy() *MultiKueueClusterStatus {
	if in == nil {
		return nil
	}
	out := new(MultiKueueClusterStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *MultiKueueCluster) DeepCopyInto(out *MultiKueueCluster) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new MultiKueueCluster.
func (in *MultiKueueCluster) DeepCopy() *MultiKueueCluster {
	if in == nil {
		return nil
	}
	out := new(MultiKueueCluster)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *MultiKueueCluster) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *MultiKueueClusterList) DeepCopyInto(out *MultiKueueClusterList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]MultiKueueCluster, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new MultiKueueClusterList.
func (in *MultiKueueClusterList) DeepCopy() *MultiKueueClusterList {
	if in == nil {
		return nil
	}
	out := new(MultiKueueClusterList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *MultiKueueClusterList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *MultiKueueConfigSpec) DeepCopyInto(out *MultiKueueConfigSpec) {
	*out = *in
	if in.Clusters != nil {
		in, out := &in.Clusters, &out.Clusters
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new MultiKueueConfigSpec.
func (in *MultiKueueConfigSpec) DeepCopy() *MultiKueueConfigSpec {
	if in == nil {
		return nil
	}
	out := new(MultiKueueConfigSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *MultiKueueConfig) DeepCopyInto(out *MultiKueueConfig) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new MultiKueueConfig.
func (in *MultiKueueConfig) DeepCopy() *MultiKueueConfig {
	if in == nil {
		return nil
	}
	out := new(MultiKueueConfig)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *MultiKueueConfig) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *MultiKueueConfigList) DeepCopyInto(out *MultiKueueConfigList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]MultiKueueConfig, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new MultiKueueConfigList.
func (in *MultiKueueConfigList) DeepCopy() *MultiKueueConfigList {
	if in == nil {
		return nil
	}
	out := new(MultiKueueConfigList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *MultiKueueConfigList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AdmissionCheck) DeepCopyInto(out *AdmissionCheck) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AdmissionCheck.
func (in *AdmissionCheck) DeepCopy() *AdmissionCheck {
	if in == nil {
		return nil
	}
	out := new(AdmissionCheck)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *AdmissionCheck) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AdmissionCheckList) DeepCopyInto(out *AdmissionCheckList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]AdmissionCheck, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AdmissionCheckList.
func (in *AdmissionCheckList) DeepCopy() *AdmissionCheckList {
	if in == nil {
		return nil
	}
	out := new(AdmissionCheckList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *AdmissionCheckList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AdmissionCheckParametersReference) DeepCopyInto(out *AdmissionCheckParametersReference) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AdmissionCheckParametersReference.
func (in *AdmissionCheckParametersReference) DeepCopy() *AdmissionCheckParametersReference {
	if in == nil {
		return nil
	}
	out := new(AdmissionCheckParametersReference)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AdmissionCheckSpec) DeepCopyInto(out *AdmissionCheckSpec) {
	*out = *in
	if in.Parameters != nil {
		in, out := &in.Parameters, &out.Parameters
		*out = new(AdmissionCheckParametersReference)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AdmissionCheckSpec.
func (in *AdmissionCheckSpec) DeepCopy() *AdmissionCheckSpec {
	if in == nil {
		return nil
	}
	out := new(AdmissionCheckSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AdmissionCheckState) DeepCopyInto(out *AdmissionCheckState) {
	*out = *in
	in.LastTransitionTime.DeepCopyInto(&out.LastTransitionTime)
	if in.RequeueAfterSeconds != nil {
		in, out := &in.RequeueAfterSeconds, &out.RequeueAfterSeconds
		*out = new(int32)
		**out = **in
	}
	if in.RetryCount != nil {
		in, out := &in.RetryCount, &out.RetryCount
		*out = new(int32)
		**out = **in
	}
	if in.PodSetUpdates != nil {
		in, out := &in.PodSetUpdates, &out.PodSetUpdates
		*out = make([]PodSetUpdate, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AdmissionCheckState.
func (in *AdmissionCheckState) DeepCopy() *AdmissionCheckState {
	if in == nil {
		return nil
	}
	out := new(AdmissionCheckState)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AdmissionCheckStatus) DeepCopyInto(out *AdmissionCheckStatus) {
	*out = *in
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]metav1.Condition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AdmissionCheckStatus.
func (in *AdmissionCheckStatus) DeepCopy() *AdmissionCheckStatus {
	if in == nil {
		return nil
	}
	out := new(AdmissionCheckStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Admission) DeepCopyInto(out *Admission) {
	*out = *in
	if in.PodSetAssignments != nil {
		in, out := &in.PodSetAssignments, &out.PodSetAssignments
		*out = make([]PodSetAssignment, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Admission.
func (in *Admission) DeepCopy() *Admission {
	if in == nil {
		return nil
	}
	out := new(Admission)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *BorrowWithinCohort) DeepCopyInto(out *BorrowWithinCohort) {
	*out = *in
	if in.MaxPriorityThreshold != nil {
		in, out := &in.MaxPriorityThreshold, &out.MaxPriorityThreshold
		*out = new(int32)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new BorrowWithinCohort.
func (in *BorrowWithinCohort) DeepCopy() *BorrowWithinCohort {
	if in == nil {
		return nil
	}
	out := new(BorrowWithinCohort)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ClusterQueue) DeepCopyInto(out *ClusterQueue) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ClusterQueue.
func (in *ClusterQueue) DeepCopy() *ClusterQueue {
	if in == nil {
		return nil
	}
	out := new(ClusterQueue)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ClusterQueue) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ClusterQueueList) DeepCopyInto(out *ClusterQueueList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]ClusterQueue, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ClusterQueueList.
func (in *ClusterQueueList) DeepCopy() *ClusterQueueList {
	if in == nil {
		return nil
	}
	out := new(ClusterQueueList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ClusterQueueList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ClusterQueuePreemption) DeepCopyInto(out *ClusterQueuePreemption) {
	*out = *in
	if in.BorrowWithinCohort != nil {
		in, out := &in.BorrowWithinCohort, &out.BorrowWithinCohort
		*out = new(BorrowWithinCohort)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ClusterQueuePreemption.
func (in *ClusterQueuePreemption) DeepCopy() *ClusterQueuePreemption {
	if in == nil {
		return nil
	}
	out := new(ClusterQueuePreemption)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ClusterQueueSpec) DeepCopyInto(out *ClusterQueueSpec) {
	*out = *in
	if in.ResourceGroups != nil {
		in, out := &in.ResourceGroups, &out.ResourceGroups
		*out = make([]ResourceGroup, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.NamespaceSelector != nil {
		in, out := &in.NamespaceSelector, &out.NamespaceSelector
		*out = new(metav1.LabelSelector)
		(*in).DeepCopyInto(*out)
	}
	if in.FlavorFungibility != nil {
		in, out := &in.FlavorFungibility, &out.FlavorFungibility
		*out = new(FlavorFungibility)
		(*in).DeepCopyInto(*out)
	}
	if in.Preemption != nil {
		in, out := &in.Preemption, &out.Preemption
		*out = new(ClusterQueuePreemption)
		(*in).DeepCopyInto(*out)
	}
	if in.StopPolicy != nil {
		in, out := &in.StopPolicy, &out.StopPolicy
		*out = new(StopPolicy)
		**out = **in
	}
	if in.FairSharing != nil {
		in, out := &in.FairSharing, &out.FairSharing
		*out = new(FairSharing)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ClusterQueueSpec.
func (in *ClusterQueueSpec) DeepCopy() *ClusterQueueSpec {
	if in == nil {
		return nil
	}
	out := new(ClusterQueueSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ClusterQueueStatus) DeepCopyInto(out *ClusterQueueStatus) {
	*out = *in
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]metav1.Condition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.FlavorsReservation != nil {
		in, out := &in.FlavorsReservation, &out.FlavorsReservation
		*out = make([]FlavorUsage, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.FlavorsUsage != nil {
		in, out := &in.FlavorsUsage, &out.FlavorsUsage
		*out = make([]FlavorUsage, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.FairSharing != nil {
		in, out := &in.FairSharing, &out.FairSharing
		*out = new(FairSharingStatus)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ClusterQueueStatus.
func (in *ClusterQueueStatus) DeepCopy() *ClusterQueueStatus {
	if in == nil {
		return nil
	}
	out := new(ClusterQueueStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *FairSharing) DeepCopyInto(out *FairSharing) {
	*out = *in
	if in.Weight != nil {
		in, out := &in.Weight, &out.Weight
		x := (*in).DeepCopy()
		*out = &x
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new FairSharing.
func (in *FairSharing) DeepCopy() *FairSharing {
	if in == nil {
		return nil
	}
	out := new(FairSharing)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *FairSharingStatus) DeepCopyInto(out *FairSharingStatus) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new FairSharingStatus.
func (in *FairSharingStatus) DeepCopy() *FairSharingStatus {
	if in == nil {
		return nil
	}
	out := new(FairSharingStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *FlavorFungibility) DeepCopyInto(out *FlavorFungibility) {
	*out = *in
	if in.Preference != nil {
		in, out := &in.Preference, &out.Preference
		*out = new(FlavorFungibilityPreference)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new FlavorFungibility.
func (in *FlavorFungibility) DeepCopy() *FlavorFungibility {
	if in == nil {
		return nil
	}
	out := new(FlavorFungibility)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *FlavorQuotas) DeepCopyInto(out *FlavorQuotas) {
	*out = *in
	if in.Resources != nil {
		in, out := &in.Resources, &out.Resources
		*out = make([]ResourceQuota, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new FlavorQuotas.
func (in *FlavorQuotas) DeepCopy() *FlavorQuotas {
	if in == nil {
		return nil
	}
	out := new(FlavorQuotas)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *FlavorUsage) DeepCopyInto(out *FlavorUsage) {
	*out = *in
	if in.Resources != nil {
		in, out := &in.Resources, &out.Resources
		*out = make([]ResourceUsage, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new FlavorUsage.
func (in *FlavorUsage) DeepCopy() *FlavorUsage {
	if in == nil {
		return nil
	}
	out := new(FlavorUsage)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LocalQueue) DeepCopyInto(out *LocalQueue) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LocalQueue.
func (in *LocalQueue) DeepCopy() *LocalQueue {
	if in == nil {
		return nil
	}
	out := new(LocalQueue)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *LocalQueue) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LocalQueueList) DeepCopyInto(out *LocalQueueList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]LocalQueue, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LocalQueueList.
func (in *LocalQueueList) DeepCopy() *LocalQueueList {
	if in == nil {
		return nil
	}
	out := new(LocalQueueList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *LocalQueueList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LocalQueueSpec) DeepCopyInto(out *LocalQueueSpec) {
	*out = *in
	if in.StopPolicy != nil {
		in, out := &in.StopPolicy, &out.StopPolicy
		*out = new(StopPolicy)
		**out = **in
	}
	if in.FairSharing != nil {
		in, out := &in.FairSharing, &out.FairSharing
		*out = new(FairSharing)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LocalQueueSpec.
func (in *LocalQueueSpec) DeepCopy() *LocalQueueSpec {
	if in == nil {
		return nil
	}
	out := new(LocalQueueSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LocalQueueStatus) DeepCopyInto(out *LocalQueueStatus) {
	*out = *in
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]metav1.Condition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.FlavorsReservation != nil {
		in, out := &in.FlavorsReservation, &out.FlavorsReservation
		*out = make([]LocalQueueFlavorUsage, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.FlavorsUsage != nil {
		in, out := &in.FlavorsUsage, &out.FlavorsUsage
		*out = make([]LocalQueueFlavorUsage, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LocalQueueStatus.
func (in *LocalQueueStatus) DeepCopy() *LocalQueueStatus {
	if in == nil {
		return nil
	}
	out := new(LocalQueueStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PodSet) DeepCopyInto(out *PodSet) {
	*out = *in
	in.Template.DeepCopyInto(&out.Template)
	if in.MinCount != nil {
		in, out := &in.MinCount, &out.MinCount
		*out = new(int32)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PodSet.
func (in *PodSet) DeepCopy() *PodSet {
	if in == nil {
		return nil
	}
	out := new(PodSet)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PodSetAssignment) DeepCopyInto(out *PodSetAssignment) {
	*out = *in
	if in.Flavors != nil {
		in, out := &in.Flavors, &out.Flavors
		*out = make(map[corev1.ResourceName]ResourceFlavorReference, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	if in.ResourceUsage != nil {
		in, out := &in.ResourceUsage, &out.ResourceUsage
		*out = make(corev1.ResourceList, len(*in))
		for key, val := range *in {
			(*out)[key] = val.DeepCopy()
		}
	}
	if in.Count != nil {
		in, out := &in.Count, &out.Count
		*out = new(int32)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PodSetAssignment.
func (in *PodSetAssignment) DeepCopy() *PodSetAssignment {
	if in == nil {
		return nil
	}
	out := new(PodSetAssignment)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PriorityClassRef) DeepCopyInto(out *PriorityClassRef) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PriorityClassRef.
func (in *PriorityClassRef) DeepCopy() *PriorityClassRef {
	if in == nil {
		return nil
	}
	out := new(PriorityClassRef)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ReclaimablePod) DeepCopyInto(out *ReclaimablePod) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ReclaimablePod.
func (in *ReclaimablePod) DeepCopy() *ReclaimablePod {
	if in == nil {
		return nil
	}
	out := new(ReclaimablePod)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *RequeueState) DeepCopyInto(out *RequeueState) {
	*out = *in
	if in.Count != nil {
		in, out := &in.Count, &out.Count
		*out = new(int32)
		**out = **in
	}
	if in.RequeueAt != nil {
		in, out := &in.RequeueAt, &out.RequeueAt
		*out = (*in).DeepCopy()
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new RequeueState.
func (in *RequeueState) DeepCopy() *RequeueState {
	if in == nil {
		return nil
	}
	out := new(RequeueState)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ResourceFlavor) DeepCopyInto(out *ResourceFlavor) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ResourceFlavor.
func (in *ResourceFlavor) DeepCopy() *ResourceFlavor {
	if in == nil {
		return nil
	}
	out := new(ResourceFlavor)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ResourceFlavor) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ResourceFlavorList) DeepCopyInto(out *ResourceFlavorList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]ResourceFlavor, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ResourceFlavorList.
func (in *ResourceFlavorList) DeepCopy() *ResourceFlavorList {
	if in == nil {
		return nil
	}
	out := new(ResourceFlavorList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ResourceFlavorList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ResourceFlavorSpec) DeepCopyInto(out *ResourceFlavorSpec) {
	*out = *in
	if in.NodeLabels != nil {
		in, out := &in.NodeLabels, &out.NodeLabels
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	if in.NodeTaints != nil {
		in, out := &in.NodeTaints, &out.NodeTaints
		*out = make([]corev1.Taint, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.Tolerations != nil {
		in, out := &in.Tolerations, &out.Tolerations
		*out = make([]corev1.Toleration, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ResourceFlavorSpec.
func (in *ResourceFlavorSpec) DeepCopy() *ResourceFlavorSpec {
	if in == nil {
		return nil
	}
	out := new(ResourceFlavorSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ResourceGroup) DeepCopyInto(out *ResourceGroup) {
	*out = *in
	if in.CoveredResources != nil {
		in, out := &in.CoveredResources, &out.CoveredResources
		*out = make([]corev1.ResourceName, len(*in))
		copy(*out, *in)
	}
	if in.Flavors != nil {
		in, out := &in.Flavors, &out.Flavors
		*out = make([]FlavorQuotas, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ResourceGroup.
func (in *ResourceGroup) DeepCopy() *ResourceGroup {
	if in == nil {
		return nil
	}
	out := new(ResourceGroup)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ResourceQuota) DeepCopyInto(out *ResourceQuota) {
	*out = *in
	out.NominalQuota = in.NominalQuota.DeepCopy()
	if in.BorrowingLimit != nil {
		in, out := &in.BorrowingLimit, &out.BorrowingLimit
		x := (*in).DeepCopy()
		*out = &x
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ResourceQuota.
func (in *ResourceQuota) DeepCopy() *ResourceQuota {
	if in == nil {
		return nil
	}
	out := new(ResourceQuota)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ResourceUsage) DeepCopyInto(out *ResourceUsage) {
	*out = *in
	out.Total = in.Total.DeepCopy()
	out.Borrowed = in.Borrowed.DeepCopy()
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ResourceUsage.
func (in *ResourceUsage) DeepCopy() *ResourceUsage {
	if in == nil {
		return nil
	}
	out := new(ResourceUsage)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Workload) DeepCopyInto(out *Workload) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Workload.
func (in *Workload) DeepCopy() *Workload {
	if in == nil {
		return nil
	}
	out := new(Workload)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *Workload) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *WorkloadList) DeepCopyInto(out *WorkloadList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]Workload, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new WorkloadList.
func (in *WorkloadList) DeepCopy() *WorkloadList {
	if in == nil {
		return nil
	}
	out := new(WorkloadList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *WorkloadList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *WorkloadSpec) DeepCopyInto(out *WorkloadSpec) {
	*out = *in
	if in.PodSets != nil {
		in, out := &in.PodSets, &out.PodSets
		*out = make([]PodSet, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.PriorityClassRef != nil {
		in, out := &in.PriorityClassRef, &out.PriorityClassRef
		*out = new(PriorityClassRef)
		**out = **in
	}
	if in.Priority != nil {
		in, out := &in.Priority, &out.Priority
		*out = new(int32)
		**out = **in
	}
	if in.Active != nil {
		in, out := &in.Active, &out.Active
		*out = new(bool)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new WorkloadSpec.
func (in *WorkloadSpec) DeepCopy() *WorkloadSpec {
	if in == nil {
		return nil
	}
	out := new(WorkloadSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *WorkloadStatus) DeepCopyInto(out *WorkloadStatus) {
	*out = *in
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]metav1.Condition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.Admission != nil {
		in, out := &in.Admission, &out.Admission
		*out = new(Admission)
		(*in).DeepCopyInto(*out)
	}
	if in.RequeueState != nil {
		in, out := &in.RequeueState, &out.RequeueState
		*out = new(RequeueState)
		(*in).DeepCopyInto(*out)
	}
	if in.ReclaimablePods != nil {
		in, out := &in.ReclaimablePods, &out.ReclaimablePods
		*out = make([]ReclaimablePod, len(*in))
		copy(*out, *in)
	}
	if in.AdmissionChecks != nil {
		in, out := &in.AdmissionChecks, &out.AdmissionChecks
		*out = make([]AdmissionCheckState, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new WorkloadStatus.
func (in *WorkloadStatus) DeepCopy() *WorkloadStatus {
	if in == nil {
		return nil
	}
	out := new(WorkloadStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AdmissionChecksStrategy) DeepCopyInto(out *AdmissionChecksStrategy) {
	*out = *in
	if in.AdmissionChecks != nil {
		in, out := &in.AdmissionChecks, &out.AdmissionChecks
		*out = make([]AdmissionCheckStrategyRule, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AdmissionChecksStrategy.
func (in *AdmissionChecksStrategy) DeepCopy() *AdmissionChecksStrategy {
	if in == nil {
		return nil
	}
	out := new(AdmissionChecksStrategy)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AdmissionCheckStrategyRule) DeepCopyInto(out *AdmissionCheckStrategyRule) {
	*out = *in
	if in.OnFlavors != nil {
		in, out := &in.OnFlavors, &out.OnFlavors
		*out = make([]ResourceFlavorReference, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AdmissionCheckStrategyRule.
func (in *AdmissionCheckStrategyRule) DeepCopy() *AdmissionCheckStrategyRule {
	if in == nil {
		return nil
	}
	out := new(AdmissionCheckStrategyRule)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LocalQueueFlavorUsage) DeepCopyInto(out *LocalQueueFlavorUsage) {
	*out = *in
	if in.Resources != nil {
		in, out := &in.Resources, &out.Resources
		*out = make([]LocalQueueResourceUsage, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LocalQueueFlavorUsage.
func (in *LocalQueueFlavorUsage) DeepCopy() *LocalQueueFlavorUsage {
	if in == nil {
		return nil
	}
	out := new(LocalQueueFlavorUsage)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *LocalQueueResourceUsage) DeepCopyInto(out *LocalQueueResourceUsage) {
	*out = *in
	out.Total = in.Total.DeepCopy()
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new LocalQueueResourceUsage.
func (in *LocalQueueResourceUsage) DeepCopy() *LocalQueueResourceUsage {
	if in == nil {
		return nil
	}
	out := new(LocalQueueResourceUsage)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PodSetUpdate) DeepCopyInto(out *PodSetUpdate) {
	*out = *in
	if in.Labels != nil {
		in, out := &in.Labels, &out.Labels
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	if in.Annotations != nil {
		in, out := &in.Annotations, &out.Annotations
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	if in.NodeSelector != nil {
		in, out := &in.NodeSelector, &out.NodeSelector
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
	if in.Tolerations != nil {
		in, out := &in.Tolerations, &out.Tolerations
		*out = make([]corev1.Toleration, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PodSetUpdate.
func (in *PodSetUpdate) DeepCopy() *PodSetUpdate {
	if in == nil {
		return nil
	}
	out := new(PodSetUpdate)
	in.DeepCopyInto(out)
	return out
}
