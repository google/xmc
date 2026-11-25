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

"""Unit tests for xmc.types module using proto."""

import importlib.abc
import importlib.util
import sys
import types
import unittest
from unittest import mock

from xmc import types as xmc_types
from xmc.types import Metric, format_metric_batch_payload, is_valid_metric_name


def _exec_fresh_types_module() -> types.ModuleType:
    """Executes xmc/types.py into a new module object, leaving sys.modules intact."""
    spec = importlib.util.find_spec("xmc.types")
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class TestMetricsProtoImport(unittest.TestCase):
    def test_bazel_build_falls_back_to_api_protos(self):
        self.assertEqual(xmc_types.metrics_pb2.__name__, "api.protos.metrics.metrics_pb2")

    def test_missing_protos_package_falls_back_to_api_protos(self):
        # None in sys.modules makes `import xmc._protos...` raise ModuleNotFoundError
        # naming "xmc._protos", as when the package is absent from an install.
        with mock.patch.dict(sys.modules, {"xmc._protos": None}):
            module = _exec_fresh_types_module()

        self.assertEqual(module.metrics_pb2.__name__, "api.protos.metrics.metrics_pb2")

    def test_descriptor_name_matches_packaged_build(self):
        # setup.py compiles with -I <repo_root>, so both copies share this name and can
        # be loaded in one process.
        self.assertEqual(Metric.DESCRIPTOR.file.name, "api/protos/metrics/metrics.proto")

    def test_prefers_package_local_module(self):
        packaged = types.ModuleType("xmc._protos.metrics_pb2")
        packaged.Metric = type("Metric", (), {})  # type: ignore[attr-defined]

        with mock.patch.dict(sys.modules, {"xmc._protos.metrics_pb2": packaged}):
            module = _exec_fresh_types_module()

        self.assertIs(module.metrics_pb2, packaged)
        self.assertIs(module.Metric, packaged.Metric)

    def test_errors_inside_packaged_module_propagate(self):
        errors = {
            "protobuf missing": ModuleNotFoundError("No module named 'google'", name="google"),
            "protobuf too old": ImportError("cannot import name 'runtime_version'"),
        }
        for label, error in errors.items():
            with self.subTest(label), self.assertRaises(type(error)) as raised:
                with mock.patch.object(sys, "meta_path", [_FailingFinder(error), *sys.meta_path]):
                    _exec_fresh_types_module()
            self.assertIs(raised.exception, error)


class _FailingLoader(importlib.abc.Loader):
    """Loader whose module body raises the given error, like a broken generated module."""

    def __init__(self, error: ImportError) -> None:
        self._error = error

    def create_module(self, spec):
        return None

    def exec_module(self, module):
        raise self._error


class _FailingFinder(importlib.abc.MetaPathFinder):
    """Resolves xmc._protos.metrics_pb2 to a module that fails while executing."""

    def __init__(self, error: ImportError) -> None:
        self._error = error

    def find_spec(self, fullname, path, target=None):
        if fullname != "xmc._protos.metrics_pb2":
            return None
        return importlib.util.spec_from_loader(fullname, _FailingLoader(self._error))


class TestMetricValidation(unittest.TestCase):
    def test_valid_metric(self):
        metric = Metric(
            xid=100,
            work_unit_id=1,
            execution_id=2,
            job_name="worker_job",
            replica_id="0",
            metric_name="eval/accuracy",
            metric_value=0.95,
            step=50,
        )
        metric.validate()
        self.assertEqual(metric.job_name, "worker_job")
        self.assertTrue(is_valid_metric_name("eval/accuracy"))

    def test_lru_caching(self):
        self.assertTrue(is_valid_metric_name("loss"))
        # Second call hits cache
        self.assertTrue(is_valid_metric_name("loss"))
        cache_info = is_valid_metric_name.cache_info()
        self.assertGreater(cache_info.hits, 0)

    def test_format_metric_batch_payload(self):
        items = [
            ("loss", 0.123, 10, 100.0),
            ("accuracy", 0.95, 11, 101.0),
        ]
        payload = format_metric_batch_payload(
            items,
            xid="1001",
            work_unit_id="2002",
            execution_id="3003",
            replica_id="replica-0",
            job_name="my_job",
        )
        self.assertEqual(len(payload), 2)
        self.assertEqual(payload[0]["xid"], "1001")
        self.assertEqual(payload[0]["job_name"], "my_job")
        self.assertEqual(payload[0]["metric_name"], "loss")
        self.assertEqual(payload[0]["timestamp"], 100000000)

    def test_invalid_metric_name_regex(self):
        invalid_names = [
            "",
            "a" * 256,
            "metric with spaces",
            "metric:name",
            "metric#tag",
        ]
        for name in invalid_names:
            with self.subTest(name=name):
                m = Metric(metric_name=name, metric_value=1.0, step=0)
                with self.assertRaises(ValueError):
                    m.validate()

    def test_invalid_metric_values(self):
        invalid_values = [float("nan"), float("inf"), float("-inf")]
        for val in invalid_values:
            with self.subTest(val=val):
                m = Metric(metric_name="loss", metric_value=val, step=0)
                with self.assertRaises(ValueError):
                    m.validate()

    def test_invalid_step(self):
        m = Metric(metric_name="loss", metric_value=0.5, step=-1)
        with self.assertRaises(ValueError):
            m.validate()

    def test_zero_step_valid(self):
        m = Metric(metric_name="loss", metric_value=0.5, step=0)
        m.validate()


if __name__ == "__main__":
    unittest.main()
