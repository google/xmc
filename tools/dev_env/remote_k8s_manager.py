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

"""Remote Kubernetes cluster authentication and static kubeconfig generator.

Enables hybrid execution where lightweight control plane services run in KinD
while heavy workload orchestration (JobSet, Kueue, and physical nodes) targets
a remote GKE or external Kubernetes cluster.
"""

from __future__ import annotations

import json
import logging
import subprocess

from tools.common.tool_utils import log_info, log_success, run_command

_LOGGER = logging.getLogger("xmc.dev_env.remote_k8s")


def extract_remote_cluster_endpoint_and_ca(context: str) -> tuple[str, str]:
    """Extracts the API server URL and base64 CA certificate from host kubeconfig.

    Runs `kubectl config view --raw --minify --context <context> -o json` to retrieve
    the cluster connection parameters without relying on local file paths.

    Args:
        context: The kubectl context name of the target remote cluster.

    Returns:
        Tuple of (server_url, ca_data_base64).

    Raises:
        RuntimeError: If cluster information cannot be queried or is missing required fields.
    """
    cmd = ["kubectl", "config", "view", "--raw", "--minify", "--context", context, "-o", "json"]
    try:
        res = run_command(cmd, check=True, capture_output=True, log_cmd=False)
        stdout = res.stdout.decode("utf-8") if isinstance(res.stdout, bytes) else res.stdout
        clusters = json.loads(stdout).get("clusters", [])
    except Exception as e:
        raise RuntimeError(
            f"Failed to query remote cluster configuration for context '{context}': {e}"
        ) from e

    if not clusters:
        raise RuntimeError(f"No clusters found in kubeconfig for context '{context}'.")

    cluster_info = clusters[0].get("cluster", {})
    server = cluster_info.get("server")
    ca_data = cluster_info.get("certificate-authority-data")

    if not server:
        raise RuntimeError(f"Remote cluster API server endpoint missing for context '{context}'.")
    if not ca_data:
        raise RuntimeError(
            f"Remote cluster certificate-authority-data missing for context '{context}'."
        )

    return server, ca_data


def mint_service_account_token(
    context: str,
    namespace: str = "xmc",
    service_account: str = "actuator-service-account",
    duration: str = "24h",
) -> str:
    """Mints a self-contained ServiceAccount bearer token on the remote cluster.

    Uses the Kubernetes TokenRequest API via `kubectl create token` to generate
    a secure bearer token that requires zero GCP/gcloud credentials inside the
    container.

    Args:
        context: The kubectl context name of the target remote cluster.
        namespace: The namespace where the ServiceAccount resides.
        service_account: Name of the ServiceAccount to authenticate as.
        duration: Token validity period (default: 24h).

    Returns:
        The minted JWT bearer token string.

    Raises:
        RuntimeError: If token generation fails.
    """
    cmd = [
        "kubectl",
        "--context",
        context,
        "create",
        "token",
        service_account,
        "-n",
        namespace,
        f"--duration={duration}",
    ]
    try:
        res = run_command(cmd, check=True, capture_output=True, log_cmd=False)
        stdout = res.stdout.decode("utf-8") if isinstance(res.stdout, bytes) else res.stdout
        token = stdout.strip()
        if not token:
            raise RuntimeError("Token output was empty.")
        return token
    except Exception as e:
        raise RuntimeError(
            f"Failed to mint token for ServiceAccount '{service_account}' in namespace '{namespace}' "
            f"on cluster '{context}': {e}"
        ) from e


def generate_static_kubeconfig(
    server: str,
    ca_data: str,
    token: str,
    namespace: str = "xmc",
    cluster_name: str = "remote-cluster",
    user_name: str = "remote-user",
    context_name: str = "remote-context",
) -> str:
    """Generates a standalone static kubeconfig YAML string."""
    return f"""apiVersion: v1
kind: Config
clusters:
- cluster:
    certificate-authority-data: {ca_data}
    server: {server}
  name: {cluster_name}
contexts:
- context:
    cluster: {cluster_name}
    namespace: {namespace}
    user: {user_name}
  name: {context_name}
current-context: {context_name}
users:
- name: {user_name}
  user:
    token: {token}
"""


def setup_remote_k8s_secret(
    kind_context: str,
    remote_context: str,
    remote_namespace: str = "xmc",
    target_namespace: str = "xmc",
    service_account: str = "actuator-service-account",
    secret_name: str = "remote-k8s-config",
) -> None:
    """Generates static remote kubeconfig and creates the secret in KinD."""
    log_info(f"Configuring remote Kubernetes credentials for context '{remote_context}'...")
    server, ca_data = extract_remote_cluster_endpoint_and_ca(remote_context)
    token = mint_service_account_token(
        remote_context,
        namespace=remote_namespace,
        service_account=service_account,
    )
    kubeconfig_yaml = generate_static_kubeconfig(
        server=server,
        ca_data=ca_data,
        token=token,
        namespace=remote_namespace,
    )

    k_cmd = ["kubectl", "--context", kind_context]
    run_command([*k_cmd, "create", "namespace", target_namespace], check=False, log_cmd=False)
    run_command(
        [*k_cmd, "delete", "secret", secret_name, "-n", target_namespace],
        check=False,
        log_cmd=False,
    )

    create_cmd = [
        *k_cmd,
        "create",
        "secret",
        "generic",
        secret_name,
        "-n",
        target_namespace,
        f"--from-literal=kubeconfig={kubeconfig_yaml}",
    ]
    try:
        run_command(create_cmd, check=True, log_cmd=False, log_on_error=False)
        run_command(
            [
                *k_cmd,
                "annotate",
                "secret",
                secret_name,
                "-n",
                target_namespace,
                "meta.helm.sh/release-name=xmc",
                f"meta.helm.sh/release-namespace={target_namespace}",
                "--overwrite",
            ],
            check=False,
            log_cmd=False,
        )
        run_command(
            [
                *k_cmd,
                "label",
                "secret",
                secret_name,
                "-n",
                target_namespace,
                "app.kubernetes.io/managed-by=Helm",
                "--overwrite",
            ],
            check=False,
            log_cmd=False,
        )
    except subprocess.CalledProcessError:
        raise RuntimeError(
            f"Failed to create secret '{secret_name}' in namespace '{target_namespace}'"
        ) from None

    log_success(
        f"Remote Kubernetes credentials configured: secret '{secret_name}' in namespace '{target_namespace}'."
    )
