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

import unittest

from xmc.types import _MAX_CACHED_METRIC_NAMES, Metric, _validated_metric_names, validate_metric


class TestMetricValidation(unittest.TestCase):
    def setUp(self):
        _validated_metric_names.clear()

    def test_valid_metric(self):
        # Note: proto fields use snake_case in Python, but we can also use
        # positional or keyword args.
        # work_unit_id in proto becomes work_unit_id (or workUnitId depending on
        # proto 3 syntax but usually snake_case in python)
        metric = Metric(
            xid=100,
            work_unit_id=1,
            execution_id=2,
            replica_id="0",
            metric_name="eval/accuracy",
            metric_value=0.95,
            step=50,
        )
        metric.validate()
        self.assertIn("eval/accuracy", _validated_metric_names)

    def test_set_caching(self):
        m1 = Metric(metric_name="loss", metric_value=0.1, step=1)
        m2 = Metric(metric_name="loss", metric_value=0.08, step=2)

        validate_metric(m1)
        self.assertEqual(len(_validated_metric_names), 1)
        self.assertIn("loss", _validated_metric_names)

        # Second validation hits cache
        validate_metric(m2)
        self.assertEqual(len(_validated_metric_names), 1)

    def test_cache_cap_eviction(self):
        # Pre-fill cache up to MAX_CACHED_METRIC_NAMES
        for i in range(_MAX_CACHED_METRIC_NAMES):
            _validated_metric_names.add(f"metric_{i}")

        self.assertEqual(len(_validated_metric_names), _MAX_CACHED_METRIC_NAMES)

        # Adding a new metric name should reset the cache and add the new one
        m_new = Metric(metric_name="new_metric", metric_value=1.0, step=0)
        validate_metric(m_new)

        self.assertEqual(len(_validated_metric_names), 1)
        self.assertIn("new_metric", _validated_metric_names)

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
