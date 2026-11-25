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

variable "REGISTRY" {
  default = "ghcr.io/google/xmc"
}

variable "TAGS" {
  default = "local"
}

variable "GIT_SHA" {
  default = "dev"
}

variable "CACHE_MODE" {
  default = ""
}

# Populated from deploy/ci/images/versions.json via render_bake_vars.py.
variable "BASE_IMAGE" { default = "" }
variable "PYTHON_VERSION" { default = "" }
variable "PYTHON_RELEASE_TAG" { default = "" }
variable "PYTHON_SHA256" { default = "" }
variable "NODE_VERSION" { default = "" }
variable "NODE_SHA256" { default = "" }
variable "GO_VERSION" { default = "" }
variable "GO_SHA256" { default = "" }
variable "RUFF_VERSION" { default = "" }
variable "MYPY_VERSION" { default = "" }
variable "GOLANGCI_LINT_VERSION" { default = "" }
variable "ACTIONLINT_VERSION" { default = "" }
variable "ACTIONLINT_SHA256" { default = "" }
variable "BUILDIFIER_VERSION" { default = "" }
variable "BUILDIFIER_SHA256" { default = "" }
variable "ADDLICENSE_VERSION" { default = "" }
variable "ADDLICENSE_SHA256" { default = "" }
variable "SHELLCHECK_VERSION" { default = "" }
variable "SHELLCHECK_SHA256" { default = "" }
variable "TERRAFORM_VERSION" { default = "" }
variable "TERRAFORM_SHA256" { default = "" }
variable "TFLINT_VERSION" { default = "" }
variable "TFLINT_SHA256" { default = "" }
variable "HELM_VERSION" { default = "" }
variable "HELM_SHA256" { default = "" }
variable "TRIVY_VERSION" { default = "" }
variable "TRIVY_SHA256" { default = "" }

function "image_tags" {
  params = [name]
  result = [for t in compact(split(",", TAGS)) : "${REGISTRY}/${name}:${trimspace(t)}"]
}

function "gha_cache_from" {
  params = [name]
  result = CACHE_MODE == "gha" ? ["type=gha,scope=${name}"] : []
}

function "gha_cache_to" {
  params = [name]
  result = CACHE_MODE == "gha" ? ["type=gha,mode=max,scope=${name}"] : []
}

group "default" {
  targets = ["ci-base", "ci-lint"]
}

target "_common" {
  context   = "."
  platforms = ["linux/amd64"]
  labels = {
    "org.opencontainers.image.source"   = "https://github.com/google/xmc"
    "org.opencontainers.image.revision" = GIT_SHA
    "org.opencontainers.image.licenses" = "Apache-2.0"
  }
}

target "ci-base" {
  inherits   = ["_common"]
  dockerfile = "deploy/ci/images/Dockerfile.base"
  tags       = image_tags("ci-base")
  cache-from = gha_cache_from("ci-base")
  cache-to   = gha_cache_to("ci-base")
  args = {
    BASE_IMAGE         = BASE_IMAGE
    PYTHON_VERSION     = PYTHON_VERSION
    PYTHON_RELEASE_TAG = PYTHON_RELEASE_TAG
    PYTHON_SHA256      = PYTHON_SHA256
    NODE_VERSION       = NODE_VERSION
    NODE_SHA256        = NODE_SHA256
  }
}

target "ci-lint" {
  inherits   = ["_common"]
  dockerfile = "deploy/ci/images/Dockerfile.lint"
  tags       = image_tags("ci-lint")
  cache-from = gha_cache_from("ci-lint")
  cache-to   = gha_cache_to("ci-lint")
  contexts = {
    "ci-base" = "target:ci-base"
  }
  args = {
    GO_VERSION            = GO_VERSION
    GO_SHA256             = GO_SHA256
    RUFF_VERSION          = RUFF_VERSION
    MYPY_VERSION          = MYPY_VERSION
    GOLANGCI_LINT_VERSION = GOLANGCI_LINT_VERSION
    ACTIONLINT_VERSION    = ACTIONLINT_VERSION
    ACTIONLINT_SHA256     = ACTIONLINT_SHA256
    BUILDIFIER_VERSION    = BUILDIFIER_VERSION
    BUILDIFIER_SHA256     = BUILDIFIER_SHA256
    ADDLICENSE_VERSION    = ADDLICENSE_VERSION
    ADDLICENSE_SHA256     = ADDLICENSE_SHA256
    SHELLCHECK_VERSION    = SHELLCHECK_VERSION
    SHELLCHECK_SHA256     = SHELLCHECK_SHA256
    TERRAFORM_VERSION     = TERRAFORM_VERSION
    TERRAFORM_SHA256      = TERRAFORM_SHA256
    TFLINT_VERSION        = TFLINT_VERSION
    TFLINT_SHA256         = TFLINT_SHA256
    HELM_VERSION          = HELM_VERSION
    HELM_SHA256           = HELM_SHA256
    TRIVY_VERSION         = TRIVY_VERSION
    TRIVY_SHA256          = TRIVY_SHA256
  }
}
