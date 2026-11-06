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

"""Tests for server_env."""

import unittest

import server_env


class LoadServerEnvTest(unittest.TestCase):
    def test_empty_environment_uses_defaults(self):
        self.assertEqual(
            server_env.load_server_env({}),
            server_env.ServerEnv(port=7077, database_url="sqlite:////data/vizier.db"),
        )

    def test_reads_values(self):
        env = {"VIZIER_PORT": "8080", "VIZIER_DATABASE_URL": "sqlite:////tmp/v.db"}

        self.assertEqual(
            server_env.load_server_env(env),
            server_env.ServerEnv(port=8080, database_url="sqlite:////tmp/v.db"),
        )

    def test_blank_values_use_defaults(self):
        env = {"VIZIER_PORT": "  ", "VIZIER_DATABASE_URL": " "}

        self.assertEqual(
            server_env.load_server_env(env),
            server_env.ServerEnv(port=7077, database_url="sqlite:////data/vizier.db"),
        )


class ParsePortTest(unittest.TestCase):
    def test_accepts_bounds_and_surrounding_whitespace(self):
        for raw, want in (("1", 1), ("65535", 65535), (" 7077 ", 7077)):
            with self.subTest(raw=raw):
                self.assertEqual(server_env.parse_port(raw), want)

    def test_rejects_non_integers(self):
        for raw in ("abc", "70.7", "7077a", "٧٠٧٧", "７０７７"):
            with (
                self.subTest(raw=raw),
                self.assertRaisesRegex(ValueError, "VIZIER_PORT must be an integer"),
            ):
                server_env.parse_port(raw)

    def test_rejects_out_of_range_ports(self):
        for raw in ("0", "-1", "65536"):
            with (
                self.subTest(raw=raw),
                self.assertRaisesRegex(ValueError, "VIZIER_PORT must be between 1 and 65535"),
            ):
                server_env.parse_port(raw)


if __name__ == "__main__":
    unittest.main()
