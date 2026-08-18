# CI Infrastructure & In-Cluster Bazel Cache

This directory contains Kubernetes deployment manifests for CI/CD support infrastructure running on the self-hosted Actions Runner Controller (ARC) GKE cluster.

## Manifests

- [`bazel-remote-cache.yaml`](bazel-remote-cache.yaml): Deploys an in-cluster HTTP/gRPC Bazel Remote Cache (`buchgr/bazel-remote-cache`) backed by a 100Gi SSD Persistent Volume (`premium-rwo`) in the `arc-runners` namespace.

## Complete Documentation & Migration Guide

For architecture diagrams, setup instructions, performance metrics, and a step-by-step migration guide on how to revert from self-hosted runners to GitHub-hosted runners (`ubuntu-latest`), see:
- [`docs/ci_runner_infrastructure.md`](../../docs/ci_runner_infrastructure.md)
