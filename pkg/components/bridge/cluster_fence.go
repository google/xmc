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
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/logging"
)

const (
	kubeSystemNamespace        = "kube-system"
	defaultFenceRetryDelay     = 5 * time.Second
	defaultFenceDeclareTimeout = 10 * time.Second
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

// ClusterFence declares the cluster's ownership epoch to CMS while leading.
type ClusterFence struct {
	cms CMSClient
	// identity is a private copy set once in NewClusterFence and never mutated afterwards, so it
	// is safe to read concurrently (including from Declare and Start).
	identity *componentspb.ClusterIdentity
	opts     ClusterFenceOptions
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
	return &ClusterFence{cms: cms, identity: id, opts: opts}, nil
}

// NeedLeaderElection makes the manager start the runnable only once it holds the Lease.
func (f *ClusterFence) NeedLeaderElection() bool { return true }

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
			logging.Warn(ctx, "Failed to declare cluster ownership epoch to CMS; retrying", "cluster", f.identity.GetName(), "epoch", f.identity.GetEpoch(), "error", err)
			delay = f.opts.RetryInterval
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

// declareWithTimeout runs Declare bounded by the configured DeclareTimeout.
func (f *ClusterFence) declareWithTimeout(ctx context.Context) error {
	declareCtx, cancel := context.WithTimeout(ctx, f.opts.DeclareTimeout)
	defer cancel()
	return f.Declare(declareCtx)
}

// Declare sends DeclareClusterEpoch to CMS. A declaration that CMS rejects because another
// cluster holds the fence or the epoch regressed returns its FailedPrecondition error, which Start
// logs and retries.
func (f *ClusterFence) Declare(ctx context.Context) error {
	if _, err := f.cms.DeclareClusterEpoch(ctx, &componentspb.DeclareClusterEpochRequest{Cluster: f.Identity()}); err != nil {
		return fmt.Errorf("failed to declare cluster epoch in CMS: %w", err)
	}
	return nil
}
