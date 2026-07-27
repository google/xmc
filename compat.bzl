# Copyright 2025 Google LLC

#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

load("@io_bazel_rules_go//go:def.bzl", "go_library", "go_test")

def _compat_gen_srcs(target_name, srcs):
    compat_srcs = []
    for src in srcs:
        compat_src = target_name + "_" + src.replace(".go", "_compat.go")
        native.genrule(
            name = target_name + "_" + src + "_compat_gen",
            srcs = [src],
            outs = [compat_src],
            cmd = """
python3 -c '
import re, sys
content = open("$<").read()
content = content.replace("golang/k8s_io/apimachinery/v/v0_23/pkg/", "k8s.io/apimachinery/pkg/")
content = content.replace("golang/k8s_io/client_go/v/v0_23/", "k8s.io/client-go/")
content = content.replace("golang/sigs_k8s_io/controller_runtime/v/v0_11/", "sigs.k8s.io/controller-runtime/")
content = content.replace("golang/k8s_io/api/v/v0_23/", "k8s.io/api/")
content = content.replace("sigs.k8s.io/controller-runtime/controllerruntime", "sigs.k8s.io/controller-runtime")
content = content.replace("xmanager_cloud/experiment_state_server/proto/api_go_grpc_proto", "xmanager_cloud/experiment_state_server/proto/api_go_proto")
content = content.replace("xmanager_cloud/xid_service/proto/api_go_grpc_proto", "xmanager_cloud/xid_service/proto/api_go_proto")
content = content.replace("golang/prometheus/client/prometheus/promauto/promauto", "github.com/prometheus/client_golang/prometheus/promauto")
content = content.replace("golang/prometheus/client/prometheus/prometheus", "github.com/prometheus/client_golang/prometheus")
content = content.replace("golang/prometheus/client/prometheus/testutil/testutil", "github.com/prometheus/client_golang/prometheus/testutil")
content = content.replace("golang/prometheus/client/prometheus/promhttp/promhttp", "github.com/prometheus/client_golang/prometheus/promhttp")
content = content.replace("prometheus_model/metrics_go_proto", "github.com/prometheus/client_model/go")
content = content.replace("xmanager_cloud/kubernetes/reconciler/shared/shared", "xmanager_cloud/cmd/reconciler/shared")
content = content.replace("xmanager_cloud/kubernetes/reconciler/clusterqueue/clusterqueue", "xmanager_cloud/cmd/reconciler/clusterqueue")
content = content.replace("protobuf/timestamp_go_proto", "google.golang.org/protobuf/types/known/timestamppb")
content = content.replace("protobuf/any_go_proto", "google.golang.org/protobuf/types/known/anypb")
content = content.replace("rpc/status_go_proto", "google.golang.org/genproto/googleapis/rpc/status")
content = content.replace("golang/cloud_google_com/go/spanner/v/v1/admin/database/apiv1/database", "cloud.google.com/go/spanner/admin/database/apiv1")
content = content.replace("golang/cloud_google_com/go/spanner/v/v1/spanner", "cloud.google.com/go/spanner")
content = content.replace("golang/github_com/golang_migrate/migrate/v/v4/migrate", "github.com/golang-migrate/migrate/v4")
content = content.replace("golang/github_com/golang_migrate/migrate/v/v4/source/iofs/iofs", "github.com/golang-migrate/migrate/v4/source/iofs")
content = content.replace("golang/github_com/golang_migrate/migrate/v/v4/source/source", "github.com/golang-migrate/migrate/v4/source")
content = content.replace("golang/github_com/golang_migrate/migrate/v/v4/database/database", "github.com/golang-migrate/migrate/v4/database")
content = content.replace("golang/github_com/golang_migrate/migrate/v/v4/database/stub/stub", "github.com/golang-migrate/migrate/v4/database/stub")
content = content.replace("golang/github_com/golang_migrate/migrate/v/v4/source/stub/stub", "github.com/golang-migrate/migrate/v4/source/stub")
content = content.replace("xmanager_cloud/storage/spanner/emulator/emulator", "xmanager_cloud/internal/storage/spanner/emulator")
content = content.replace("xmanager_cloud/storage/spanner/migrations/version", "xmanager_cloud/internal/storage/spanner/migrations/version")
content = content.replace("golang/github_com/golang_migrate/migrate/v/v4/database/spanner/spanner", "github.com/golang-migrate/migrate/v4/database/spanner")
content = content.replace("spanner/admin/database/v1/spanner_database_admin_go_proto", "google.golang.org/genproto/googleapis/spanner/admin/database/v1")
content = content.replace("spanner/admin/instance/v1/spanner_instance_admin_go_proto", "google.golang.org/genproto/googleapis/spanner/admin/instance/v1")
content = content.replace("cloud_spanner_emulator/public/go/emulator", "xmanager_cloud/internal/storage/spanner/emulator")
content = content.replace("golang/cloud_google_com/go/spanner/v/v1/admin/instance/apiv1/instance", "cloud.google.com/go/spanner/admin/instance/apiv1")
content = content.replace("golang/protobuf/v2/reflect/protodesc/protodesc", "google.golang.org/protobuf/reflect/protodesc")
content = content.replace("protobuf/empty_go_proto", "google.golang.org/protobuf/types/known/emptypb")
content = content.replace("golang/google_api/option/option", "google.golang.org/api/option")
content = content.replace("golang/google_api/iterator/iterator", "google.golang.org/api/iterator")
content = content.replace("golang/k8s_io/client_go/v/v0_23/dynamic/dynamic", "k8s.io/client-go/dynamic")
content = content.replace("golang/k8s_io/client_go/v/v0_23/kubernetes/kubernetes", "k8s.io/client-go/kubernetes")
content = content.replace("golang/k8s_io/client_go/v/v0_23/rest/rest", "k8s.io/client-go/rest")
content = content.replace("golang/k8s_io/client_go/v/v0_23/dynamic/fake/fake", "k8s.io/client-go/dynamic/fake")
content = content.replace("golang/k8s_io/client_go/v/v0_23/kubernetes/fake/fake", "k8s.io/client-go/kubernetes/fake")
content = content.replace("xmanager_cloud/kubernetes/actuator/k8s_client/preconditions_validator/preconditionsvalidator", "xmanager_cloud/cmd/actuator/k8s_client/preconditions_validator")
content = content.replace("xmanager_cloud/toollibs/execution_environments/executionenvironments", "xmanager_cloud/internal/toollibs/execution_environments")
content = content.replace("golang/protobuf/v2/encoding/prototext/prototext", "google.golang.org/protobuf/encoding/prototext")
content = content.replace("golang/godebug/diff/diff", "github.com/kylelemons/godebug/diff")
content = content.replace("xmanager_cloud/query_generator/normalizer/normalizer", "xmanager_cloud/internal/query_generator/normalizer")
content = content.replace("go.yaml.in/yaml/v3", "gopkg.in/yaml.v3")
content = content.replace("golang/cloud_google_com/go/pubsub/v/v1/pubsub", "cloud.google.com/go/pubsub")
content = content.replace("golang/cloud_google_com/go/pubsub/v/v1/pstest/pstest", "cloud.google.com/go/pubsub/pstest")
content = content.replace("xmanager_cloud/kubernetes/actuator/k8s_client/k8sclient", "xmanager_cloud/cmd/actuator/k8s_client")
content = content.replace("golang/cloud_google_com/go/compute/metadata/v/v0/metadata", "cloud.google.com/go/compute/metadata")
content = content.replace("xmanager_cloud/kubernetes/reconciler/jobset/jobset", "xmanager_cloud/cmd/reconciler/jobset")
content = content.replace("xmanager_cloud/kubernetes/reconciler/kueue_sync/kueuesync", "xmanager_cloud/cmd/reconciler/kueue_sync")
content = content.replace("xmanager_cloud/kubernetes/reconciler/workload/workload", "xmanager_cloud/cmd/reconciler/workload")
content = content.replace("xmanager_cloud/kubernetes/actuator/workunittaskhandler", "xmanager_cloud/cmd/actuator/workunittaskhandler")
content = content.replace("xmanager_cloud/kubernetes/common/labels", "xmanager_cloud/internal/kubernetes/common/labels")
content = content.replace("golang/google_api/idtoken/idtoken", "google.golang.org/api/idtoken")
content = content.replace("tink/go/jwt/jwt", "github.com/tink-crypto/tink-go/v2/jwt")
content = content.replace("xmanager_cloud/auth/auth", "xmanager_cloud/internal/auth")
content = content.replace("xmanager_cloud/experiment_state_server/config", "xmanager_cloud/cmd/experiment_state_server/config")
content = content.replace("xmanager_cloud/xid_service/config", "xmanager_cloud/cmd/xid_service/config")
content = content.replace("xmanager_cloud/xid_service/server/server", "xmanager_cloud/cmd/xid_service/server")
content = content.replace("golang/grpc/credentials/insecure/insecure", "google.golang.org/grpc/credentials/insecure")
content = content.replace("golang/grpc/reflection/reflection", "google.golang.org/grpc/reflection")
content = content.replace("xmanager_cloud/storage/spanner/spanner", "xmanager_cloud/internal/storage/spanner")
content = content.replace("go/flag", "flag")
content = content.replace("go:embed xmanager_cloud/experiment_state_server/proto/ess_messages.pb", "go:embed ess_messages.pb")
content = content.replace("golang/grpc/grpc", "google.golang.org/grpc")
content = content.replace("golang/grpc/health/health", "google.golang.org/grpc/health")
content = content.replace("grpc_proto/health_go_grpc", "google.golang.org/grpc/health/grpc_health_v1")
content = content.replace("grpc_proto/health_go_proto", "google.golang.org/grpc/health/grpc_health_v1")
content = content.replace("golang/protobuf/v2/encoding/protojson/protojson", "google.golang.org/protobuf/encoding/protojson")
content = content.replace("golang/google_api/cloudidentity/v1beta1/cloudidentity", "google.golang.org/api/cloudidentity/v1beta1")
content = content.replace("golang/go_sync/errgroup/errgroup", "golang.org/x/sync/errgroup")
content = content.replace("golang/cors/cors", "github.com/rs/cors")
content = content.replace("golang/github_com/grpc_ecosystem/grpc_gateway/v/v2/runtime/runtime", "github.com/grpc-ecosystem/grpc-gateway/v2/runtime")
content = content.replace("golang/grpc/credentials/credentials", "google.golang.org/grpc/credentials")
if "func Load() Config {" in content:
    if "import (" in content and "\\"os\\"" not in content:
        content = content.replace("import (", "import (\\n\\t\\"os\\"")
    if "import (" in content and "\\"strings\\"" not in content:
        content = content.replace("import (", "import (\\n\\t\\"strings\\"")
    content = content.replace("func Load() Config {", "func Load() Config {\\n\\t{\\n\\t\\tregisteredFlags := make(map[string]bool)\\n\\t\\tflag.CommandLine.VisitAll(func(f *flag.Flag) {\\n\\t\\t\\tregisteredFlags[f.Name] = true\\n\\t\\t})\\n\\t\\tfilteredArgs := make([]string, 0, len(os.Args))\\n\\t\\tif len(os.Args) > 0 {\\n\\t\\t\\tfilteredArgs = append(filteredArgs, os.Args[0])\\n\\t\\t}\\n\\t\\tskipNext := false\\n\\t\\tfor i := 1; i < len(os.Args); i++ {\\n\\t\\t\\tif skipNext {\\n\\t\\t\\t\\tskipNext = false\\n\\t\\t\\t\\tcontinue\\n\\t\\t\\t}\\n\\t\\t\\targ := os.Args[i]\\n\\t\\t\\tif !strings.HasPrefix(arg, \\\"-\\\") {\\n\\t\\t\\t\\tfilteredArgs = append(filteredArgs, arg)\\n\\t\\t\\t\\tcontinue\\n\\t\\t\\t}\\n\\t\\t\\tvar name string\\n\\t\\t\\tvar hasValue bool\\n\\t\\t\\tif strings.Contains(arg, \\\"=\\\") {\\n\\t\\t\\t\\tparts := strings.SplitN(arg, \\\"=\\\", 2)\\n\\t\\t\\t\\tname = parts[0]\\n\\t\\t\\t\\thasValue = true\\n\\t\\t\\t} else {\\n\\t\\t\\t\\tname = arg\\n\\t\\t\\t}\\n\\t\\t\\tname = strings.TrimPrefix(name, \\\"--\\\")\\n\\t\\t\\tname = strings.TrimPrefix(name, \\\"-\\\")\\n\\t\\t\\tif registeredFlags[name] {\\n\\t\\t\\t\\tfilteredArgs = append(filteredArgs, arg)\\n\\t\\t\\t\\tvar isBool bool\\n\\t\\t\\t\\tif f := flag.CommandLine.Lookup(name); f != nil {\\n\\t\\t\\t\\t\tif bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {\\n\\t\\t\\t\\t\t\tisBool = true\\n\\t\\t\\t\\t\t}\\n\\t\\t\\t\\t}\\n\\t\\t\\t\\tif !hasValue && i+1 < len(os.Args) && !strings.HasPrefix(os.Args[i+1], \\\"-\\\") {\\n\\t\\t\\t\\t\tif !isBool || os.Args[i+1] == \\\"true\\\" || os.Args[i+1] == \\\"false\\\" {\\n\\t\\t\\t\\t\t\tfilteredArgs = append(filteredArgs, os.Args[i+1])\\n\\t\\t\\t\\t\t\tskipNext = true\\n\\t\\t\\t\\t\t}\\n\\t\\t\\t\\t}\\n\\t\\t\\t} else {\\n\\t\\t\\t\tisSystemPrefix := strings.HasPrefix(name, \\\"enable_streamz_\\\") || strings.HasPrefix(name, \\\"streamz_\\\") || strings.HasPrefix(name, \\\"enforce_kernel_\\\") || strings.HasPrefix(name, \\\"securewrapper_\\\") || strings.HasPrefix(name, \\\"glog_\\\") || strings.HasPrefix(name, \\\"log_\\\") || name == \\\"alsologtostderr\\\" || name == \\\"logtostderr\\\" || name == \\\"stderrthreshold\\\" || name == \\\"vmodule\\\" || name == \\\"log_backtrace_at\\\" || name == \\\"v\\\"\\n\\t\\t\\t\tif !hasValue && i+1 < len(os.Args) && !strings.HasPrefix(os.Args[i+1], \\\"-\\\") {\\n\\t\\t\\t\\t\tif isSystemPrefix || os.Args[i+1] == \\\"true\\\" || os.Args[i+1] == \\\"false\\\" {\\n\\t\\t\\t\\t\t\tskipNext = true\\n\\t\\t\\t\\t\t}\\n\\t\\t\\t\\t}\\n\\t\\t\\t}\\n\\t\\t}\\n\\t\\tos.Args = filteredArgs\\n\\t}\\n\\tflag.Parse()")
if "func LoadConfig() (*Config, error) {" in content:
    if "import (" in content and "\\"os\\"" not in content:
        content = content.replace("import (", "import (\\n\\t\\"os\\"")
    if "import (" in content and "\\"strings\\"" not in content:
        content = content.replace("import (", "import (\\n\\t\\"strings\\"")
    content = content.replace("func LoadConfig() (*Config, error) {", "func LoadConfig() (*Config, error) {\\n\\t{\\n\\t\\tregisteredFlags := make(map[string]bool)\\n\\t\\tflag.CommandLine.VisitAll(func(f *flag.Flag) {\\n\\t\\t\\tregisteredFlags[f.Name] = true\\n\\t\\t})\\n\\t\\tfilteredArgs := make([]string, 0, len(os.Args))\\n\\t\\tif len(os.Args) > 0 {\\n\\t\\t\\tfilteredArgs = append(filteredArgs, os.Args[0])\\n\\t\\t}\\n\\t\\tskipNext := false\\n\\t\\tfor i := 1; i < len(os.Args); i++ {\\n\\t\\t\\tif skipNext {\\n\\t\\t\\t\\tskipNext = false\\n\\t\\t\\t\\tcontinue\\n\\t\\t\\t}\\n\\t\\t\\targ := os.Args[i]\\n\\t\\t\\tif !strings.HasPrefix(arg, \\\"-\\\") {\\n\\t\\t\\t\\tfilteredArgs = append(filteredArgs, arg)\\n\\t\\t\\t\\tcontinue\\n\\t\\t\\t}\\n\\t\\t\\tvar name string\\n\\t\\t\\tvar hasValue bool\\n\\t\\t\\tif strings.Contains(arg, \\\"=\\\") {\\n\\t\\t\\t\\tparts := strings.SplitN(arg, \\\"=\\\", 2)\\n\\t\\t\\t\\tname = parts[0]\\n\\t\\t\\t\\thasValue = true\\n\\t\\t\\t} else {\\n\\t\\t\\t\\tname = arg\\n\\t\\t\\t}\\n\\t\\t\\tname = strings.TrimPrefix(name, \\\"--\\\")\\n\\t\\t\\tname = strings.TrimPrefix(name, \\\"-\\\")\\n\\t\\t\\tif registeredFlags[name] {\\n\\t\\t\\t\\tfilteredArgs = append(filteredArgs, arg)\\n\\t\\t\\t\\tvar isBool bool\\n\\t\\t\\t\\tif f := flag.CommandLine.Lookup(name); f != nil {\\n\\t\\t\\t\\t\tif bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {\\n\\t\\t\\t\\t\t\tisBool = true\\n\\t\\t\\t\\t\t}\\n\\t\\t\\t\\t}\\n\\t\\t\\t\\tif !hasValue && i+1 < len(os.Args) && !strings.HasPrefix(os.Args[i+1], \\\"-\\\") {\\n\\t\\t\\t\\t\tif !isBool || os.Args[i+1] == \\\"true\\\" || os.Args[i+1] == \\\"false\\\" {\\n\\t\\t\\t\\t\t\tfilteredArgs = append(filteredArgs, os.Args[i+1])\\n\\t\\t\\t\\t\t\tskipNext = true\\n\\t\\t\\t\\t\t}\\n\\t\\t\\t\\t}\\n\\t\\t\\t} else {\\n\\t\\t\\t\tisSystemPrefix := strings.HasPrefix(name, \\\"enable_streamz_\\\") || strings.HasPrefix(name, \\\"streamz_\\\") || strings.HasPrefix(name, \\\"enforce_kernel_\\\") || strings.HasPrefix(name, \\\"securewrapper_\\\") || strings.HasPrefix(name, \\\"glog_\\\") || strings.HasPrefix(name, \\\"log_\\\") || name == \\\"alsologtostderr\\\" || name == \\\"logtostderr\\\" || name == \\\"stderrthreshold\\\" || name == \\\"vmodule\\\" || name == \\\"log_backtrace_at\\\" || name == \\\"v\\\"\\n\\t\\t\\t\tif !hasValue && i+1 < len(os.Args) && !strings.HasPrefix(os.Args[i+1], \\\"-\\\") {\\n\\t\\t\\t\\t\tif isSystemPrefix || os.Args[i+1] == \\\"true\\\" || os.Args[i+1] == \\\"false\\\" {\\n\\t\\t\\t\\t\t\tskipNext = true\\n\\t\\t\\t\\t\t}\\n\\t\\t\\t\\t}\\n\\t\\t\\t}\\n\\t\\t}\\n\\t\\tos.Args = filteredArgs\\n\\t}\\n\\tflag.Parse()")
content = content.replace("golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/metrics/server/server", "sigs.k8s.io/controller-runtime/pkg/metrics/server")
content = content.replace("xmanager_cloud/security/keyreloader", "xmanager_cloud/internal/security/keyreloader")
if "startManager(ctx" in content: content = content.replace("\\\"sigs.k8s.io/controller-runtime/pkg/client/client\\\"", "\\\"sigs.k8s.io/controller-runtime/pkg/client/client\\\"\\n\\tmetricsserver \\\"sigs.k8s.io/controller-runtime/pkg/metrics/server\\\"")
content = content.replace("MetricsBindAddress:     \\\":8081\\\",\\n\\t\\tHealthProbeBindAddress: \\\":8080\\\",\\n\\t\\tNewCache: cache.BuilderWithOptions(cache.Options{\\n\\t\\t\\tSelectorsByObject: cache.SelectorsByObject{\\n\\t\\t\\t\\t&v1alpha2.JobSet{}: {\\n\\t\\t\\t\\t\\tLabel: labels.SelectorFromSet(labels.Set{\\n\\t\\t\\t\\t\\t\\tconstants.ManagedByLabel: constants.XmcLabelValue,\\n\\t\\t\\t\\t\\t}),\\n\\t\\t\\t\\t},\\n\\t\\t\\t\\t&v1beta1.MultiKueueCluster{}: {},\\n\\t\\t\\t\\t&v1beta1.ResourceFlavor{}:    {},\\n\\t\\t\\t},\\n\\t\\t}),", "Metrics:                metricsServerOptions,\\n\\t\\tHealthProbeBindAddress: \\\":8080\\\",\\n\\t\\tCache: cache.Options{\\n\\t\\t\\tByObject: map[runtimeclient.Object]cache.ByObject{\\n\\t\\t\\t\\t&v1alpha2.JobSet{}: {\\n\\t\\t\\t\\t\\tLabel: labels.SelectorFromSet(labels.Set{\\n\\t\\t\\t\\t\\t\\tconstants.ManagedByLabel: constants.XmcLabelValue,\\n\\t\\t\\t\\t\\t}),\\n\\t\\t\\t\\t},\\n\\t\\t\\t\\t&v1beta1.MultiKueueCluster{}: {},\\n\\t\\t\\t\\t&v1beta1.ResourceFlavor{}:    {},\\n\\t\\t\\t},\\n\\t\\t},")
content = content.replace("mgr, err := ctrl.NewManager(k8sConfig, ctrl.Options{\\n\\t\\tScheme:                 scheme,", "metricsServerOptions := metricsserver.Options{\\n\\t\\tBindAddress: \\\":8081\\\",\\n\\t}\\n\\tmgr, err := ctrl.NewManager(k8sConfig, ctrl.Options{\\n\\t\\tScheme:                 scheme,")
content = content.replace("xmanager_cloud/experiment_state_server/proto/api_go_grpc_gateway_proto", "xmanager_cloud/experiment_state_server/proto/api_go_proto")
content = content.replace("xmanager_cloud/health_service/proto/api_go_grpc_gateway_proto", "xmanager_cloud/health_service/proto/api_go_proto")
content = content.replace("xmanager_cloud/health_service/proto/api_go_grpc_proto", "xmanager_cloud/health_service/proto/api_go_proto")
content = content.replace("xmanager_cloud/experiment_state_server/server/server", "xmanager_cloud/cmd/experiment_state_server/server")
content = content.replace("go:embed xmanager_cloud/experiment_state_server/proto/ess_messages.pb", "go:embed ess_messages.pb")
content = content.replace("go:embed xmanager_cloud/experiment_state_server/static/*", "go:embed static/*")
content = content.replace("xmanager_cloud/experiment_state_server/static", "static")
content = content.replace("fs.Sub(embedFS, \\"xmanager_cloud/experiment_state_server/static\\")", "fs.Sub(embedFS, \\"static\\")")
content = content.replace("go:embed xmanager_cloud/xid_service/proto/xid_messages.pb", "go:embed xid_messages.pb")
content = content.replace("func (r *Registry) Load(req *pluginpb.CodeGeneratorRequest) error {", "func (r *Registry) LoadGrpcAPIServiceFromYAML(yamlFile string) error { return fmt.Errorf(\\\"YAML configuration is not supported\\\") }\\nfunc (r *Registry) LoadOpenAPIDefinitionFromYAML(yamlFile string) error { return fmt.Errorf(\\\"YAML configuration is not supported\\\") }\\nfunc (r *Registry) Load(req *pluginpb.CodeGeneratorRequest) error {")
if "FileDescriptorSet" in content: content = content.replace("emptypb \\\"google.golang.org/protobuf/types/known/emptypb\\\"", "emptypb \\\"google.golang.org/protobuf/types/known/emptypb\\\"\\n\\tdescpb \\\"google.golang.org/protobuf/types/descriptorpb\\\"")
content = content.replace("xmanager_cloud/storage/spanner/migrations/commands/commands", "xmanager_cloud/internal/storage/spanner/migrations/commands")
content = content.replace("xmanager_cloud/storage/spanner/migrations/driver/spannerdriver", "xmanager_cloud/internal/storage/spanner/migrations/driver")
content = content.replace("xmanager_cloud/storage/storage", "xmanager_cloud/internal/storage")
content = content.replace("xmanager_cloud/storage/data/data", "xmanager_cloud/internal/storage/data")
content = content.replace("c.Watch(&source.Kind{Type: &v1beta1.ClusterQueue{}}, &handler.EnqueueRequestForObject{}, predicate.ResourceVersionChangedPredicate{})", "c.Watch(source.Kind[client.Object](mgr.GetCache(), &v1beta1.ClusterQueue{}, &handler.EnqueueRequestForObject{}, predicate.ResourceVersionChangedPredicate{}))")
content = content.replace("c.Watch(&source.Kind{Type: &v1beta1.Workload{}}, &handler.EnqueueRequestForObject{}, workloadPredicate(ctx))", "c.Watch(source.Kind[client.Object](mgr.GetCache(), &v1beta1.Workload{}, &handler.EnqueueRequestForObject{}, workloadPredicate(ctx)))")
content = content.replace("c.Watch(&source.Kind{Type: &v1alpha2.JobSet{}}, &handler.EnqueueRequestForObject{}, jobSetPredicate(ctx))", "c.Watch(source.Kind[client.Object](mgr.GetCache(), &v1alpha2.JobSet{}, &handler.EnqueueRequestForObject{}, jobSetPredicate(ctx)))")
content = content.replace("func (c *clientWithError) Get(ctx context.Context, key client.ObjectKey, obj client.Object) error {", "func (c *clientWithError) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {")
content = content.replace("return c.Client.Get(ctx, key, obj)", "return c.Client.Get(ctx, key, obj, opts...)")
# deduplicate trailing name/name pattern ONLY for k8s.io and sigs.k8s.io packages
content = re.sub(r"((?:k8s\\.io|sigs\\.k8s\\.io)/[^\\x22\\s]+)/([^/\\x22\\s]+)/\\2(?=[/\\x22\\s])", r"\\1/\\2", content)
open("$@", "w").write(content)
'
""",
        )
        compat_srcs.append(compat_src)
    return compat_srcs

def compat_go_library(name, srcs, **kwargs):
    go_library(
        name = name,
        srcs = _compat_gen_srcs(name, srcs),
        **kwargs
    )

def compat_go_test(name, srcs = [], **kwargs):
    go_test(
        name = name,
        srcs = _compat_gen_srcs(name, srcs),
        **kwargs
    )
