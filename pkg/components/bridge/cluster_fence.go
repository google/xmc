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
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/pkg/components/store"
)

const (
	kubeSystemNamespace        = "kube-system"
	defaultFenceRetryDelay     = 5 * time.Second
	defaultFenceDeclareTimeout = 10 * time.Second
	conflictRecheckInterval    = 15 * time.Minute
)

// ValidateClusterName validates that name is a valid DNS-1123 label.
func ValidateClusterName(name string) error {
	if errs := validation.IsDNS1123Label(name); len(errs) > 0 {
		return fmt.Errorf("cluster name %q must be a DNS-1123 label: %s", name, strings.Join(errs, "; "))
	}
	return nil
}

// ReadClusterUID fetches the kube-system Namespace UID from the Kubernetes API server.
func ReadClusterUID(ctx context.Context, client kubernetes.Interface) (string, error) {
	if client == nil {
		return "", errors.New("kubernetes client must not be nil")
	}
	ns, err := client.CoreV1().Namespaces().Get(ctx, kubeSystemNamespace, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get %s namespace: %w", kubeSystemNamespace, err)
	}
	if ns == nil {
		return "", fmt.Errorf("%s namespace response is nil", kubeSystemNamespace)
	}
	if uid := string(ns.GetUID()); uid != "" {
		return uid, nil
	}
	return "", fmt.Errorf("%s namespace has an empty UID", kubeSystemNamespace)
}

// ClusterFenceOptions configures periodic cluster ownership epoch declarations.
type ClusterFenceOptions struct {
	HeartbeatInterval time.Duration
	RetryInterval     time.Duration
	// DeclareTimeout bounds each periodic declaration so a hung CMS call cannot stall the
	// heartbeat loop indefinitely.
	DeclareTimeout time.Duration
}

// ClusterFence declares the cluster's ownership epoch to CMS while leading and tracks whether
// this cluster holds the active CMS ownership fence or is in fenced cold standby.
type ClusterFence struct {
	cms CMSClient
	// identity is a private copy set once in NewClusterFence and never mutated afterwards, so it
	// is safe to read without holding mu (including from Declare and Start).
	identity *componentspb.ClusterIdentity
	opts     ClusterFenceOptions
	changes  chan event.GenericEvent

	// mu guards the mutable fence status fields below. It is never held across a CMS call.
	mu                          sync.Mutex
	declared, declareFailed     bool
	fencedReason, fencedMessage string
	// declareSeq numbers declarations in start order; appliedSeq is the newest applied result,
	// so a slow declaration finishing after a newer one cannot overwrite fresher status.
	declareSeq, appliedSeq uint64
	// declareGroup collapses concurrent first declarations from Check into one CMS call.
	declareGroup singleflight.Group
}

var _ manager.LeaderElectionRunnable = (*ClusterFence)(nil)

// NewClusterFence constructs a ClusterFence for identity backed by cms.
func NewClusterFence(cms CMSClient, identity *componentspb.ClusterIdentity, opts ClusterFenceOptions) (*ClusterFence, error) {
	if cms == nil {
		return nil, errors.New("cms client must not be nil")
	}
	if identity == nil || identity.GetUid() == "" || identity.GetEpoch() < 1 {
		return nil, errors.New("cluster identity must have non-empty uid, name and epoch >= 1")
	}
	if err := ValidateClusterName(identity.GetName()); err != nil {
		return nil, err
	}
	if opts.HeartbeatInterval <= 0 {
		opts.HeartbeatInterval = defaultCMSHeartbeatInterval
	}
	if opts.RetryInterval <= 0 {
		opts.RetryInterval = defaultFenceRetryDelay
	}
	if opts.DeclareTimeout <= 0 {
		opts.DeclareTimeout = defaultFenceDeclareTimeout
	}
	id := &componentspb.ClusterIdentity{Uid: identity.GetUid(), Name: identity.GetName(), Epoch: identity.GetEpoch()}
	return &ClusterFence{cms: cms, identity: id, opts: opts, changes: make(chan event.GenericEvent, 1)}, nil
}

// NeedLeaderElection makes the manager start the runnable only once it holds the Lease.
func (f *ClusterFence) NeedLeaderElection() bool { return true }

// Changes returns a channel signaled whenever the cluster fence status transitions.
func (f *ClusterFence) Changes() <-chan event.GenericEvent { return f.changes }

// Identity returns a copy of the cluster identity declared by f.
func (f *ClusterFence) Identity() *componentspb.ClusterIdentity {
	if f == nil || f.identity == nil {
		return nil
	}
	return &componentspb.ClusterIdentity{Uid: f.identity.GetUid(), Name: f.identity.GetName(), Epoch: f.identity.GetEpoch()}
}

// Start declares the cluster epoch immediately and refreshes it periodically until ctx ends.
func (f *ClusterFence) Start(ctx context.Context) error {
	for {
		delay := f.opts.HeartbeatInterval
		if err := f.declareWithTimeout(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			delay = f.retryDelay(ctx, err)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

// retryDelay logs a failed declaration and returns how long Start waits before declaring again.
// CMS keeps rejecting a fenced or regressed declaration (FailedPrecondition) until an operator
// raises the epoch, so it is logged as an error and retried at the heartbeat interval; other
// failures, such as Unavailable or DeadlineExceeded, are retried at RetryInterval.
func (f *ClusterFence) retryDelay(ctx context.Context, err error) time.Duration {
	if status.Code(err) == codes.FailedPrecondition {
		logging.Error(ctx, "CMS rejected the cluster ownership epoch; retrying at the heartbeat interval", "cluster", f.identity.GetName(), "epoch", f.identity.GetEpoch(), "error", err)
		return f.opts.HeartbeatInterval
	}
	logging.Warn(ctx, "Failed to declare cluster ownership epoch to CMS; retrying", "cluster", f.identity.GetName(), "epoch", f.identity.GetEpoch(), "error", err)
	return f.opts.RetryInterval
}

// declareWithTimeout runs Declare bounded by the configured DeclareTimeout.
func (f *ClusterFence) declareWithTimeout(ctx context.Context) error {
	declareCtx, cancel := context.WithTimeout(ctx, f.opts.DeclareTimeout)
	defer cancel()
	return f.Declare(declareCtx)
}

// Declare sends DeclareClusterEpoch to CMS and updates the in-memory fence status. The CMS call
// runs without holding mu, so concurrent Check calls are never blocked behind it.
func (f *ClusterFence) Declare(ctx context.Context) error {
	seq := f.nextDeclareSeq()
	_, err := f.cms.DeclareClusterEpoch(ctx, &componentspb.DeclareClusterEpochRequest{Cluster: f.Identity()})
	reason, msg, fenced := fenceConflictFromError(err)

	f.mu.Lock()
	defer f.mu.Unlock()
	if seq < f.appliedSeq {
		// A declaration started later has already applied a fresher result, so this one's outcome
		// (success or failure) is obsolete.
		return nil
	}
	if err != nil && !fenced {
		f.declareFailed = true
		return fmt.Errorf("failed to declare cluster epoch in CMS: %w", err)
	}
	f.appliedSeq = seq
	f.applyStateLocked(ctx, reason, msg, !f.declared && f.declareFailed)
	return nil
}

// Check returns the current fence conflict reason and message, declaring first if no
// declaration has succeeded yet.
func (f *ClusterFence) Check(ctx context.Context) (string, string, error) {
	if !f.isDeclared() {
		if err := f.declareShared(ctx); err != nil {
			return "", "", err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fencedReason, f.fencedMessage, nil
}

// declareShared runs one bounded declaration for all concurrent callers. The shared call detaches
// from any single caller's cancellation and is bounded by DeclareTimeout, so a hung CMS cannot stall
// reconcile workers.
func (f *ClusterFence) declareShared(ctx context.Context) error {
	_, err, _ := f.declareGroup.Do("declare", func() (any, error) {
		return nil, f.declareWithTimeout(context.WithoutCancel(ctx))
	})
	return err
}

func (f *ClusterFence) nextDeclareSeq() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.declareSeq++
	return f.declareSeq
}

func (f *ClusterFence) isDeclared() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.declared
}

func (f *ClusterFence) applyStateLocked(ctx context.Context, reason, msg string, recoveredFromError bool) {
	if !f.declared || f.fencedReason != reason {
		f.logTransition(ctx, reason, msg)
	}
	changed := (f.declared && f.fencedReason != reason) || (!f.declared && (reason != "" || recoveredFromError))
	f.declared, f.declareFailed = true, false
	f.fencedReason, f.fencedMessage = reason, msg
	if changed {
		select {
		case f.changes <- event.GenericEvent{Object: newComponentObject()}:
		default:
		}
	}
}

// logTransition logs the fence status that a declaration moves this cluster into: once on the
// first declaration and once per change, never on an unchanged heartbeat.
func (f *ClusterFence) logTransition(ctx context.Context, reason, msg string) {
	if reason != "" {
		logging.Error(ctx, "Cluster is fenced out of CMS; holding Components in cold standby until the ownership epoch is raised", "cluster", f.identity.GetName(), "epoch", f.identity.GetEpoch(), "reason", reason, "message", msg)
		return
	}
	logging.Info(ctx, "Cluster holds the CMS ownership fence", "cluster", f.identity.GetName(), "epoch", f.identity.GetEpoch())
}

func fenceConflictFromError(err error) (string, string, bool) {
	info, msg := extractErrorInfo(err)
	if info == nil {
		return "", "", false
	}
	switch info.GetReason() {
	case store.ReasonClusterFenced:
		return ReasonClusterFenced, msg, true
	case store.ReasonClusterEpochRegressed:
		return ReasonClusterEpochRegressed, msg, true
	default:
		return "", "", false
	}
}

// extractErrorInfo returns the components-domain ErrorInfo and the CMS status message of a
// FailedPrecondition status that err is or wraps. It reads the wrapped status itself rather than
// status.FromError, whose message for a wrapped error is the full wrapped error text.
func extractErrorInfo(err error) (*errdetails.ErrorInfo, string) {
	var se interface{ GRPCStatus() *status.Status }
	if !errors.As(err, &se) {
		return nil, ""
	}
	st := se.GRPCStatus()
	if st.Code() != codes.FailedPrecondition {
		return nil, ""
	}
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok && info.GetDomain() == store.ErrorInfoDomain {
			return info, st.Message()
		}
	}
	return nil, ""
}
