# XMC GHCR CI Runner Images

This directory defines the layered `linux/amd64` Docker images published to GitHub Packages (`ghcr.io/google/xmc/ci-*`) for accelerating XMC GitHub Actions workflows on GitHub-hosted `ubuntu-latest` runners.

## Image Topology

- [`Dockerfile.base`](Dockerfile.base) (`ci-base`): Pinned `ubuntu:24.04` digest, core CLI utilities, standalone Python 3.13 (`/opt/python`), and Node.js 22 (`/opt/node`).

## Single Source of Truth

All base image digests, tool versions, and SHA-256 checksums live in [`versions.json`](versions.json). [`render_bake_vars.py`](render_bake_vars.py) feeds those values into [`docker-bake.hcl`](docker-bake.hcl) with no duplicated version pins.

## Local Build

Build the base image locally:

```bash
python3 deploy/ci/images/render_bake_vars.py --output /tmp/bake-vars.json
docker buildx bake -f deploy/ci/images/docker-bake.hcl -f /tmp/bake-vars.json --load ci-base
```
