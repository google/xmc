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

from xmc.types import Metric, format_metric_batch_payload, is_valid_metric_name


class TestMetricValidation(unittest.TestCase):
    def test_valid_metric(self):
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
        )
        self.assertEqual(len(payload), 2)
        self.assertEqual(payload[0]["xid"], "1001")
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
