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

"""Service aliases, naming normalization, and Kubernetes deployment mapping."""

SERVICE_ALIASES: dict[str, str] = {
    "ess": "experiment_state_server",
    "xid": "xid_service",
    "rod": "rod_data_provider",
    "gw": "ingestion_gateway",
    "gateway": "ingestion_gateway",
    "ingestion": "ingestion_gateway",
    "frontend": "ui",
    "web": "ui",
    "dash": "dashboard_service",
    "dashboard": "dashboard_service",
    "health": "health_service",
    "comp": "components",
    "bridge": "components",
    "xmc_bridge": "components",
    "monitor": "log_monitor",
}

RESERVED_SERVICE_KEYWORDS: frozenset[str] = frozenset({"auto", "all"})


def resolve_service_name(alias_or_name: str) -> str:
    """Resolves service aliases to canonical snake_case service names.

    Args:
        alias_or_name: Service alias or name (e.g. 'ess', 'xid-service', 'ui').

    Returns:
        Canonical snake_case microservice name (e.g. 'experiment_state_server').
    """
    normalized = alias_or_name.strip().lower().replace("-", "_")
    return SERVICE_ALIASES.get(normalized, normalized)


def resolve_service_names(names: list[str] | None = None) -> list[str]:
    """Resolves a list of service names or aliases to canonical service names.

    Args:
        names: List of service names or aliases.

    Returns:
        Deduplicated list of canonical snake_case service names preserving order,
        excluding reserved keywords ('auto', 'all').
    """
    if not names:
        return []
    return list(
        dict.fromkeys(
            resolve_service_name(n)
            for n in names
            if n and n.strip() and n.strip().lower() not in RESERVED_SERVICE_KEYWORDS
        )
    )


def service_to_k8s_deployment_name(service: str) -> str:
    """Converts a service name or alias to Kubernetes Deployment name.

    Converts underscores to dashes and adds '-deployment' suffix, with exceptions
    for special gateway components.

    Examples:
        'dashboard_service' -> 'dashboard-service-deployment'
        'experiment_state_server' -> 'experiment-state-server-deployment'
        'ingestion_gateway' -> 'gateway-proxy'
        'components' -> 'xmc-bridge-deployment'
        'ui' -> 'ui-deployment'

    Args:
        service: Service alias, name, or deployment name.

    Returns:
        Kubernetes Deployment resource name.
    """
    canonical = resolve_service_name(service)
    svc_lower = canonical.lower().replace("_", "-")
    if svc_lower in ("ingestion-gateway", "gateway-proxy"):
        return "gateway-proxy"
    if svc_lower in ("components", "xmc-bridge"):
        return "xmc-bridge-deployment"
    if svc_lower.endswith("-deployment"):
        return svc_lower
    return f"{svc_lower}-deployment"


def resolve_target_services(
    services: list[str] | None,
    enabled_services: list[str],
    helm: bool = False,
) -> tuple[set[str], bool]:
    """Resolves target services and Helm requirement from arguments.

    Args:
        services: Optional list of service names, aliases, or special keywords ('all', 'helm').
        enabled_services: List of currently enabled services in the configuration.
        helm: Whether Helm upgrade was explicitly requested.

    Returns:
        Tuple of (resolved target service set, boolean helm_needed).

    Raises:
        ValueError: If no services are specified without --helm, or if requested
            services are invalid or disabled.
    """
    raw_services = services or []
    if not raw_services and not helm:
        raise ValueError(
            "No services specified to reload. Specify microservice names (e.g. 'ess', 'ui', 'all') or use --helm."
        )

    helm_needed = helm or any(
        s.strip().lower() in ("helm", "chart", "charts") for s in raw_services if s and s.strip()
    )

    target_services: set[str] = set()
    explicit_services = [
        s.strip()
        for s in raw_services
        if s and s.strip() and s.strip().lower() not in ("all", "helm", "chart", "charts")
    ]
    if explicit_services:
        resolved = resolve_service_names(explicit_services)
        invalid_or_disabled = [s for s in resolved if s not in enabled_services]
        if invalid_or_disabled:
            raise ValueError(
                "The following services are invalid or not enabled in the current"
                f" configuration: {', '.join(invalid_or_disabled)}. Enabled services:"
                f" {', '.join(enabled_services)}"
            )
        target_services.update(resolved)

    lowercase_services = {s.strip().lower() for s in raw_services if s and s.strip()}
    if "all" in lowercase_services:
        target_services.update(enabled_services)

    return target_services, helm_needed
