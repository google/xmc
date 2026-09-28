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

def _repo_import_impl(_ctx):
    _custom_grpc_gateway(name = "com_github_grpc_ecosystem_grpc_gateway_v2_custom")

repo_import = module_extension(
    implementation = _repo_import_impl,
)
