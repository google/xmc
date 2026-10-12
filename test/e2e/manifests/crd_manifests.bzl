# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Repository rule exposing upstream JobSet and Kueue CRD manifests from Go module dependencies."""

_CRD_SOURCES = {
    "jobset.x-k8s.io_jobsets.yaml": (
        Label("@io_k8s_sigs_jobset//:BUILD.bazel"),
        "config/components/crd/bases/jobset.x-k8s.io_jobsets.yaml",
    ),
    "kueue.x-k8s.io_clusterqueues.yaml": (
        Label("@io_k8s_sigs_kueue//:BUILD.bazel"),
        "config/components/crd/bases/kueue.x-k8s.io_clusterqueues.yaml",
    ),
    "kueue.x-k8s.io_localqueues.yaml": (
        Label("@io_k8s_sigs_kueue//:BUILD.bazel"),
        "config/components/crd/bases/kueue.x-k8s.io_localqueues.yaml",
    ),
    "kueue.x-k8s.io_resourceflavors.yaml": (
        Label("@io_k8s_sigs_kueue//:BUILD.bazel"),
        "config/components/crd/bases/kueue.x-k8s.io_resourceflavors.yaml",
    ),
    "kueue.x-k8s.io_workloadpriorityclasses.yaml": (
        Label("@io_k8s_sigs_kueue//:BUILD.bazel"),
        "config/components/crd/bases/kueue.x-k8s.io_workloadpriorityclasses.yaml",
    ),
    "kueue.x-k8s.io_workloads.yaml": (
        Label("@io_k8s_sigs_kueue//:BUILD.bazel"),
        "config/components/crd/bases/kueue.x-k8s.io_workloads.yaml",
    ),
}

def _k8s_crd_manifests_impl(repository_ctx):
    for filename, (anchor_label, rel_path) in _CRD_SOURCES.items():
        repo_root = repository_ctx.path(anchor_label).dirname
        repository_ctx.symlink(repo_root.get_child(rel_path), filename)

    repository_ctx.file(
        "BUILD.bazel",
        'exports_files(glob(["*.yaml"]), visibility = ["//visibility:public"])\n',
    )

k8s_crd_manifests = repository_rule(
    implementation = _k8s_crd_manifests_impl,
    doc = "Symlinks upstream JobSet and Kueue CRD YAML files from @io_k8s_sigs_jobset and @io_k8s_sigs_kueue.",
)
