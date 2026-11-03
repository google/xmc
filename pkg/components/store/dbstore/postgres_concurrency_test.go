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

// Concurrency tests of the component store on PostgreSQL, where CMS transactions run at
// SERIALIZABLE isolation (storage AGENTS.md rule 5). Each test overlaps two transactions: both take
// their first catalog or fence read before either writes, so PostgreSQL must abort one with a
// serialization failure, and the runner must retry it against the committed state.
package dbstore_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/pkg/components/store"
	"xmanager_cloud/pkg/components/store/dbstore"
	dbstoretest "xmanager_cloud/pkg/components/store/dbstore/testing"
	"xmanager_cloud/pkg/components/store/dependency"
)

// barrier blocks its first parties callers of wait until all of them have arrived; later calls,
// such as those of retried transactions, return at once.
type barrier struct {
	mu      sync.Mutex
	pending int
	all     chan struct{}
}

func newBarrier(parties int) *barrier {
	return &barrier{pending: parties, all: make(chan struct{})}
}

func (r *barrier) wait(ctx context.Context) error {
	r.mu.Lock()
	if r.pending == 0 {
		r.mu.Unlock()
		return nil
	}
	r.pending--
	if r.pending == 0 {
		close(r.all)
	}
	r.mu.Unlock()
	select {
	case <-r.all:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// overlappingStorage makes the first read of each serializable transaction wait at meet, and
// records every transaction attempt with the error its callback returned.
type overlappingStorage struct {
	storage.Storage
	meet     *barrier
	mu       sync.Mutex
	attempts []error
}

func (s *overlappingStorage) RunInSerializableTransaction(ctx context.Context, fn func(ctx context.Context, tx *storage.Transaction) error) (time.Time, error) {
	return s.Storage.RunInSerializableTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		tx.Components = meetingTxRepository{ComponentTxRepository: tx.Components, meet: s.meet}
		err := fn(ctx, tx)
		s.mu.Lock()
		s.attempts = append(s.attempts, err)
		s.mu.Unlock()
		return err
	})
}

// assertRetried checks that the overlapping transactions took more attempts than transactions,
// and that every failed attempt was a serialization failure or one of the expected rejections.
func (s *overlappingStorage) assertRetried(t *testing.T, transactions int, allowed ...string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.attempts) <= transactions {
		t.Errorf("%d attempts for %d overlapping transactions, want a retry", len(s.attempts), transactions)
	}
	for _, err := range s.attempts {
		if err != nil && !pgdb.IsRetryableTxError(err) && !hasReasonOrPrefix(err, allowed) {
			t.Errorf("attempt error = %v, want a serialization failure or one of %v", err, allowed)
		}
	}
}

// hasReasonOrPrefix reports whether err carries one of wants as its ErrorInfo reason or message prefix.
func hasReasonOrPrefix(err error, wants []string) bool {
	for _, want := range wants {
		if store.ErrorInfoReason(err) == want || strings.HasPrefix(status.Convert(err).Message(), want) {
			return true
		}
	}
	return false
}

// meetingTxRepository waits at meet after the catalog and fence reads that start CMS transactions.
type meetingTxRepository struct {
	storage.ComponentTxRepository
	meet *barrier
}

func (r meetingTxRepository) ListActiveComponents(ctx context.Context) (map[string]*store.ComponentRecord, error) {
	catalog, err := r.ComponentTxRepository.ListActiveComponents(ctx)
	if err != nil {
		return nil, err
	}
	return catalog, r.meet.wait(ctx)
}

func (r meetingTxRepository) GetActiveComponent(ctx context.Context, canonicalName string) (*store.ComponentRecord, error) {
	rec, err := r.ComponentTxRepository.GetActiveComponent(ctx, canonicalName)
	if err != nil {
		return nil, err
	}
	return rec, r.meet.wait(ctx)
}

func (r meetingTxRepository) ReadFence(ctx context.Context) (*store.ClusterIdentity, error) {
	fence, err := r.ComponentTxRepository.ReadFence(ctx)
	if err != nil {
		return nil, err
	}
	return fence, r.meet.wait(ctx)
}

// overlappingStores holds the stores and pool of one fresh PostgreSQL database: seed is a plain
// store for seeding, and the next two transactions of store overlap through overlap.
type overlappingStores struct {
	seed, store *dbstore.Store
	overlap     *overlappingStorage
	pool        *pgxpool.Pool
}

// newOverlappingStores returns overlappingStores on one fresh PostgreSQL database.
func newOverlappingStores(ctx context.Context, t *testing.T) overlappingStores {
	t.Helper()
	pool, st, _ := dbstoretest.OpenPostgres(ctx, t)
	seedStore, err := dbstore.New(st)
	if err != nil {
		t.Fatalf("dbstore.New(seed) failed: %v", err)
	}
	overlap := &overlappingStorage{Storage: st, meet: newBarrier(2)}
	overlappingStore, err := dbstore.New(overlap)
	if err != nil {
		t.Fatalf("dbstore.New(overlap) failed: %v", err)
	}
	return overlappingStores{seed: seedStore, store: overlappingStore, overlap: overlap, pool: pool}
}

// runConcurrently runs ops in parallel goroutines and returns their errors in order.
func runConcurrently(ops ...func() error) []error {
	errs := make([]error, len(ops))
	var wg sync.WaitGroup
	for i, op := range ops {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = op()
		}()
	}
	wg.Wait()
	return errs
}

func TestPostgresStore_ConcurrentIntentsFormingCycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f := newOverlappingStores(ctx, t)
	seed, s, overlap := f.seed, f.store, f.overlap
	for _, name := range []string{"cycle-x", "cycle-y"} {
		if _, err := seed.SetComponentIntent(ctx, name, intentSpec("v1.0.0"), nil, 1); err != nil {
			t.Fatalf("seed SetComponentIntent(%s) error = %v", name, err)
		}
	}

	errs := runConcurrently(
		func() error {
			_, err := s.SetComponentIntent(ctx, "cycle-x", intentSpec("v1.1.0", "cycle-y"), nil, 0)
			return err
		},
		func() error {
			_, err := s.SetComponentIntent(ctx, "cycle-y", intentSpec("v1.1.0", "cycle-x"), nil, 0)
			return err
		},
	)
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("SetComponentIntent errors = %v, want exactly one success", errs)
	}
	for _, err := range errs {
		if err != nil && store.ErrorInfoReason(err) != store.ReasonDependencyCycle {
			t.Errorf("rejected SetComponentIntent error = %v, want reason %s", err, store.ReasonDependencyCycle)
		}
	}
	overlap.assertRetried(t, 2, store.ReasonDependencyCycle)
}

func TestPostgresStore_ConcurrentDependentCreationAndCascadingDeletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f := newOverlappingStores(ctx, t)
	seed, s, overlap, pool := f.seed, f.store, f.overlap, f.pool
	if _, err := seed.SetComponentIntent(ctx, "cascade-y", intentSpec("v1.0.0"), nil, 1); err != nil {
		t.Fatalf("seed SetComponentIntent(cascade-y) error = %v", err)
	}

	errs := runConcurrently(
		func() error {
			_, err := s.SetComponentIntent(ctx, "cascade-z", intentSpec("v1.0.0", "cascade-y"), nil, 1)
			return err
		},
		func() error {
			return s.ExecuteDeletion(ctx, "cascade-y", componentspb.DeletionPolicy_DELETION_POLICY_DELETE, false)
		},
	)
	if errs[1] != nil {
		t.Errorf("ExecuteDeletion(cascade-y) error = %v, want nil", errs[1])
	}
	if errs[0] != nil && !strings.HasPrefix(status.Convert(errs[0]).Message(), dependency.DependencyNotFoundMessagePrefix) {
		t.Errorf("SetComponentIntent(cascade-z) error = %v, want nil or a missing dependency rejection", errs[0])
	}
	var dangling int64
	err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM ComponentDependencies cd
JOIN Components c ON c.ComponentId = cd.ComponentId JOIN Components d ON d.ComponentId = cd.DependencyComponentId
WHERE c.ActiveName IS NOT NULL AND d.ActiveName IS NULL`).Scan(&dangling)
	if err != nil || dangling != 0 {
		t.Errorf("active components depending on deleted ones = %d (error %v), want 0", dangling, err)
	}
	overlap.assertRetried(t, 2, dependency.DependencyNotFoundMessagePrefix)
}

func TestPostgresStore_ConcurrentFirstFenceDeclarations(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f := newOverlappingStores(ctx, t)
	s, overlap := f.store, f.overlap
	clusters := []store.ClusterIdentity{{UID: prodClusterUID, Name: "prod", Epoch: 1}, {UID: drClusterUID, Name: "dr", Epoch: 1}}

	errs := runConcurrently(
		func() error { _, err := s.DeclareClusterEpoch(ctx, clusters[0]); return err },
		func() error { _, err := s.DeclareClusterEpoch(ctx, clusters[1]); return err },
	)
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("DeclareClusterEpoch errors = %v, want exactly one success", errs)
	}
	for _, err := range errs {
		if err != nil && (status.Code(err) != codes.FailedPrecondition || store.ErrorInfoReason(err) != store.ReasonClusterFenced) {
			t.Errorf("losing DeclareClusterEpoch error = %v, want a %s rejection", err, store.ReasonClusterFenced)
		}
	}
	// Both transactions read no fence before either wrote it, so the losing insert must fail as a
	// serialization failure (40001), not as a unique violation, and be retried.
	overlap.mu.Lock()
	serializationFailures := 0
	for _, err := range overlap.attempts {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgdb.SQLStateSerializationFailure {
			serializationFailures++
		}
		if pgdb.IsUniqueViolation(err) {
			t.Errorf("attempt error = %v, want no unique violation", err)
		}
	}
	overlap.mu.Unlock()
	if serializationFailures != 1 {
		t.Errorf("attempts failing with SQLSTATE %s = %d, want 1", pgdb.SQLStateSerializationFailure, serializationFailures)
	}
	overlap.assertRetried(t, 2, store.ReasonClusterFenced)
}

func TestPostgresStore_ConcurrentObservationAndSoftDeletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f := newOverlappingStores(ctx, t)
	seed, s, overlap, pool := f.seed, f.store, f.overlap, f.pool
	const name, message = "observed-deleted", "observed concurrently"
	seeded := seedIntentComponent(ctx, t, seed, name, intentSpec("v1.0.0"), 1, nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED)
	deletedState := int64(componentspb.ComponentState_COMPONENT_STATE_DELETED)

	errs := runConcurrently(
		func() error {
			_, err := s.UpdateObservedGeneration(ctx, name, 1, nil, componentspb.ComponentState_COMPONENT_STATE_ENABLED, message)
			return err
		},
		func() error {
			return s.ExecuteDeletion(ctx, name, componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN, true)
		},
	)
	if errs[1] != nil {
		t.Fatalf("ExecuteDeletion() error = %v, want nil", errs[1])
	}
	if errs[0] != nil && status.Code(errs[0]) != codes.NotFound {
		t.Errorf("UpdateObservedGeneration() error = %v, want nil or NotFound", errs[0])
	}
	// The deleted row keeps an observation that committed before the deletion.
	var state int64
	var access []byte
	if err := pool.QueryRow(ctx, `SELECT State, AccessPayload FROM Components WHERE ComponentId = $1`, seeded.ComponentID).Scan(&state, &access); err != nil {
		t.Fatalf("query deleted row error = %v", err)
	}
	stored := &componentspb.ComponentAccessStatus{}
	if err := proto.Unmarshal(access, stored); err != nil {
		t.Fatalf("proto.Unmarshal(AccessPayload) error = %v", err)
	}
	if state != deletedState || (errs[0] == nil && stored.GetHealthMessage() != message) {
		t.Errorf("deleted row state = %d, health message = %q; want %d keeping %q when the observation succeeded", state, stored.GetHealthMessage(), deletedState, message)
	}
	overlap.assertRetried(t, 2, "component not found")
}
