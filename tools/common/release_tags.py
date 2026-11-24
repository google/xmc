# Copyright 2026 Google LLC
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

"""Shared git release tag glob patterns for release tooling and build stamping."""

RELEASE_TAG_GLOBS: tuple[str, ...] = ("[0-9]*.[0-9]*.[0-9]*", "v[0-9]*.[0-9]*.[0-9]*")

# Pre-release tags such as 1.2.3-rc1 contain a hyphen; bare release tags never do.
PRE_RELEASE_TAG_GLOB = "*-*"
