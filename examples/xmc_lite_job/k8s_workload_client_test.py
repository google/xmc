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

"""Unit tests for the XMC Lite Kubernetes Job and JobSet workload client."""

import unittest
from unittest import mock

from absl import flags
from kubernetes import client as k8s_client
from xmanager import xm, xm_local
from xmanager.cloud import kubernetes as xm_k8s
from xmanager.xm_local import executables as local_executables

from examples.xmc_lite_job import k8s_workload_client as wc

_EXP_PREFIX = "1790367844544_1"


def _full_job_name(name: str) -> str:
    return f"{_EXP_PREFIX}_{name}"


def _xm_job(name: str = "worker") -> xm.Job:
    executable = local_executables.GoogleContainerRegistryImage(
        name=name,
        image_path="python:3.11-slim",
        args=xm.SequentialArgs.from_collection(["python3", "-c", "print(1)"]),
    )
    executor = xm_local.Kubernetes(requirements=xm.JobRequirements(cpu=1, ram=1 * xm.GiB))
    return xm.Job(executable=executable, executor=executor, name=name)


def _v1_job(name: str, pod_spec: k8s_client.V1PodSpec | None) -> k8s_client.V1Job:
    return k8s_client.V1Job(
        metadata=k8s_client.V1ObjectMeta(name=name),
        spec=k8s_client.V1JobSpec(template=k8s_client.V1PodTemplateSpec(spec=pod_spec)),
    )


def _client(**config_overrides: object) -> wc.XmcLiteKubernetesClient:
    config = wc.WorkloadClientConfig(**{"dry_run": True, **config_overrides})
    return wc.XmcLiteKubernetesClient(config=config, api_client=k8s_client.ApiClient())


def setUpModule() -> None:
    # The SDK reads xm_flags.K8S_SERVICE_ACCOUNT_NAME while building pod specs.
    flags.FLAGS.mark_as_parsed()


class Dns1035JobsetNameTest(unittest.TestCase):
    """Covers DNS-1035 normalization of JobSet names."""

    def test_prefixes_names_starting_with_a_digit(self):
        self.assertEqual(
            "xm-1790367844544-1-jobset", wc._dns1035_jobset_name(_full_job_name("jobset"))
        )

    def test_strips_surrounding_hyphens(self):
        self.assertEqual("abc", wc._dns1035_jobset_name("-abc-"))

    def test_falls_back_when_empty(self):
        self.assertEqual("xm-jobset", wc._dns1035_jobset_name("--"))

    def test_truncates_to_63_chars_without_trailing_hyphen(self):
        name = wc._dns1035_jobset_name("a" * 62 + "-b")
        self.assertLessEqual(len(name), 63)
        self.assertFalse(name.endswith("-"))


class ExtractShortJobNameTest(unittest.TestCase):
    """Covers extraction of the job suffix from full job names."""

    def test_returns_suffix_after_experiment_and_work_unit(self):
        self.assertEqual("worker", wc._extract_short_job_name("1790367844544-1-worker"))

    def test_returns_input_when_not_three_parts(self):
        self.assertEqual("worker", wc._extract_short_job_name("worker"))


class BuildJobsetManifestTest(unittest.TestCase):
    """Covers the non-restarting JobSet manifest contract."""

    def setUp(self):
        self.api_client = k8s_client.ApiClient()
        pod_spec = k8s_client.V1PodSpec(containers=[k8s_client.V1Container(name="c", image="i")])
        self.jobs = [_v1_job("1790367844544-1-worker", pod_spec)]

    def test_sets_non_restarting_policies(self):
        manifest = wc.build_jobset_manifest(
            self.api_client, "js", self.jobs, wc.WorkloadClientConfig()
        )

        self.assertEqual(0, manifest["spec"]["failurePolicy"]["maxRestarts"])
        [replicated] = manifest["spec"]["replicatedJobs"]
        self.assertEqual("worker", replicated["name"])
        self.assertEqual(0, replicated["template"]["spec"]["backoffLimit"])
        self.assertEqual(
            "Never", replicated["template"]["spec"]["template"]["spec"]["restartPolicy"]
        )

    def test_adds_kueue_label_only_when_queue_is_set(self):
        without_queue = wc.build_jobset_manifest(
            self.api_client, "js", self.jobs, wc.WorkloadClientConfig()
        )
        with_queue = wc.build_jobset_manifest(
            self.api_client, "js", self.jobs, wc.WorkloadClientConfig(local_queue="default-queue")
        )

        self.assertNotIn(wc.KUEUE_QUEUE_LABEL, without_queue["metadata"]["labels"])
        self.assertEqual("default-queue", with_queue["metadata"]["labels"][wc.KUEUE_QUEUE_LABEL])

    def test_creates_pod_spec_when_missing(self):
        jobs = [_v1_job("1790367844544-1-worker", None)]

        manifest = wc.build_jobset_manifest(self.api_client, "js", jobs, wc.WorkloadClientConfig())

        pod_spec = manifest["spec"]["replicatedJobs"][0]["template"]["spec"]["template"]["spec"]
        self.assertEqual({"restartPolicy": "Never"}, pod_spec)


class EnsureExperimentsServiceTest(unittest.TestCase):
    """Covers idempotent creation of the headless experiments Service."""

    def setUp(self):
        patcher = mock.patch.object(wc.k8s_client, "CoreV1Api", autospec=True)
        self.core_api = patcher.start().return_value
        self.addCleanup(patcher.stop)
        self.client = _client(dry_run=False)

    def test_skips_creation_when_service_exists(self):
        self.client._ensure_experiments_service()

        self.core_api.create_namespaced_service.assert_not_called()

    def test_creates_service_when_missing(self):
        self.core_api.read_namespaced_service.side_effect = k8s_client.ApiException(status=404)

        self.client._ensure_experiments_service()

        self.core_api.create_namespaced_service.assert_called_once()

    def test_ignores_conflict_on_concurrent_creation(self):
        self.core_api.read_namespaced_service.side_effect = k8s_client.ApiException(status=404)
        self.core_api.create_namespaced_service.side_effect = k8s_client.ApiException(status=409)

        self.client._ensure_experiments_service()

    def test_raises_unexpected_read_errors(self):
        self.core_api.read_namespaced_service.side_effect = k8s_client.ApiException(status=500)

        with self.assertRaises(k8s_client.ApiException):
            self.client._ensure_experiments_service()


class LaunchTest(unittest.TestCase):
    """Covers JobSet and batch/v1 Job submission and dry-run recording."""

    def test_dry_run_jobset_records_manifest_without_submitting(self):
        client = _client(workload_mode="jobset", namespace="team-a")

        with mock.patch.object(wc.k8s_client, "CustomObjectsApi", autospec=True) as custom_api:
            [handle_job] = client.launch(_full_job_name, [_xm_job()])

        custom_api.assert_not_called()
        [manifest] = client.recorded_manifests
        self.assertEqual("JobSet", manifest["kind"])
        self.assertEqual("team-a", manifest["metadata"]["namespace"])
        self.assertEqual(manifest["metadata"]["name"], handle_job.metadata.name)

    def test_jobset_mode_submits_custom_object(self):
        client = _client(dry_run=False, workload_mode="jobset")

        with (
            mock.patch.object(client, "_ensure_experiments_service") as ensure_service,
            mock.patch.object(wc.k8s_client, "CustomObjectsApi", autospec=True) as custom_api,
        ):
            client.launch(_full_job_name, [_xm_job()])

        ensure_service.assert_called_once()
        kwargs = custom_api.return_value.create_namespaced_custom_object.call_args.kwargs
        self.assertEqual(
            (wc.JOBSET_GROUP, wc.JOBSET_VERSION, wc.JOBSET_PLURAL),
            (kwargs["group"], kwargs["version"], kwargs["plural"]),
        )

    def test_job_mode_submits_batch_jobs_to_configured_namespace(self):
        client = _client(dry_run=False, workload_mode="job", namespace="team-a")

        with (
            mock.patch.object(client, "_ensure_experiments_service"),
            mock.patch.object(wc.k8s_client, "BatchV1Api", autospec=True) as batch_api,
        ):
            [job] = client.launch(_full_job_name, [_xm_job()])

        # The SDK's own build-phase call also hits the patched BatchV1Api; the
        # client's real submission is the last call.
        batch_api.return_value.create_namespaced_job.assert_called_with(
            namespace="team-a", body=job
        )
        self.assertEqual("team-a", job.metadata.namespace)


class ActiveXmcLiteClientTest(unittest.TestCase):
    """Covers binding the custom client as the SDK's Kubernetes client."""

    def test_binds_client_for_the_context_only(self):
        config = wc.WorkloadClientConfig(dry_run=True)
        original_factory = xm_k8s.client

        with wc.active_xmc_lite_client(config, api_client=k8s_client.ApiClient()) as client:
            self.assertIs(client, xm_k8s.client())

        self.assertIs(original_factory, xm_k8s.client)


if __name__ == "__main__":
    unittest.main()
