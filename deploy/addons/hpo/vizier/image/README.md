# Vizier Server Image (`deploy/addons/hpo/vizier/image`)

A container image that runs the [Open Source Vizier](https://github.com/google/vizier) gRPC
service as the backend of the EXPERIMENTAL `vizier` 3P component. Upstream publishes neither an
image nor a chart, so XMC builds this one.

The image does **not** contain a copy of Vizier. It installs the official `google-vizier` release
from PyPI and adds a small launcher that wires upstream's own classes (`VizierServicer`,
`SQLDataStore`, the Pythia policy factory) into a gRPC server.

## What's What

| File | Maintained by | Purpose |
| :--- | :--- | :--- |
| [`Dockerfile`](Dockerfile) | Hand-written | `python:3.11-slim` base pinned by digest. Installs the locked dependencies with `--require-hashes`, runs as non-root UID 10001, and listens on port `7077`. |
| [`requirements.in`](requirements.in) | Hand-written | The **direct** dependencies, each pinned to an exact version. This is the file you edit. |
| [`requirements.txt`](requirements.txt) | **Generated, do not edit** | Lockfile compiled from `requirements.in` by `pip-compile --generate-hashes`. It pins every transitive dependency with its SHA-256 hashes, so builds are reproducible and a tampered package fails the install. Like `go.sum` and `MODULE.bazel.lock`, it is committed on purpose. |
| [`vizier_server.py`](vizier_server.py) | Hand-written | The launcher. It builds the database engine, the datastore, the servicer and the gRPC server, then serves until SIGTERM. |
| [`server_config.py`](server_config.py) | Hand-written | Stdlib-only parsing of the environment variables below, database URL construction, and a log-safe (credential-free) database description. |
| [`supported_algorithms.py`](supported_algorithms.py) | Hand-written | The algorithms this image accepts. |
| `*_test.py`, [`BUILD.bazel`](BUILD.bazel) | Hand-written | Bazel unit tests for the dependency-free modules. `vizier_server.py` needs `google-vizier`, which is installed only in the image, so it is tested by running the image. |

## Why a Launcher Instead of Upstream's `DefaultVizierServer`

Upstream's `DefaultVizierServer` can only use SQLite:

- It always creates the SQLAlchemy engine with SQLite-only arguments (`check_same_thread`,
  `StaticPool`), which the PostgreSQL driver rejects.
- Its schema stores serialized protos in `VARCHAR` columns, which PostgreSQL cannot hold.

The launcher repeats its short wiring with an engine suited to the configured backend. On
PostgreSQL it creates the `serialized_*` columns as `BYTEA`, runs in `AUTOCOMMIT` (upstream holds
one long-lived connection), and refuses to start on an existing schema that still has `VARCHAR`
proto columns.

It also adds behaviour upstream doesn't have:

- **Algorithm guard**: `CreateStudy` accepts only `RANDOM_SEARCH`, `QUASI_RANDOM_SEARCH` and
  `GRID_SEARCH`, and returns `INVALID_ARGUMENT` otherwise (including unset and `DEFAULT`). The GP
  algorithms need `tensorflow-probability` and `jaxopt`, which this image does not install yet.
- **Optional gRPC reflection**, off by default.
- **Version guard**: the launcher uses non-public upstream modules, so it refuses to start unless
  the installed `google-vizier` is exactly the pinned release.
- **Graceful shutdown** on SIGTERM/SIGINT.

## Configuration

| Variable | Default | Meaning |
| :--- | :--- | :--- |
| `VIZIER_PORT` | `7077` | gRPC port. |
| `VIZIER_ENABLE_REFLECTION` | `false` | Serve gRPC server reflection (`true`/`false`). |
| `VIZIER_DATABASE_URL` | `sqlite:////data/vizier.db` | SQLAlchemy URL. Takes precedence over the `VIZIER_POSTGRES_*` variables. |
| `VIZIER_POSTGRES_HOST` | unset | When set (and `VIZIER_DATABASE_URL` is not), connect to PostgreSQL. Must be a hostname or IP address. |
| `VIZIER_POSTGRES_USER` | required with `HOST` | Database user. |
| `VIZIER_POSTGRES_PASSWORD` | empty | Password; percent-encoded by the launcher, so any characters are allowed. |
| `VIZIER_POSTGRES_PORT` | `5432` | Database port. |
| `VIZIER_POSTGRES_DATABASE` | `vizier` | Database name; it must already exist. |
| `VIZIER_POSTGRES_SSLMODE` | `verify-full` | libpq `sslmode`. `verify-ca`/`verify-full` trust the image's system CA bundle. |

## Build and Run

```bash
docker build -t xmc-vizier:0.1.24 deploy/addons/hpo/vizier/image

# SQLite in a local volume, with reflection for grpcurl:
docker run --rm -p 7077:7077 -v vizier-data:/data \
  -e VIZIER_ENABLE_REFLECTION=true xmc-vizier:0.1.24
grpcurl -plaintext localhost:7077 list
```

The tag follows the pinned `google-vizier` version.

## Updating Dependencies

1. Edit [`requirements.in`](requirements.in). When bumping `google-vizier`, also update
   `_SUPPORTED_VIZIER_VERSION` in [`vizier_server.py`](vizier_server.py) and re-check the launcher
   against the new upstream internals.
2. Regenerate the lockfile from the repository root:

   ```bash
   docker run --rm --user "$(id -u):$(id -g)" -e HOME=/tmp \
     -e CUSTOM_COMPILE_COMMAND="the command in requirements.in" \
     -v "${PWD}/deploy/addons/hpo/vizier/image:/src" -w /src python:3.11.18-slim \
     sh -c 'pip install --user pip-tools==7.4.1 && python -m piptools compile \
     --generate-hashes --allow-unsafe --strip-extras --output-file=requirements.txt requirements.in'
   ```

3. Rebuild the image, then smoke-test it against SQLite and PostgreSQL: create a study, suggest
   and complete trials, restart the server, and read the study back.
