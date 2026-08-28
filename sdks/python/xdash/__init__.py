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
"""Public API for xdash: XMC Python SDK & CLI for Metrics Dashboards."""

from xdash.builders import (
    ChartBuilder,
    DashboardBuilder,
    bar_plot,
    chart_id_from_name,
    scatter_plot,
    summary_kpi_plot,
    validate_chart_id,
)
from xdash.client import (
    DashboardClient,
    XDashConnectionError,
    XDashError,
    XDashRPCError,
)
from xdash.presets import (
    VCC_BASELINE_XID,
    VCC_COMPARISON_RUNS,
    VCC_TUNED_V1_XID,
    VCC_TUNED_V2_XID,
    create_standard_experiment_chart,
    create_vcc_comparison_charts,
    create_vcc_comparison_dashboard,
)

__all__ = [
    "DashboardBuilder",
    "ChartBuilder",
    "summary_kpi_plot",
    "scatter_plot",
    "bar_plot",
    "chart_id_from_name",
    "validate_chart_id",
    "DashboardClient",
    "XDashError",
    "XDashConnectionError",
    "XDashRPCError",
    "create_vcc_comparison_dashboard",
    "create_vcc_comparison_charts",
    "create_standard_experiment_chart",
    "VCC_BASELINE_XID",
    "VCC_TUNED_V1_XID",
    "VCC_TUNED_V2_XID",
    "VCC_COMPARISON_RUNS",
]
