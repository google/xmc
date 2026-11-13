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
"""Tests for local queue fixture filtering, ordering, and pagination."""

import base64
import unittest

import resource_fixtures as fixtures


class ResourceFixturesTest(unittest.TestCase):
    """Validate full-queue behavior independently of HTTP routing."""

    def test_quota_matches_running_requests_and_preserves_borrowing(self) -> None:
        queues = fixtures.cluster_queues()["queues"]
        gpu = queues[0]
        running = fixtures.queue_jobs(fixtures.GPU_QUEUE, {"filter": ["status = RUNNING"]})["jobs"]
        totals = {}
        for job in running:
            for resource in job["requestedResources"]:
                kind = resource["resourceType"]
                totals[kind] = totals.get(kind, 0) + resource["count"]
        for capacity in gpu["queueCapacity"]:
            self.assertEqual(totals[capacity["resourceType"]], capacity["usedCapacity"])
        self.assertEqual(
            gpu["queueCapacity"][0],
            {
                "resourceType": fixtures.H100,
                "totalCapacity": 16,
                "usedCapacity": 24,
                "freeCapacity": 0,
                "freeCapacityWithBorrowing": 8,
            },
        )
        self.assertEqual(fixtures.queue_jobs(fixtures.EMPTY_QUEUE, {})["totalQueueSize"], 0)
        cpu = fixtures.queue_jobs(fixtures.CPU_QUEUE, {})
        self.assertEqual(cpu["totalQueueSize"], 4)
        self.assertTrue(
            all(job["requestedResources"][0]["resourceType"] == "cpu" for job in cpu["jobs"])
        )

    def test_all_pages_are_complete_and_unique(self) -> None:
        token = ""
        jobs = []
        lengths = []
        while True:
            response = fixtures.queue_jobs(fixtures.GPU_QUEUE, {"pageToken": [token]})
            self.assertEqual(response["totalQueueSize"], 24)
            jobs.extend(response["jobs"])
            lengths.append(len(response["jobs"]))
            token = response["nextPageToken"]
            if not token:
                break
        self.assertEqual(lengths, [10, 10, 4])
        self.assertEqual(len({(job["xid"], job["workUnitId"]) for job in jobs}), 24)
        self.assertEqual([job["status"] for job in jobs[-6:]], ["SCHEDULING"] * 6)

    def test_each_order_applies_before_pagination_and_has_stable_ties(self) -> None:
        cases = {
            "author asc": "234911122/1 234911125/1 234911128/1 234911131/1 234911123/2 234911126/2",
            "author desc": "234911123/1 234911126/1 234911129/1 234911132/1 234911124/2 234911127/2",
            "status asc": "234911122/1 234911123/1 234911124/1 234911122/2 234911123/2 234911124/2",
            "status desc": "234911131/1 234911132/1 234911133/1 234911131/2 234911132/2 234911133/2",
            "priority asc": "234911123/2 234911125/2 234911127/2 234911129/2 234911131/2 234911133/2",
            "priority desc": "234911122/1 234911124/1 234911126/1 234911128/1 234911130/1 234911132/1",
        }
        for order, expected_ids in cases.items():
            with self.subTest(order=order):
                params = {"pageSize": ["3"], "orderBy": [order]}
                first = fixtures.queue_jobs(fixtures.GPU_QUEUE, params)
                second = fixtures.queue_jobs(
                    fixtures.GPU_QUEUE, {**params, "pageToken": [first["nextPageToken"]]}
                )
                self.assertEqual(
                    [f"{job['xid']}/{job['workUnitId']}" for job in first["jobs"] + second["jobs"]],
                    expected_ids.split(),
                )

    def test_combined_filter_finds_later_page_jobs_and_retains_positions(self) -> None:
        first = fixtures.queue_jobs(fixtures.GPU_QUEUE, {})["jobs"]
        result = fixtures.queue_jobs(
            fixtures.GPU_QUEUE,
            {
                "filter": ['author = "carol@example.com" AND status = SCHEDULING'],
                "orderBy": ["priority desc"],
            },
        )
        self.assertEqual(result["totalQueueSize"], 2)
        self.assertEqual({job["positionInQueue"] for job in result["jobs"]}, {3, 6})
        self.assertTrue(all(job not in first for job in result["jobs"]))
        empty = fixtures.queue_jobs(
            fixtures.GPU_QUEUE, {"filter": ['author = "nobody@example.com"']}
        )
        self.assertEqual(empty, {"jobs": [], "totalQueueSize": 0, "nextPageToken": ""})

    def test_quoted_author_escapes_and_and_are_literal(self) -> None:
        for expression in ['author = "Alice AND Bob"', r'author = "a\"b@example.com"']:
            with self.subTest(expression=expression):
                self.assertEqual(
                    fixtures.queue_jobs(fixtures.GPU_QUEUE, {"filter": [expression]})[
                        "totalQueueSize"
                    ],
                    0,
                )

    def test_invalid_query_controls(self) -> None:
        invalid = [
            {"filter": ["author = alice"]},
            {"filter": ["status = COMPLETED"]},
            {"filter": ['author = "alice" OR status = RUNNING']},
            {"filter": ["status = RUNNING AND status = STARTING"]},
            {"filter": ["status = RUNNING AND "]},
            {"filter": ["priority = 1"]},
            {"filter": ['author = "unterminated']},
            {"filter": ['author = "a\\q"']},
            {"orderBy": ["user asc"]},
            {"orderBy": ["author sideways"]},
            {"orderBy": ["priority desc extra"]},
            {"pageSize": ["-1"]},
            {"pageSize": ["invalid"]},
            {"pageSize": ["10", "20"]},
            {"pageToken": ["invalid!"]},
            {"pageToken": [base64.urlsafe_b64encode(b"-1").decode()]},
        ]
        for params in invalid:
            with self.subTest(params=params), self.assertRaises(ValueError):
                fixtures.queue_jobs(fixtures.GPU_QUEUE, params)

    def test_page_defaults_caps_and_raw_tokens(self) -> None:
        self.assertEqual(
            len(fixtures.queue_jobs(fixtures.GPU_QUEUE, {"pageSize": ["0"]})["jobs"]), 10
        )
        self.assertEqual(
            len(fixtures.queue_jobs(fixtures.GPU_QUEUE, {"pageSize": ["10000"]})["jobs"]), 24
        )
        raw_token = base64.urlsafe_b64encode(b"20").decode().rstrip("=")
        self.assertEqual(
            len(fixtures.queue_jobs(fixtures.GPU_QUEUE, {"pageToken": [raw_token]})["jobs"]), 4
        )

    def test_independent_responses(self) -> None:
        first = fixtures.cluster_queues()
        first["queues"].clear()
        self.assertEqual(len(fixtures.cluster_queues()["queues"]), 3)
        fixtures.queue_jobs(fixtures.GPU_QUEUE, {})["jobs"][0]["requestedResources"].clear()
        self.assertTrue(
            fixtures.queue_jobs(fixtures.GPU_QUEUE, {})["jobs"][0]["requestedResources"]
        )

    def test_secondary_range_and_queue_selection(self) -> None:
        for period in fixtures.RANGES:
            response = fixtures.secondary_data(
                {"queueNames": [fixtures.GPU_QUEUE], "range": period}
            )["queuePredictability"]
            self.assertEqual(list(response), [fixtures.GPU_QUEUE])
            metrics = response[fixtures.GPU_QUEUE]["queuePredictability"]
            self.assertEqual({entry["velocityRange"] for entry in metrics}, {period})
            self.assertEqual({entry["pendingJobs"] for entry in metrics}, {6})
            self.assertEqual(
                {entry["pendingResourceRequests"]["resourceType"] for entry in metrics},
                {fixtures.H100, fixtures.L4},
            )
        self.assertEqual(fixtures.secondary_data({}), {"queuePredictability": {}})
        self.assertEqual(
            fixtures.submit_queue(fixtures.GPU_QUEUE), {"submitQueue": "gpu-local-queue"}
        )

    def test_invalid_secondary_requests_and_unknown_queue(self) -> None:
        for body in ([], {"queueNames": "queue"}, {"queueNames": [1]}, {"range": "invalid"}):
            with self.subTest(body=body), self.assertRaises(ValueError):
                fixtures.secondary_data(body)
        for call in (
            lambda: fixtures.queue_jobs("missing", {}),
            lambda: fixtures.submit_queue("missing"),
            lambda: fixtures.secondary_data({"queueNames": ["missing"]}),
        ):
            with self.assertRaises(KeyError):
                call()


if __name__ == "__main__":
    unittest.main()
