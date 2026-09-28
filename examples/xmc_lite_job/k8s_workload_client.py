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

"""Kubernetes Job and JobSet workload client for XMC Lite (xm_local.Kubernetes)."""

from collections.abc import Callable, Iterator, Sequence
import contextlib
from dataclasses import dataclass
import os
from typing import Any
from unittest import mock

from kubernetes import client as k8s_client
from kubernetes import config as k8s_config
from xmanager import xm
from xmanager.cloud import kubernetes as xm_k8s

JOBSET_GROUP: str = "jobset.x-k8s.io"
JOBSET_VERSION: str = "v1alpha2"
JOBSET_PLURAL: str = "jobsets"
KUEUE_QUEUE_LABEL: str = "kueue.x-k8s.io/queue-name"
EXPERIMENTS_SERVICE_NAME: str = "experiments"


@dataclass(frozen=True)
class WorkloadClientConfig:
  """Immutable configuration for XMC Lite Kubernetes workload submission."""

  namespace: str = "default"
  workload_mode: str = "jobset"
  local_queue: str = ""
  dry_run: bool = False


def _dns1035_jobset_name(raw_name: str) -> str:
  """Formats a raw job name as a DNS-1035 label starting with an alphabetic char."""
  label = xm_k8s.convert_to_valid_label(raw_name).lower()
  return f"xm-{label}" if not label or not label[0].isalpha() else label


def _load_kube_config() -> None:
  """Loads Kubernetes configuration, honoring KUBECONFIG from the environment."""
  try:
    k8s_config.load_incluster_config()
  except k8s_config.ConfigException:
    k8s_config.load_kube_config(config_file=os.environ.get("KUBECONFIG"))


def _extract_short_job_name(full_job_name: str) -> str:
  """Extracts the RFC-1123 job suffix from '<exp_id>-<wu_id>-<job_name>'."""
  parts = full_job_name.split("-", 2)
  return parts[2] if len(parts) == 3 and parts[2] else full_job_name


def _build_labels(local_queue: str) -> dict[str, str]:
  """Builds workload metadata labels including optional Kueue queue assignment."""
  labels: dict[str, str] = {"service": EXPERIMENTS_SERVICE_NAME}
  if local_queue:
    labels[KUEUE_QUEUE_LABEL] = local_queue
  return labels


def _build_replicated_job_entry(
    api_client: k8s_client.ApiClient, v1_job: k8s_client.V1Job
) -> dict[str, Any]:
  """Converts an xmanager V1Job into a JobSet ReplicatedJob dictionary."""
  pod_template = api_client.sanitize_for_serialization(v1_job.spec.template)
  pod_template.setdefault("spec", {})["restartPolicy"] = "Never"
  return {
      "name": _extract_short_job_name(v1_job.metadata.name),
      "replicas": 1,
      "template": {
          "spec": {
              "parallelism": 1,
              "completions": 1,
              "backoffLimit": 0,
              "template": pod_template,
          }
      },
  }


def build_jobset_manifest(
    api_client: k8s_client.ApiClient,
    jobset_name: str,
    v1_jobs: Sequence[k8s_client.V1Job],
    config: WorkloadClientConfig,
) -> dict[str, Any]:
  """Constructs a non-restarting JobSet manifest from xmanager V1Job objects.

  Args:
    api_client: Kubernetes ApiClient for PodTemplateSpec serialization.
    jobset_name: DNS-1035 compliant name for the JobSet resource.
    v1_jobs: Sequence of V1Job objects produced for the WorkUnit.
    config: WorkloadClientConfig specifying namespace and Kueue queue.

  Returns:
    A dictionary representing the jobset.x-k8s.io/v1alpha2 JobSet manifest.
  """
  replicated = [_build_replicated_job_entry(api_client, j) for j in v1_jobs]
  return {
      "apiVersion": f"{JOBSET_GROUP}/{JOBSET_VERSION}",
      "kind": "JobSet",
      "metadata": {
          "name": jobset_name,
          "namespace": config.namespace,
          "labels": _build_labels(config.local_queue),
      },
      "spec": {"failurePolicy": {"maxRestarts": 0}, "replicatedJobs": replicated},
  }


class XmcLiteKubernetesClient(xm_k8s.Client):
  """Kubernetes client supporting both batch/v1 Job and JobSet in XMC Lite."""

  def __init__(
      self,
      config: WorkloadClientConfig,
      api_client: k8s_client.ApiClient | None = None,
  ) -> None:
    """Initializes the XMC Lite Kubernetes workload client."""
    if api_client is None:
      if not config.dry_run:
        _load_kube_config()
      api_client = k8s_client.ApiClient()
    super().__init__(api_client=api_client)
    self.config = config
    self.namespace = config.namespace
    self.recorded_manifests: list[dict[str, Any]] = []

  def _ensure_experiments_service(self) -> None:
    """Ensures the headless 'experiments' V1Service exists in the namespace."""
    core_api = k8s_client.CoreV1Api(self.api_client)
    items = core_api.list_namespaced_service(namespace=self.namespace).items
    if any(item.metadata.name == EXPERIMENTS_SERVICE_NAME for item in items):
      return
    body = k8s_client.V1Service(
        metadata=k8s_client.V1ObjectMeta(name=EXPERIMENTS_SERVICE_NAME),
        spec=k8s_client.V1ServiceSpec(
            selector={"service": EXPERIMENTS_SERVICE_NAME}, cluster_ip="None"
        ),
    )
    core_api.create_namespaced_service(namespace=self.namespace, body=body)

  def _build_v1_jobs(
      self, get_full_job_name: Callable[[str], str], jobs: Sequence[xm.Job]
  ) -> list[k8s_client.V1Job]:
    """Builds V1Job objects via super().launch without immediate submission."""
    with (
        mock.patch.object(self, "_create_service"),
        mock.patch.object(k8s_client.BatchV1Api, "create_namespaced_job"),
    ):
      v1_jobs = super().launch(get_full_job_name, jobs)
    labels = _build_labels(self.config.local_queue)
    for job in v1_jobs:
      job.api_version, job.kind = "batch/v1", "Job"
      job.metadata.namespace, job.metadata.labels = self.namespace, labels
    return v1_jobs

  def _launch_batch_jobs(
      self, v1_jobs: Sequence[k8s_client.V1Job]
  ) -> list[k8s_client.V1Job]:
    """Submits or records standard batch/v1 Jobs."""
    if not self.config.dry_run:
      self._ensure_experiments_service()
    batch_api = k8s_client.BatchV1Api(self.api_client)
    for job in v1_jobs:
      self.recorded_manifests.append(
          self.api_client.sanitize_for_serialization(job)
      )
      if not self.config.dry_run:
        batch_api.create_namespaced_job(namespace=self.namespace, body=job)
    return list(v1_jobs)

  def _launch_jobset(
      self,
      get_full_job_name: Callable[[str], str],
      v1_jobs: Sequence[k8s_client.V1Job],
  ) -> list[k8s_client.V1Job]:
    """Submits or records a jobset.x-k8s.io/v1alpha2 JobSet."""
    jobset_name = _dns1035_jobset_name(get_full_job_name("jobset"))
    manifest = build_jobset_manifest(
        self.api_client, jobset_name, v1_jobs, self.config
    )
    self.recorded_manifests.append(manifest)
    if not self.config.dry_run:
      self._ensure_experiments_service()
      custom_api = k8s_client.CustomObjectsApi(self.api_client)
      custom_api.create_namespaced_custom_object(
          group=JOBSET_GROUP,
          version=JOBSET_VERSION,
          namespace=self.namespace,
          plural=JOBSET_PLURAL,
          body=manifest,
      )
    meta = k8s_client.V1ObjectMeta(name=jobset_name, namespace=self.namespace)
    return [k8s_client.V1Job(metadata=meta)]

  def launch(
      self,
      get_full_job_name: Callable[[str], str],
      jobs: Sequence[xm.Job],
  ) -> list[k8s_client.V1Job]:
    """Launches xm.Job definitions as either a JobSet or batch/v1 Jobs.

    Args:
      get_full_job_name: Callback producing '<exp_id>_<wu_id>_<name>'.
      jobs: Sequence of xm.Job instances scheduled by the WorkUnit.

    Returns:
      List of V1Job objects persisted to SQLite via KubernetesHandle.
    """
    v1_jobs = self._build_v1_jobs(get_full_job_name, jobs)
    if not v1_jobs:
      return []
    if self.config.workload_mode == "job":
      return self._launch_batch_jobs(v1_jobs)
    return self._launch_jobset(get_full_job_name, v1_jobs)


@contextlib.contextmanager
def active_xmc_lite_client(
    config: WorkloadClientConfig,
    api_client: k8s_client.ApiClient | None = None,
) -> Iterator[XmcLiteKubernetesClient]:
  """Context manager that binds XmcLiteKubernetesClient as xm_k8s.client().

  Args:
    config: WorkloadClientConfig for the experiment session.
    api_client: Optional Kubernetes ApiClient override.

  Yields:
    The active XmcLiteKubernetesClient instance.
  """
  client_instance = XmcLiteKubernetesClient(
      config=config, api_client=api_client
  )
  with mock.patch.object(xm_k8s, "client", return_value=client_instance):
    yield client_instance
