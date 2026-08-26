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
"""Internal helper for resolving generated Dashboard Service protobuf modules."""

try:
    from xmanager_cloud.dashboard_service.proto import (  # type: ignore[attr-defined, no-redef]
        dashboard_service_pb2,
        messages_pb2,
    )
except ImportError:
    from third_party.xmanager_cloud.dashboard_service.proto import (  # type: ignore[attr-defined, no-redef]
        dashboard_service_pb2,
        messages_pb2,
    )

__all__ = ["messages_pb2", "dashboard_service_pb2"]
