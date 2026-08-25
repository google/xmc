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

def _custom_grpc_gateway_impl(ctx):
    ctx.download_and_extract(
        url = "https://github.com/grpc-ecosystem/grpc-gateway/archive/refs/tags/v2.27.7.tar.gz",
        sha256 = "1d4f949bf463e324632653082012dba2d4b25e58e70584fedbdad7a626e2d445",
        stripPrefix = "grpc-gateway-2.27.7",
    )
    ctx.execute(["find", ".", "-name", "BUILD", "-o", "-name", "BUILD.bazel", "-delete"])
    ctx.symlink(ctx.path(Label("@@//third_party:grpc_gateway.BUILD")), "BUILD.bazel")

_custom_grpc_gateway = repository_rule(
    implementation = _custom_grpc_gateway_impl,
)

def _envtest_binaries_impl(ctx):
    os_name = ctx.os.name.lower()
    arch = ctx.os.arch.lower()

    if "linux" in os_name:
        if arch in ["amd64", "x86_64"]:
            url = "https://github.com/kubernetes-sigs/controller-tools/releases/download/envtest-v1.30.0/envtest-v1.30.0-linux-amd64.tar.gz"
            sha256 = "2a9792cb5f1403f524543ce94c3115e3c4a4229f0e86af55fd26c078da448164"
        elif arch in ["arm64", "aarch64"]:
            url = "https://github.com/kubernetes-sigs/controller-tools/releases/download/envtest-v1.30.0/envtest-v1.30.0-linux-arm64.tar.gz"
            sha256 = "39cc7274a3075a650a20fcd24b9e2067375732bebaf5356088a8efb35155f068"
        else:
            fail("Unsupported linux arch for envtest: " + arch)
    elif "mac" in os_name or "darwin" in os_name:
        if arch in ["amd64", "x86_64"]:
            url = "https://github.com/kubernetes-sigs/controller-tools/releases/download/envtest-v1.30.0/envtest-v1.30.0-darwin-amd64.tar.gz"
            sha256 = "85890b864330baec88f53aabfc1d5d94a8ca8c17483f34f4823dec0fae7c6e3a"
        elif arch in ["arm64", "aarch64"]:
            url = "https://github.com/kubernetes-sigs/controller-tools/releases/download/envtest-v1.30.0/envtest-v1.30.0-darwin-arm64.tar.gz"
            sha256 = "849362d26105b64193b4142982c710306d90248272731a81fb83efac27c5a750"
        else:
            fail("Unsupported darwin arch for envtest: " + arch)
    else:
        fail("Unsupported os for envtest: " + os_name)

    ctx.download_and_extract(
        url = url,
        sha256 = sha256,
        stripPrefix = "controller-tools/envtest",
    )
    ctx.file("BUILD.bazel", """
filegroup(
    name = "binaries",
    srcs = glob(["**/*"]),
    visibility = ["//visibility:public"],
)
exports_files(glob(["**/*"]))
""")

envtest_binaries = repository_rule(
    implementation = _envtest_binaries_impl,
)

def _jobset_manifests_impl(ctx):
    ctx.download(
        url = "https://github.com/kubernetes-sigs/jobset/releases/download/v0.12.0/manifests.yaml",
        sha256 = "a41aaf12dd0b7b0a3d626b8d6107f32c3dc889d7e7830adc46b8cdb77a8963bb",
        output = "manifests.yaml",
    )
    ctx.file("BUILD.bazel", """
filegroup(
    name = "manifests",
    srcs = ["manifests.yaml"],
    visibility = ["//visibility:public"],
)
exports_files(["manifests.yaml"])
""")

jobset_manifests = repository_rule(
    implementation = _jobset_manifests_impl,
)

def _kueue_manifests_impl(ctx):
    ctx.download(
        url = "https://github.com/kubernetes-sigs/kueue/releases/download/v0.18.2/manifests.yaml",
        sha256 = "087c660cb2ba78947c9041f80d23c85e091e939c95377f47fb7a012cdca5f85f",
        output = "manifests.yaml",
    )
    ctx.file("BUILD.bazel", """
filegroup(
    name = "manifests",
    srcs = ["manifests.yaml"],
    visibility = ["//visibility:public"],
)
exports_files(["manifests.yaml"])
""")

kueue_manifests = repository_rule(
    implementation = _kueue_manifests_impl,
)

def _repo_import_impl(ctx):
    _custom_grpc_gateway(name = "com_github_grpc_ecosystem_grpc_gateway_v2_custom")
    envtest_binaries(name = "envtest_binaries")
    jobset_manifests(name = "jobset_manifests")
    kueue_manifests(name = "kueue_manifests")

repo_import = module_extension(
    implementation = _repo_import_impl,
)
