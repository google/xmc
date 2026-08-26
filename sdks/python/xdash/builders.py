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
"""Fluent builders and helpers for constructing Dashboard, Chart, and Plot protobuf messages."""

from __future__ import annotations

import re
from collections.abc import Sequence

from xdash._protos import messages_pb2


def _slugify(text: str) -> str:
    """Converts a title or name into a safe resource ID slug."""
    slug = re.sub(r"[^a-zA-Z0-9_-]+", "_", text.strip()).lower()
    return slug.strip("_") or "default"


def validate_chart_id(chart_id: str) -> str:
    """Validates a raw chart ID and returns it stripped of surrounding whitespace.

    A dashboard references its charts by raw ID, never by resource name, so a value
    containing a slash is rejected rather than silently stored as an unresolvable
    reference. This mirrors ValidateChartID in the Go validator package.

    Args:
        chart_id: Raw chart identifier, normally the UUID assigned by CreateChart.

    Returns:
        The validated chart ID.

    Raises:
        ValueError: If chart_id is blank or contains a slash.
    """
    if not chart_id or not chart_id.strip():
        raise ValueError("chart_id is required and cannot be blank")
    if "/" in chart_id:
        raise ValueError(
            f"chart_id {chart_id!r} cannot contain slashes. Dashboards reference charts by "
            "raw ID, not by resource name — use chart_id_from_name() to extract it."
        )
    return chart_id.strip()


def chart_id_from_name(name: str) -> str:
    """Extracts the raw chart ID from a full chart resource name.

    CreateChart returns a chart whose name is
    'experiments/{xid}/workUnits/{wid}/charts/{chart_id}'; a dashboard needs only the
    trailing '{chart_id}'. The other two Chart patterns, 'charts/{chart_id}' and
    'dashboards/{dashboard}/charts/{chart_id}', are accepted too.

    The name must actually identify a chart. Taking the trailing segment on its own
    would turn a work unit name into a plausible-looking ID — 'experiments/1001/
    workUnits/1' would yield '1' — that the server then resolves to nothing, or to the
    wrong chart. Requiring the 'charts' collection segment rejects that at the call
    site instead.

    Args:
        name: Full chart resource name.

    Returns:
        The trailing chart ID segment.

    Raises:
        ValueError: If name is blank or does not name a chart.
    """
    if not name or not name.strip():
        raise ValueError("chart resource name is required and cannot be blank")
    # rstrip first: a trailing slash would otherwise yield an empty ID that only fails
    # later, at the server, with a message pointing nowhere near the real cause.
    segments = name.strip().rstrip("/").split("/")
    # Match on the collection segment rather than a leading '/charts/', so the
    # top-level 'charts/{chart_id}' pattern is not rejected for lacking a parent.
    if len(segments) < 2 or segments[-2] != "charts" or not segments[-1]:
        raise ValueError(
            f"{name!r} is not a chart resource name: expected a trailing 'charts/<chart_id>'"
        )
    return segments[-1]


def _validate_plot_args(plot_id: str, metrics: Sequence[str]) -> None:
    """Validates the identifier and metric list shared by every plot helper.

    Args:
        plot_id: Unique string identifier for the plot within a chart.
        metrics: List of metric names the plot renders.

    Raises:
        ValueError: If plot_id is blank, or metrics is empty or holds a blank name.
    """
    if not plot_id or not plot_id.strip():
        raise ValueError("plot_id is required and cannot be blank")
    if not metrics:
        raise ValueError("At least one metric must be provided for a plot")
    if any(not metric or not metric.strip() for metric in metrics):
        raise ValueError("Metric names cannot be blank")


def summary_kpi_plot(
    plot_id: str,
    title: str,
    metrics: Sequence[str],
) -> messages_pb2.PlotSpec:
    """Constructs a Summary Box KPI card PlotSpec for the given metrics.

    Args:
        plot_id: Unique string identifier for the plot within a chart.
        title: Human-readable title displayed above the KPI box.
        metrics: List of metric names (e.g. ['val/loss']).

    Returns:
        A validated PlotSpec protobuf message.

    Raises:
        ValueError: If plot_id is blank, or metrics is empty or holds a blank name.
    """
    _validate_plot_args(plot_id, metrics)
    return messages_pb2.PlotSpec(
        plot_id=plot_id,
        title=title,
        type=messages_pb2.SUMMARY_BOX,
        data=messages_pb2.DataSpec(metrics=list(metrics)),
    )


def scatter_plot(
    plot_id: str,
    title: str,
    metrics: Sequence[str],
    x_metric: str = "step",
    y_metric: str = "",
) -> messages_pb2.PlotSpec:
    """Constructs a Scatter/Line chart PlotSpec for time series metrics.

    Args:
        plot_id: Unique string identifier for the plot within a chart.
        title: Human-readable title for the chart plot.
        metrics: List of metric names to plot on the Y axis.
        x_metric: Mapping dimension for X axis (defaults to 'step').
        y_metric: Explicit mapping dimension for Y axis (optional).

    Returns:
        A validated PlotSpec protobuf message.

    Raises:
        ValueError: If plot_id is blank, or metrics is empty or holds a blank name.
    """
    _validate_plot_args(plot_id, metrics)
    mapping = messages_pb2.MappingSpec(x=x_metric)
    if y_metric:
        mapping.y = y_metric
    return messages_pb2.PlotSpec(
        plot_id=plot_id,
        title=title,
        type=messages_pb2.SCATTER,
        data=messages_pb2.DataSpec(metrics=list(metrics)),
        mapping=mapping,
    )


def bar_plot(
    plot_id: str,
    title: str,
    metrics: Sequence[str],
) -> messages_pb2.PlotSpec:
    """Constructs a Bar chart PlotSpec for categorical/bucketed metric comparisons.

    Args:
        plot_id: Unique string identifier for the plot within a chart.
        title: Human-readable title for the bar plot.
        metrics: List of metric names to display.

    Returns:
        A validated PlotSpec protobuf message.

    Raises:
        ValueError: If plot_id is blank, or metrics is empty or holds a blank name.
    """
    _validate_plot_args(plot_id, metrics)
    return messages_pb2.PlotSpec(
        plot_id=plot_id,
        title=title,
        type=messages_pb2.BAR,
        data=messages_pb2.DataSpec(metrics=list(metrics)),
    )


class ChartBuilder:
    """Fluent builder for constructing validated Chart protobuf messages."""

    def __init__(
        self,
        name: str | None = None,
        title: str = "",
        description: str = "",
    ) -> None:
        """Initializes a new ChartBuilder instance.

        Args:
            name: Optional full resource name for the chart.
            title: Human-readable chart title.
            description: Optional detailed description.
        """
        self._name: str | None = name
        self._title: str = title
        self._description: str = description
        self._xid: int = 0
        self._wid: int = 0
        self._plots: list[messages_pb2.PlotSpec] = []
        self._columns: int = 2

    def for_experiment(self, xid: int, wid: int = 1) -> ChartBuilder:
        """Associates the chart with a specific experiment ID and work unit ID."""
        if xid <= 0:
            raise ValueError("Experiment ID (xid) must be a positive integer.")
        # Positive, not merely non-negative: CreateChart validates its parent with
        # ValidateWorkUnitName, which rejects experiments/{xid}/workUnits/0.
        if wid <= 0:
            raise ValueError("Work unit ID (wid) must be a positive integer.")
        self._xid = xid
        self._wid = wid
        return self

    def set_name(self, name: str) -> ChartBuilder:
        """Sets the explicit resource name for the chart."""
        self._name = name
        return self

    def set_title(self, title: str) -> ChartBuilder:
        """Sets the chart title."""
        self._title = title
        return self

    def set_description(self, description: str) -> ChartBuilder:
        """Sets the chart description."""
        self._description = description
        return self

    def set_grid_layout(self, columns: int = 2) -> ChartBuilder:
        """Configures a grid layout with the specified column count."""
        if columns <= 0:
            raise ValueError("Grid columns must be >= 1.")
        self._columns = columns
        return self

    def add_plot(self, plot: messages_pb2.PlotSpec) -> ChartBuilder:
        """Appends a PlotSpec to the chart.

        Raises:
            ValueError: If plot is None — a helper that returned nothing would
                otherwise surface as an opaque failure at build() or at the server.
        """
        if plot is None:
            raise ValueError("plot is required and cannot be None")
        self._plots.append(plot)
        return self

    def _resolve_resource_name(self) -> str:
        """Determines the default resource name if not explicitly set."""
        if self._name:
            return self._name
        slug = _slugify(self._title or "chart")
        return f"experiments/{self._xid}/workUnits/{self._wid}/charts/{slug}"

    def build(self) -> messages_pb2.Chart:
        """Validates configuration and builds the Chart protobuf message.

        Returns:
            A populated messages_pb2.Chart instance.

        Raises:
            ValueError: If required fields (xid, wid, title, plots) are missing.
        """
        if self._xid <= 0:
            raise ValueError("Experiment ID (xid) must be set via for_experiment().")
        if self._wid <= 0:
            raise ValueError("Work unit ID (wid) must be set via for_experiment().")
        if not self._title or not self._title.strip():
            raise ValueError("Chart title is required and cannot be blank.")
        if not self._plots:
            raise ValueError("At least one PlotSpec must be added to the chart.")

        layout = messages_pb2.LayoutSpec(
            type=messages_pb2.LayoutSpec.GRID,
            columns=self._columns,
        )
        return messages_pb2.Chart(
            name=self._resolve_resource_name(),
            title=self._title,
            description=self._description,
            xid=self._xid,
            wid=self._wid,
            plots=self._plots,
            plot_layout=layout,
        )


class DashboardBuilder:
    """Fluent builder for constructing validated Dashboard protobuf messages."""

    def __init__(
        self,
        name: str | None = None,
        title: str = "",
        description: str = "",
    ) -> None:
        """Initializes a new DashboardBuilder instance.

        Args:
            name: Optional resource name (e.g. 'dashboards/my_dash').
            title: Human-readable dashboard title.
            description: Optional dashboard description.
        """
        self._name: str | None = name
        self._title: str = title
        self._description: str = description
        self._chart_ids: list[str] = []
        self._columns: int = 2

    def set_name(self, name: str) -> DashboardBuilder:
        """Sets the dashboard resource name."""
        self._name = name
        return self

    def set_title(self, title: str) -> DashboardBuilder:
        """Sets the dashboard title."""
        self._title = title
        return self

    def set_description(self, description: str) -> DashboardBuilder:
        """Sets the dashboard description."""
        self._description = description
        return self

    def set_grid_layout(self, columns: int = 2) -> DashboardBuilder:
        """Configures the dashboard chart grid layout."""
        if columns <= 0:
            raise ValueError("Grid columns must be >= 1.")
        self._columns = columns
        return self

    def add_chart_id(self, chart_id: str) -> DashboardBuilder:
        """References an existing chart by its raw ID.

        The chart must already exist: CreateDashboard records the reference but never
        creates the chart itself, so pass the ID of a chart returned by
        DashboardClient.create_chart().

        Args:
            chart_id: Raw chart identifier, normally a server-assigned UUID.

        Raises:
            ValueError: If chart_id is blank, contains a slash, or is already referenced.
                Duplicates are rejected here because the server rejects them too, and a
                clear client-side error beats an opaque InvalidArgument.
        """
        validated = validate_chart_id(chart_id)
        if validated in self._chart_ids:
            raise ValueError(f"chart_id {validated!r} is already referenced by this dashboard")
        self._chart_ids.append(validated)
        return self

    def add_chart(self, chart: messages_pb2.Chart) -> DashboardBuilder:
        """References a chart by deriving its raw ID from the Chart message's name.

        Convenience wrapper over add_chart_id() for the common case of holding the
        Chart returned by create_chart(). Only the ID is stored — a Dashboard no longer
        carries embedded Chart messages.

        Raises:
            ValueError: If chart is None, its name is unset, or the derived ID is
                already referenced. See ChartBuilder.add_plot.
        """
        if chart is None:
            raise ValueError("chart is required and cannot be None")
        if not chart.name:
            raise ValueError(
                "chart.name must be set to reference it from a dashboard. Build the chart "
                "with ChartBuilder(name=...) or use the Chart returned by create_chart()."
            )
        return self.add_chart_id(chart_id_from_name(chart.name))

    def _resolve_resource_name(self) -> str:
        """Determines the default dashboard resource name if not explicitly set."""
        if self._name:
            return self._name
        slug = _slugify(self._title or "dashboard")
        return f"dashboards/{slug}"

    def build(self) -> messages_pb2.Dashboard:
        """Validates configuration and builds the Dashboard protobuf message.

        Note that GetDashboard returns chart_ids sorted and de-duplicated, so the order
        charts were added here is not preserved on read. Use chart_layout positions,
        keyed by raw chart ID, when placement matters.

        Returns:
            A populated messages_pb2.Dashboard instance.

        Raises:
            ValueError: If the dashboard title is missing.
        """
        if not self._title or not self._title.strip():
            raise ValueError("Dashboard title is required and cannot be blank.")

        layout = messages_pb2.LayoutSpec(
            type=messages_pb2.LayoutSpec.GRID,
            columns=self._columns,
        )
        return messages_pb2.Dashboard(
            name=self._resolve_resource_name(),
            title=self._title,
            description=self._description,
            chart_ids=self._chart_ids,
            chart_layout=layout,
        )
