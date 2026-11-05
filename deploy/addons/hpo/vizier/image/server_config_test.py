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

"""Tests for server_config."""

import unittest

import server_config

# Baseline PostgreSQL environment; cases express themselves as deltas from it.
_POSTGRES_ENV = {
    "VIZIER_POSTGRES_HOST": "10.0.0.5",
    "VIZIER_POSTGRES_USER": "xmc",
    "VIZIER_POSTGRES_PASSWORD": "secret",
}


def _postgres_env(**overrides: str) -> dict[str, str]:
    """Returns the baseline PostgreSQL environment with overrides applied."""
    return {**_POSTGRES_ENV, **overrides}


class LoadServerConfigTest(unittest.TestCase):
    def test_empty_environment_uses_defaults(self):
        config = server_config.load_server_config({})

        self.assertEqual(
            config,
            server_config.ServerConfig(
                port=7077, database_url="sqlite:////data/vizier.db", enable_reflection=False
            ),
        )

    def test_reads_port(self):
        self.assertEqual(server_config.load_server_config({"VIZIER_PORT": "8080"}).port, 8080)

    def test_blank_port_uses_default(self):
        self.assertEqual(server_config.load_server_config({"VIZIER_PORT": "  "}).port, 7077)

    def test_rejects_malformed_ports(self):
        for raw in ("abc", "0", "65536", "-1", "70.7"):
            with self.subTest(raw=raw), self.assertRaisesRegex(ValueError, "VIZIER_PORT"):
                server_config.load_server_config({"VIZIER_PORT": raw})

    def test_accepts_port_bounds(self):
        for raw, want in (("1", 1), ("65535", 65535)):
            with self.subTest(raw=raw):
                self.assertEqual(server_config.load_server_config({"VIZIER_PORT": raw}).port, want)

    def test_parses_reflection_flag(self):
        for raw, want in (
            ("true", True),
            ("TRUE", True),
            (" 1 ", True),
            ("yes", True),
            ("false", False),
            ("0", False),
            ("no", False),
            ("", False),
        ):
            with self.subTest(raw=raw):
                config = server_config.load_server_config({"VIZIER_ENABLE_REFLECTION": raw})
                self.assertEqual(config.enable_reflection, want)

    def test_rejects_malformed_reflection_flag(self):
        with self.assertRaisesRegex(ValueError, "VIZIER_ENABLE_REFLECTION must be true or false"):
            server_config.load_server_config({"VIZIER_ENABLE_REFLECTION": "on"})


class ResolveDatabaseUrlTest(unittest.TestCase):
    def test_explicit_url_wins_over_postgres_variables(self):
        env = _postgres_env(VIZIER_DATABASE_URL="sqlite:////tmp/v.db")

        self.assertEqual(server_config.resolve_database_url(env), "sqlite:////tmp/v.db")

    def test_blank_explicit_url_falls_back_to_default(self):
        url = server_config.resolve_database_url({"VIZIER_DATABASE_URL": " "})

        self.assertEqual(url, server_config.DEFAULT_DATABASE_URL)

    def test_postgres_host_selects_postgres(self):
        url = server_config.resolve_database_url(_POSTGRES_ENV)

        self.assertEqual(
            url,
            "postgresql+psycopg2://xmc:secret@10.0.0.5:5432/vizier"
            "?sslmode=verify-full&sslrootcert=system",
        )


class BuildPostgresUrlTest(unittest.TestCase):
    def test_honors_overrides(self):
        env = _postgres_env(
            VIZIER_POSTGRES_PORT="6432",
            VIZIER_POSTGRES_DATABASE="studies",
            VIZIER_POSTGRES_SSLMODE="disable",
        )

        self.assertEqual(
            server_config.build_postgres_url(env),
            "postgresql+psycopg2://xmc:secret@10.0.0.5:6432/studies?sslmode=disable",
        )

    def test_trusts_system_roots_only_for_verifying_modes(self):
        for sslmode, want_rootcert in (("verify-ca", True), ("require", False), ("prefer", False)):
            with self.subTest(sslmode=sslmode):
                url = server_config.build_postgres_url(
                    _postgres_env(VIZIER_POSTGRES_SSLMODE=sslmode)
                )
                self.assertEqual("sslrootcert=system" in url, want_rootcert)

    def test_percent_encodes_credentials_and_database(self):
        env = _postgres_env(
            VIZIER_POSTGRES_USER="u@x",
            VIZIER_POSTGRES_PASSWORD="p:/@?#%é",
            VIZIER_POSTGRES_DATABASE="d/b",
        )

        url = server_config.build_postgres_url(env)

        self.assertTrue(
            url.startswith("postgresql+psycopg2://u%40x:p%3A%2F%40%3F%23%25%C3%A9@10.0.0.5:")
        )
        self.assertIn("/d%2Fb?", url)

    def test_allows_empty_password(self):
        env = _postgres_env(VIZIER_POSTGRES_PASSWORD="")

        self.assertIn("://xmc:@10.0.0.5:5432/", server_config.build_postgres_url(env))

    def test_brackets_ipv6_host(self):
        url = server_config.build_postgres_url(_postgres_env(VIZIER_POSTGRES_HOST="fd00::5"))

        self.assertIn("@[fd00::5]:5432/", url)

    def test_keeps_already_bracketed_ipv6_host(self):
        url = server_config.build_postgres_url(_postgres_env(VIZIER_POSTGRES_HOST="[fd00::5]"))

        self.assertIn("@[fd00::5]:5432/", url)

    def test_brackets_ipv6_host_with_zone(self):
        url = server_config.build_postgres_url(_postgres_env(VIZIER_POSTGRES_HOST="fe80::1%eth0"))

        self.assertIn("@[fe80::1%eth0]:5432/", url)

    def test_rejects_unix_socket_path_host(self):
        env = _postgres_env(VIZIER_POSTGRES_HOST="/cloudsql/proj:region:inst")

        with self.assertRaisesRegex(ValueError, "VIZIER_POSTGRES_HOST must be a hostname"):
            server_config.build_postgres_url(env)

    def test_rejects_host_that_changes_url_parsing(self):
        for host in ("db@evil", "db/x", "db?x", "db#x", "db:5433", "db name"):
            with self.subTest(host=host):
                with self.assertRaisesRegex(ValueError, "VIZIER_POSTGRES_HOST must be a hostname"):
                    server_config.build_postgres_url(_postgres_env(VIZIER_POSTGRES_HOST=host))

    def test_requires_user(self):
        with self.assertRaisesRegex(ValueError, "VIZIER_POSTGRES_USER is required"):
            server_config.build_postgres_url(_postgres_env(VIZIER_POSTGRES_USER=" "))

    def test_requires_host(self):
        with self.assertRaisesRegex(ValueError, "VIZIER_POSTGRES_HOST is required"):
            server_config.build_postgres_url({"VIZIER_POSTGRES_USER": "xmc"})

    def test_rejects_malformed_port(self):
        with self.assertRaisesRegex(ValueError, "VIZIER_POSTGRES_PORT must be an integer"):
            server_config.build_postgres_url(_postgres_env(VIZIER_POSTGRES_PORT="pg"))


class EngineOptionsTest(unittest.TestCase):
    def test_sqlite_shares_one_connection_across_threads(self):
        options = server_config.engine_options("sqlite:////data/vizier.db")

        self.assertEqual(
            options,
            server_config.EngineOptions(
                connect_args={"check_same_thread": False}, static_pool=True, isolation_level=None
            ),
        )

    def test_postgres_autocommits_with_keepalives(self):
        for url in ("postgresql://u@h/d", "postgresql+psycopg2://u@h/d", "POSTGRESQL://u@h/d"):
            with self.subTest(url=url):
                options = server_config.engine_options(url)
                self.assertFalse(options.static_pool)
                self.assertEqual(options.isolation_level, "AUTOCOMMIT")
                self.assertNotIn("check_same_thread", options.connect_args)
                self.assertEqual(options.connect_args["keepalives"], 1)

    def test_postgres_connect_args_are_not_shared(self):
        first = server_config.engine_options("postgresql://u@h/d")
        first.connect_args["keepalives"] = 0

        self.assertEqual(
            server_config.engine_options("postgresql://u@h/d").connect_args["keepalives"], 1
        )

    def test_rejects_other_backends(self):
        for url in ("mysql://u@h/d", "postgres://u@h/d", "", "not a url"):
            with (
                self.subTest(url=url),
                self.assertRaisesRegex(ValueError, "unsupported database backend"),
            ):
                server_config.engine_options(url)

    def test_rejects_unparsable_url_without_echoing_it(self):
        for url in ("postgresql://u:secret@[::1/db", "postgresql://u:secret@[fd00::5/vizier"):
            with self.subTest(url=url):
                with self.assertRaises(ValueError) as raised:
                    server_config.engine_options(url)
                self.assertEqual(str(raised.exception), "invalid VIZIER_DATABASE_URL")
                self.assertIsNone(raised.exception.__cause__)
                self.assertTrue(raised.exception.__suppress_context__)


class DatabaseBackendTest(unittest.TestCase):
    def test_strips_driver_and_case(self):
        self.assertEqual(server_config.database_backend("POSTGRESQL+psycopg2://h/d"), "postgresql")

    def test_rejects_unparsable_url_without_echoing_it(self):
        with self.assertRaises(ValueError) as raised:
            server_config.database_backend("postgresql://u:secret@[::1/db")

        self.assertEqual(str(raised.exception), server_config.INVALID_DATABASE_URL_MESSAGE)
        self.assertTrue(raised.exception.__suppress_context__)


class DescribeDatabaseTest(unittest.TestCase):
    def test_omits_credentials_and_query(self):
        url = "postgresql+psycopg2://xmc:secret@10.0.0.5:5432/vizier?sslmode=verify-full"

        self.assertEqual(
            server_config.describe_database(url), "postgresql+psycopg2://10.0.0.5:5432/vizier"
        )

    def test_describes_sqlite_path(self):
        self.assertEqual(
            server_config.describe_database("sqlite:////data/vizier.db"),
            "sqlite:////data/vizier.db",
        )

    def test_brackets_ipv6_host(self):
        self.assertEqual(
            server_config.describe_database("postgresql://u:p@[fd00::5]/vizier"),
            "postgresql://[fd00::5]/vizier",
        )

    def test_redacts_urls_that_may_leak_password_fragments(self):
        for url, want in (
            # An unencoded "/" in the password moves the rest of it into the path.
            ("postgresql://u:p/ss@host/db", "postgresql://<redacted>"),
            # An unclosed IPv6 bracket makes urlsplit fail.
            ("postgresql://u:pw@[::1/db", "postgresql://<redacted>"),
            # A malformed port makes parts.port fail.
            ("postgresql://u:secret@h:notaport/d", "postgresql://<redacted>"),
            # An unencoded "?" or "#" in the password moves the rest into the query or fragment.
            ("postgresql://u:p?ss@host/db", "postgresql://<redacted>"),
            ("postgresql://u:p#ss@host/db", "postgresql://<redacted>"),
            # Without a well-formed scheme even the scheme is withheld.
            ("://u:p/ss@host/db", "<redacted>"),
            ("not a scheme://u:p/ss@[::1/db", "<redacted>"),
        ):
            with self.subTest(url=url):
                description = server_config.describe_database(url)
                self.assertEqual(description, want)
                self.assertNotIn("ss", description.replace("postgresql", ""))


class SerializedColumnsTest(unittest.TestCase):
    def test_identifies_serialized_columns(self):
        for name, want in (
            ("serialized_study", True),
            ("serialized_op", True),
            ("study_name", False),
            ("owner_serialized_id", False),
        ):
            with self.subTest(name=name):
                self.assertEqual(server_config.is_serialized_column(name), want)

    def test_accepts_bytea_serialized_columns(self):
        table_columns = {
            "studies": {"study_name": "VARCHAR", "serialized_study": "BYTEA"},
            "trials": {"trial_id": "INTEGER", "serialized_trial": "bytea"},
        }

        self.assertEqual(server_config.non_binary_serialized_columns(table_columns), [])

    def test_reports_non_bytea_serialized_columns(self):
        table_columns = {
            "trials": {"serialized_trial": "VARCHAR"},
            "studies": {"study_name": "VARCHAR", "serialized_study": "TEXT"},
        }

        self.assertEqual(
            server_config.non_binary_serialized_columns(table_columns),
            ["studies.serialized_study (TEXT)", "trials.serialized_trial (VARCHAR)"],
        )

    def test_empty_schema_has_no_offending_columns(self):
        self.assertEqual(server_config.non_binary_serialized_columns({}), [])


if __name__ == "__main__":
    unittest.main()
