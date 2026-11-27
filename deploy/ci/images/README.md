# XMC GHCR CI Runner Images

This directory defines the layered `linux/amd64` Docker images published to GitHub Packages (`ghcr.io/google/xmc/ci-*`) for accelerating XMC GitHub Actions workflows on GitHub-hosted `ubuntu-latest` runners.

## Image Topology

- [`Dockerfile.base`](Dockerfile.base) (`ci-base`): Pinned `ubuntu:24.04` digest, core CLI utilities, standalone Python 3.13 (`/opt/python`), and Node.js 22 (`/opt/node`).
- [`Dockerfile.lint`](Dockerfile.lint) (`ci-lint`): Extends `ci-base` with Go, Ruff, Mypy, Go-compiled `golangci-lint`, `actionlint`, `buildifier`, `addlicense`, `shellcheck`, Terraform, TFLint (+ pre-cached Google plugin), Helm v3, and Trivy.

## Single Source of Truth

All base image digests, tool versions, and SHA-256 checksums live in [`versions.json`](versions.json). [`render_bake_vars.py`](render_bake_vars.py) feeds those values into [`docker-bake.hcl`](docker-bake.hcl) with no duplicated version pins.

## Local Build & Verification

Build and smoke-test any target locally:

```bash
python3 deploy/ci/images/render_bake_vars.py --output /tmp/bake-vars.json
docker buildx bake -f deploy/ci/images/docker-bake.hcl -f /tmp/bake-vars.json --load ci-base
docker run --rm --shm-size=2g \
  -v "${PWD}/deploy/ci/images:/smoke:ro" \
  ghcr.io/google/xmc/ci-base:local \
  python3 /smoke/verify_image.py --role base --versions /smoke/versions.json
```

## CI Verification

[`.github/workflows/ci-images.yml`](../../../.github/workflows/ci-images.yml) builds and smoke-tests images on PRs (`push: false`).
