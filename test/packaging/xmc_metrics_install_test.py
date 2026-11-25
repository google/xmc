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

"""Install smoke test for a built xmc-metrics wheel or sdist.

Runs outside Bazel, against xmc-metrics installed into a clean virtualenv, as
.github/workflows/xmc-metrics-package.yml does:

    python test/packaging/xmc_metrics_install_test.py
"""

import importlib.metadata
import sys
import sysconfig
import unittest
from pathlib import Path

import google.protobuf
import xmc
from xmc import types as xmc_types

_REPO_ROOT = Path(__file__).resolve().parents[2]


class XmcMetricsInstallTest(unittest.TestCase):
    def test_imports_installed_package_not_checkout(self):
        module_path = Path(xmc.__file__).resolve()
        self.assertFalse(module_path.is_relative_to(_REPO_ROOT))
        self.assertTrue(
            module_path.is_relative_to(Path(sysconfig.get_paths()["purelib"]).resolve())
        )

    def test_uses_packaged_metrics_proto(self):
        self.assertEqual(xmc_types.metrics_pb2.__name__, "xmc._protos.metrics_pb2")
        self.assertNotIn("api", sys.modules)

    def test_descriptor_name_matches_bazel_build(self):
        self.assertEqual(xmc.Metric.DESCRIPTOR.file.name, "api/protos/metrics/metrics.proto")

    def test_metric_validates_and_serializes(self):
        xmc.Metric(metric_name="loss", metric_value=0.5, step=1).validate()
        payload = xmc_types.format_metric_batch_payload(
            [("loss", 0.5, 1, 100.0)], xid="1", work_unit_id="2", execution_id="3", replica_id="0"
        )
        self.assertEqual(payload[0]["metric_name"], "loss")
        self.assertEqual(payload[0]["timestamp"], 100_000_000)

    def test_distribution_metadata(self):
        self.assertEqual(importlib.metadata.version("xmc-metrics"), xmc.__version__)
        major = int(google.protobuf.__version__.split(".")[0])
        self.assertIn(major, (5, 6))

    def test_build_script_and_unit_tests_not_shipped(self):
        files = importlib.metadata.files("xmc-metrics")
        self.assertIsNotNone(files, "xmc-metrics has no RECORD file list")
        shipped = [Path(str(f)).name for f in files or []]
        self.assertIn("types.py", shipped)
        self.assertNotIn("setup.py", shipped)
        self.assertEqual([name for name in shipped if name.endswith("_test.py")], [])


if __name__ == "__main__":
    unittest.main()
