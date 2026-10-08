// Copyright 2025 Google LLC
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

//go:build dummy
// +build dummy

package compat

import (
	_ "api/filter/filter_expression_go_proto"
	_ "api/filter/go/filter"
	_ "api/filter/go/filtervisitor"
	_ "api/filter/go/queryfilterbase"
	_ "cloud.google.com/go/compute/metadata"
	_ "cloud.google.com/go/pubsub"
	_ "cloud.google.com/go/pubsub/pstest"
	_ "cloud.google.com/go/spanner"
	_ "github.com/evanphx/json-patch/v5"
	_ "github.com/fsnotify/fsnotify"
	_ "github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/source"
	_ "github.com/google/go-cmp/cmp"
	_ "github.com/google/go-cmp/cmp/cmpopts"
	_ "github.com/google/uuid"
	_ "github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	_ "github.com/kylelemons/godebug/diff"
	_ "github.com/prometheus/client_golang/prometheus"
	_ "github.com/prometheus/client_golang/prometheus/testutil"
	_ "github.com/prometheus/client_model/go"
	_ "github.com/redis/go-redis/v9"
	_ "github.com/rs/cors"
	_ "github.com/tink-crypto/tink-go/v2/jwt"
	_ "github.com/tink-crypto/tink-go/v2/keyset"
	_ "golang.org/x/oauth2/google"
	_ "golang.org/x/sync/errgroup"
	_ "golang.org/x/text/runes"
	_ "golang.org/x/text/transform"
	_ "gomodules.xyz/jsonpatch/v2"
	_ "google.golang.org/api/iamcredentials/v1"
	_ "google.golang.org/api/iterator"
	_ "google.golang.org/api/option"
	_ "google.golang.org/genproto/googleapis/api/annotations"
	_ "google.golang.org/genproto/googleapis/rpc/status"
	_ "google.golang.org/genproto/googleapis/spanner/admin/database/v1"
	_ "google.golang.org/genproto/googleapis/spanner/admin/instance/v1"
	_ "google.golang.org/protobuf/types/known/anypb"
	_ "k8s.io/api/batch/v1"
	_ "k8s.io/api/core/v1"
	_ "k8s.io/apimachinery/pkg/api/resource"
	_ "k8s.io/client-go/discovery"
	_ "k8s.io/client-go/kubernetes/typed/core/v1"
	_ "k8s.io/client-go/rest"
	_ "sigs.k8s.io/controller-runtime"
	_ "sigs.k8s.io/controller-runtime/pkg/client/fake"
	_ "sigs.k8s.io/controller-runtime/pkg/manager"
)
