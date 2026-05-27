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

workspace(name = "xmanager_cloud")

load("@bazel_tools//tools/build_defs/repo:http.bzl", "http_archive")

# rules_go release v0.46.0
http_archive(
    name = "io_bazel_rules_go",
    sha256 = "6b65cb0917c5ef653c5007408460f4de2f9cc49cfaac1190b6dae621e29f8905",
    urls = [
        "https://mirror.bazel.build/github.com/bazelbuild/rules_go/releases/download/v0.46.0/rules_go-v0.46.0.tar.gz",
        "https://github.com/bazelbuild/rules_go/releases/download/v0.46.0/rules_go-v0.46.0.tar.gz",
    ],
)

# Gazelle v0.35.0
http_archive(
    name = "bazel_gazelle",
    sha256 = "3212ae2a390db2deb164a5ec4464baeeebdf5de5dd3e88fa12c3cbcc2fcaf7db",
    urls = [
        "https://mirror.bazel.build/github.com/bazelbuild/bazel-gazelle/releases/download/v0.35.0/bazel-gazelle-v0.35.0.tar.gz",
        "https://github.com/bazelbuild/bazel-gazelle/releases/download/v0.35.0/bazel-gazelle-v0.35.0.tar.gz",
    ],
)

# Rules Proto 6.0.0-rc3
http_archive(
    name = "rules_proto",
    sha256 = "dc3fb2063f240baf76f4ef6ec9d5840b3e55df8cfc3baf6ab29b5cfa4ca8b9fb",
    strip_prefix = "rules_proto-6.0.0-rc3",
    urls = [
        "https://github.com/bazelbuild/rules_proto/archive/refs/tags/6.0.0-rc3.tar.gz",
    ],
)

load("@io_bazel_rules_go//go:deps.bzl", "go_register_toolchains", "go_rules_dependencies")
load("@bazel_gazelle//:deps.bzl", "gazelle_dependencies")
load("@rules_proto//proto:repositories.bzl", "rules_proto_dependencies")

go_rules_dependencies()
go_register_toolchains(version = "1.22.3")
gazelle_dependencies()
rules_proto_dependencies()

# Protobuf (com_google_protobuf) v21.7
http_archive(
    name = "com_google_protobuf",
    sha256 = "75be37804707201fd9b7ec95d1cc32b6045d65451998bd50989f643e2632b72f",
    strip_prefix = "protobuf-21.7",
    urls = [
        "https://github.com/protocolbuffers/protobuf/archive/v21.7.tar.gz",
    ],
)
load("@com_google_protobuf//:protobuf_deps.bzl", "protobuf_deps")
protobuf_deps()

# Google APIs (com_google_googleapis)
http_archive(
    name = "com_google_googleapis",
    sha256 = "72dc03a60a7f5df555f269a910793664d4b19c287ec2d79a29e46a7be7c77f0a",
    strip_prefix = "googleapis-2487e45214041d8e1291880455d376cd5c81deef",
    urls = [
        "https://github.com/googleapis/googleapis/archive/2487e45214041d8e1291880455d376cd5c81deef.tar.gz",
    ],
)
load("@com_google_googleapis//:repository_rules.bzl", "switched_rules_by_language")
switched_rules_by_language(name = "com_google_googleapis_imports", go = True)
