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

import unittest
from unittest.mock import MagicMock, patch
from extract_telemetry import extract_telemetry, _fetch_comments, _parse_telemetry
from tool_utils import HandledError

class TestExtractTelemetry(unittest.TestCase):

    @patch('extract_telemetry.call_github_api')
    def test_fetch_comments_success(self, mock_call):
        mock_call.return_value = [{"id": 1, "body": "comment"}]
        res = _fetch_comments("google", "xmc", "2026-06-22T00:00:00Z")
        self.assertEqual(len(res), 1)
        mock_call.assert_called_once_with(
            "/repos/google/xmc/pulls/comments?per_page=100&page=1&since=2026-06-22T00:00:00Z",
            user_agent="XMC-Telemetry-Extractor"
        )

    @patch('extract_telemetry.call_github_api')
    def test_fetch_comments_pagination(self, mock_call):
        # Page 1: returns 100 comments, Page 2: returns 5 comments, Page 3: empty
        page1_batch = [{"id": i, "body": "comment"} for i in range(100)]
        page2_batch = [{"id": i + 100, "body": "comment"} for i in range(5)]
        mock_call.side_effect = [
            page1_batch,
            page2_batch,
            []
        ]
        
        res = _fetch_comments("google", "xmc")
        self.assertEqual(len(res), 105)
        
        mock_call.assert_any_call(
            "/repos/google/xmc/pulls/comments?per_page=100&page=1",
            user_agent="XMC-Telemetry-Extractor"
        )
        mock_call.assert_any_call(
            "/repos/google/xmc/pulls/comments?per_page=100&page=2",
            user_agent="XMC-Telemetry-Extractor"
        )

    @patch('extract_telemetry.call_github_api')
    def test_fetch_comments_error(self, mock_call):
        mock_call.side_effect = Exception("API error")
        with self.assertRaises(HandledError) as ctx:
            _fetch_comments("google", "xmc")
        self.assertIn("Failed to fetch repository comments", ctx.exception.message)

    def test_parse_telemetry_valid(self):
        comments = [
            {
                "id": 1,
                "body": 'done\n\n<!-- xmc-telemetry: {"git_username": "chad", "date": "2026-06-22", "time": "12:00:00 UTC", "pr_number": "29", "comment_id": 1234} -->'
            }
        ]
        res = _parse_telemetry(comments)
        self.assertEqual(len(res), 1)
        self.assertEqual(res[0]["github_username"], "chad")
        self.assertEqual(res[0]["pr_number"], "29")
        self.assertEqual(res[0]["comment_id"], 1234)
        self.assertEqual(res[0]["skill_name"], "NA")
        self.assertEqual(res[0]["skill_commit_id"], "NA")

    def test_parse_telemetry_with_skill_metadata(self):
        comments = [
            {
                "id": 8,
                "body": 'done\n\n<!-- xmc-telemetry: {"git_username": "chad", "date": "2026-06-22", "time": "12:00:00 UTC", "pr_number": "29", "comment_id": 1234, "skill_name": "address_pr_comment", "skill_commit_id": "mock_hash"} -->'
            }
        ]
        res = _parse_telemetry(comments)
        self.assertEqual(len(res), 1)
        self.assertEqual(res[0]["github_username"], "chad")
        self.assertEqual(res[0]["pr_number"], "29")
        self.assertEqual(res[0]["comment_id"], 1234)
        self.assertEqual(res[0]["skill_name"], "address_pr_comment")
        self.assertEqual(res[0]["skill_commit_id"], "mock_hash")

    def test_parse_telemetry_no_block(self):
        comments = [
            {
                "id": 3,
                "body": "Normal review comment"
            }
        ]
        res = _parse_telemetry(comments)
        self.assertEqual(len(res), 0)

    def test_parse_telemetry_corrupted_json(self):
        comments = [
            {
                "id": 4,
                "body": "done\n\n<!-- xmc-telemetry: {corrupted_json} -->"
            }
        ]
        res = _parse_telemetry(comments)
        self.assertEqual(len(res), 0)

    def test_parse_telemetry_none_body(self):
        comments = [
            {
                "id": 5,
                "body": None
            }
        ]
        res = _parse_telemetry(comments)
        self.assertEqual(len(res), 0)

    def test_parse_telemetry_non_dict_payload(self):
        comments = [
            {
                "id": 6,
                "body": "done\n\n<!-- xmc-telemetry: [1, 2, 3] -->"
            },
            {
                "id": 7,
                "body": 'done\n\n<!-- xmc-telemetry: "not_a_dictionary" -->'
            }
        ]
        res = _parse_telemetry(comments)
        self.assertEqual(len(res), 0)

    @patch('extract_telemetry._fetch_comments')
    @patch('extract_telemetry._parse_telemetry')
    def test_extract_telemetry_orchestration(self, mock_parse, mock_fetch):
        args = MagicMock()
        args.owner = "google"
        args.repo = "xmc"
        args.since = "since_date"
        args.output = None
        
        mock_fetch.return_value = [{"id": 1}]
        mock_parse.return_value = [{"username": "user"}]
        
        res = extract_telemetry(args)
        self.assertEqual(res, [{"username": "user"}])
        mock_fetch.assert_called_once_with("google", "xmc", "since_date")
        mock_parse.assert_called_once_with([{"id": 1}])

    @patch('extract_telemetry._fetch_comments')
    @patch('builtins.open', new_callable=MagicMock)
    @patch('extract_telemetry.json.dump')
    def test_extract_telemetry_write_file(self, mock_dump, mock_open, mock_fetch):
        args = MagicMock()
        args.owner = "google"
        args.repo = "xmc"
        args.since = None
        args.output = "output.json"

        mock_fetch.return_value = [
            {
                "id": 1,
                "body": 'done\n\n<!-- xmc-telemetry: {"git_username": "chad", "date": "2026-06-22", "time": "12:00:00 UTC", "pr_number": "29", "comment_id": 1234} -->'
            }
        ]

        res = extract_telemetry(args)
        self.assertEqual(res, "Successfully wrote 1 records to output.json")
        mock_open.assert_called_once_with("output.json", "w")
        mock_dump.assert_called_once()

if __name__ == '__main__':
    unittest.main()
