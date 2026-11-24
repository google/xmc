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

"""XManager on Cloud (XMC) gRPC client protos.

Generated modules live under `xmanager_cloud.experiment_state_server.proto`,
`xmanager_cloud.xid_service.proto`, and `xmanager_cloud.dashboard_service.proto`.
Importing any of them runs this module first, so the legacy `xmanager` check below
precedes every `cloud.xmanager.v1` descriptor registration.
"""

from xmanager_cloud import _version_guard

_version_guard.check_xmanager_compatibility()
