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
  targets = ["ci-base"]
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
