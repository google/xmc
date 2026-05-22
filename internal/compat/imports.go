//go:build dummy
// +build dummy

package compat

import (
	_ "cloud.google.com/go/spanner"
	_ "github.com/google/go-cmp/cmp"
	_ "github.com/google/go-cmp/cmp/cmpopts"
	_ "github.com/google/tink/go/jwt"
	_ "github.com/google/tink/go/keyset"
	_ "github.com/google/uuid"
	_ "github.com/kylelemons/godebug/diff"
	_ "github.com/redis/go-redis/v9"
	_ "golang.org/x/oauth2/google"
	_ "google.golang.org/api/iamcredentials/v1"
	_ "google.golang.org/api/iterator"
	_ "google.golang.org/api/option"
	_ "google.golang.org/genproto/googleapis/rpc/status"
	_ "google.golang.org/protobuf/types/known/anypb"
	_ "k8s.io/api/batch/v1"
	_ "k8s.io/api/core/v1"
	_ "k8s.io/apimachinery/pkg/api/resource"
	_ "k8s.io/client-go/discovery"
	_ "k8s.io/client-go/kubernetes/typed/core/v1"
	_ "k8s.io/client-go/rest"
	_ "sigs.k8s.io/controller-runtime/pkg/client/fake"
)
