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

// Package study defines the StudyEngine abstraction and sentinel errors for study orchestration.
package study

import (
	"context"
	"errors"

	spb "xmanager_cloud/api/protos/experiment"
)

// ErrInvalidStudyConfig marks permanent study configuration or validation rejections that must not
// be retried across reconcile ticks.
var ErrInvalidStudyConfig = errors.New("invalid study config")

// StudyEngine registers and orchestrates hyperparameter optimization studies in an external engine.
type StudyEngine interface {
	// CreateStudy idempotently registers the study identified by xid with cfg and returns the
	// backend study resource name.
	CreateStudy(ctx context.Context, xid int64, cfg *spb.StudyConfig) (string, error)
}
