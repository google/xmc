load("@io_bazel_rules_go//go:def.bzl", "go_binary", "go_library")
load("@//:compat.bzl", "compat_go_library")

package(default_visibility = ["//visibility:public"])

go_library(
    name = "utilities",
    srcs = glob(["utilities/*.go"], exclude = ["utilities/*_test.go"]),
    importpath = "github.com/grpc-ecosystem/grpc-gateway/v2/utilities",
)

go_library(
    name = "runtime",
    srcs = glob(["runtime/*.go"], exclude = ["runtime/*_test.go"]),
    importpath = "github.com/grpc-ecosystem/grpc-gateway/v2/runtime",
    deps = [
        ":utilities",
        "@org_golang_google_grpc//:go_default_library",
        "@org_golang_google_grpc//codes:go_default_library",
        "@org_golang_google_grpc//grpclog:go_default_library",
        "@org_golang_google_grpc//metadata:go_default_library",
        "@org_golang_google_grpc//status:go_default_library",
        "@org_golang_google_protobuf//proto:go_default_library",
        "@org_golang_google_protobuf//reflect/protoreflect:go_default_library",
        "@org_golang_google_protobuf//reflect/protoregistry:go_default_library",
        "@org_golang_google_protobuf//types/known/anypb:go_default_library",
        "@org_golang_google_protobuf//types/known/durationpb:go_default_library",
        "@org_golang_google_protobuf//types/known/structpb:go_default_library",
        "@org_golang_google_protobuf//types/known/timestamppb:go_default_library",
        "@org_golang_google_protobuf//types/known/wrapperspb:go_default_library",
        "@org_golang_google_genproto_googleapis_api//annotations:go_default_library",
        ":httprule",
        "@org_golang_google_protobuf//encoding/protojson:go_default_library",
        "@org_golang_google_protobuf//types/known/fieldmaskpb:go_default_library",
        "@org_golang_google_genproto_googleapis_api//httpbody:go_default_library",
        "@org_golang_google_grpc//health/grpc_health_v1:go_default_library",
    ],
)

go_library(
    name = "apiconfig",
    srcs = glob(["internal/descriptor/apiconfig/*.go"], exclude = ["internal/descriptor/apiconfig/*_test.go"]),
    importpath = "github.com/grpc-ecosystem/grpc-gateway/v2/internal/descriptor/apiconfig",
    deps = [
        "@org_golang_google_protobuf//reflect/protoreflect:go_default_library",
        "@org_golang_google_protobuf//runtime/protoimpl:go_default_library",
        "@org_golang_google_protobuf//types/known/anypb:go_default_library",
        "@org_golang_google_genproto_googleapis_api//annotations:go_default_library",
    ],
)

go_library(
    name = "openapiconfig",
    srcs = glob(["internal/descriptor/openapiconfig/*.go"], exclude = ["internal/descriptor/openapiconfig/*_test.go"]),
    importpath = "github.com/grpc-ecosystem/grpc-gateway/v2/internal/descriptor/openapiconfig",
    deps = [
        ":options",
        "@org_golang_google_protobuf//reflect/protoreflect:go_default_library",
        "@org_golang_google_protobuf//runtime/protoimpl:go_default_library",
    ],
)

go_library(
    name = "options",
    srcs = glob(["protoc-gen-openapiv2/options/*.go"], exclude = ["protoc-gen-openapiv2/options/*_test.go"]),
    importpath = "github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2/options",
    deps = [
        "@org_golang_google_protobuf//reflect/protoreflect:go_default_library",
        "@org_golang_google_protobuf//runtime/protoimpl:go_default_library",
        "@org_golang_google_protobuf//types/known/anypb:go_default_library",
        "@org_golang_google_protobuf//types/descriptorpb:go_default_library",
        "@org_golang_google_protobuf//types/known/structpb:go_default_library",
    ],
)

go_library(
    name = "casing",
    srcs = glob(["internal/casing/*.go"], exclude = ["internal/casing/*_test.go"]),
    importpath = "github.com/grpc-ecosystem/grpc-gateway/v2/internal/casing",
)

go_library(
    name = "httprule",
    srcs = glob(["internal/httprule/*.go"], exclude = ["internal/httprule/*_test.go"]),
    importpath = "github.com/grpc-ecosystem/grpc-gateway/v2/internal/httprule",
    deps = [
        ":utilities",
    ],
)

go_library(
    name = "codegenerator",
    srcs = glob(["internal/codegenerator/*.go"], exclude = ["internal/codegenerator/*_test.go"]),
    importpath = "github.com/grpc-ecosystem/grpc-gateway/v2/internal/codegenerator",
    deps = [
        "@org_golang_google_protobuf//proto:go_default_library",
        "@org_golang_google_protobuf//types/pluginpb:go_default_library",
        "@org_golang_google_protobuf//compiler/protogen:go_default_library",
        "@org_golang_google_protobuf//types/descriptorpb:go_default_library",
    ],
)

compat_go_library(
    name = "descriptor",
    srcs = glob(["internal/descriptor/*.go"], exclude = [
        "internal/descriptor/*_test.go",
        "internal/descriptor/grpc_api_configuration.go",
        "internal/descriptor/openapi_configuration.go",
    ]),
    importpath = "github.com/grpc-ecosystem/grpc-gateway/v2/internal/descriptor",
    deps = [
        ":apiconfig",
        ":openapiconfig",
        ":options",
        ":casing",
        ":httprule",
        ":codegenerator",
        "@org_golang_google_protobuf//proto:go_default_library",
        "@org_golang_google_protobuf//compiler/protogen:go_default_library",
        "@org_golang_google_protobuf//types/descriptorpb:go_default_library",
        "@org_golang_google_protobuf//types/pluginpb:go_default_library",
        "@org_golang_google_grpc//grpclog:go_default_library",
        "@org_golang_google_genproto_googleapis_api//annotations:go_default_library",
        "@org_golang_x_text//cases:go_default_library",
        "@org_golang_x_text//language:go_default_library",
        "@org_golang_google_protobuf//encoding/protojson:go_default_library",
    ],
)

go_library(
    name = "generator",
    srcs = glob(["internal/generator/*.go"], exclude = ["internal/generator/*_test.go"]),
    importpath = "github.com/grpc-ecosystem/grpc-gateway/v2/internal/generator",
    deps = [
        ":descriptor",
        ":codegenerator",
        "@org_golang_google_protobuf//compiler/protogen:go_default_library",
    ],
)

go_library(
    name = "gengateway",
    srcs = glob(["protoc-gen-grpc-gateway/internal/gengateway/*.go"], exclude = ["protoc-gen-grpc-gateway/internal/gengateway/*_test.go"]),
    importpath = "github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway/internal/gengateway",
    deps = [
        ":descriptor",
        ":utilities",
        ":generator",
        ":casing",
        "@org_golang_google_protobuf//compiler/protogen:go_default_library",
        "@org_golang_google_protobuf//types/descriptorpb:go_default_library",
        "@org_golang_google_protobuf//proto:go_default_library",
        "@org_golang_google_protobuf//types/pluginpb:go_default_library",
        "@org_golang_google_grpc//grpclog:go_default_library",
    ],
)

go_binary(
    name = "protoc-gen-grpc-gateway",
    srcs = glob(["protoc-gen-grpc-gateway/*.go"], exclude = ["protoc-gen-grpc-gateway/*_test.go"]),
    deps = [
        ":descriptor",
        ":gengateway",
        ":codegenerator",
        "@org_golang_google_protobuf//compiler/protogen:go_default_library",
        "@org_golang_google_protobuf//types/pluginpb:go_default_library",
        "@org_golang_google_grpc//grpclog:go_default_library",
    ],
)

# Also expose runtime as go_default_library for general compatibility aliases
alias(
    name = "go_default_library",
    actual = ":runtime",
)
