#!/usr/bin/env python3
# Copyright 2026 Google LLC
# Helper script to determine changed packages, build/push OCI images, and restart GKE deployments.

import sys
import os
import subprocess
import shutil
import json

# Mapping of file path prefixes to Bazel targets and GKE deployments
MAPPING = [
    {
        "prefix": "cmd/ui/",
        "push_target": "//cmd/ui:ui_image_push",
        "deployment": "ui-deployment"
    },
    {
        "prefix": "cmd/xid_service/",
        "push_target": "//cmd/xid_service:xid_service_image_push",
        "deployment": "xid-service-deployment"
    },
    {
        "prefix": "cmd/actuator/",
        "push_target": "//cmd/actuator:actuator_image_push",
        "deployment": "actuator-deployment"
    },
    {
        "prefix": "cmd/reconciler/",
        "push_target": "//cmd/reconciler:reconciler_image_push",
        "deployment": "reconciler-deployment"
    },
    {
        "prefix": "cmd/experiment_state_server/",
        "push_target": "//cmd/experiment_state_server:experiment_state_server_image_push",
        "deployment": "experiment-state-server-deployment"
    },
    {
        "prefix": "cmd/log_monitor/",
        "push_target": "//cmd/log_monitor:log_monitor_image_push",
        "deployment": "log-monitor-deployment"
    },
    # Dependency mappings
    {
        "prefix": "pkg/auth/",
        "push_target": "//cmd/xid_service:xid_service_image_push",
        "deployment": "xid-service-deployment"
    },
    {
        "prefix": "internal/storage/spanner/",
        "push_target": "//cmd/xid_service:xid_service_image_push",
        "deployment": "xid-service-deployment"
    }
]

def get_changed_files(base_branch="main"):
    try:
        res = subprocess.run(
            ["git", "diff", "--name-only", f"origin/{base_branch}...HEAD"],
            capture_output=True,
            text=True,
            check=True
        )
        return [line.strip() for line in res.stdout.splitlines() if line.strip()]
    except subprocess.CalledProcessError as e:
        sys.stderr.write(f"Error getting git diff: {e}\n")
        sys.exit(1)

def main():
    base_branch = sys.argv[1] if len(sys.argv) > 1 else "main"
    changed_files = get_changed_files(base_branch)
    print(f"Changed files in PR: {changed_files}")
    
    helm_changed = any(f.startswith("deploy/helm/") for f in changed_files)
    
    # Identify unique targets to build/deploy
    targets_to_build = set()
    deployments_to_restart = set()
    
    for f in changed_files:
        for entry in MAPPING:
            if f.startswith(entry["prefix"]):
                targets_to_build.add(entry["push_target"])
                deployments_to_restart.add(entry["deployment"])
                
    acr_target = os.environ.get("XMC_ACR_TARGET")
    namespace = os.environ.get("K8S_NAMESPACE", "xmc")
    
    if not acr_target:
        sys.stderr.write("XMC_ACR_TARGET environment variable is not set.\n")
        sys.exit(1)
        
    os.environ["XMC_ENV"] = "dev"
    
    # Build and Push OCI containers
    for target in targets_to_build:
        print(f"Building and pushing image target: {target}...")
        try:
            subprocess.run(["bazel", "run", target], check=True)
        except subprocess.CalledProcessError as e:
            sys.stderr.write(f"Error building target {target}: {e}\n")
            sys.exit(1)
            
    # Restart deployments if helm is NOT changed (if helm is changed, we upgrade the helm release)
    if helm_changed:
        print("Helm charts or values changed. A Helm upgrade is required.")
        # Return special exit code or output to trigger helm upgrade in the workflow
        print("RESULT:HELM_UPGRADE_REQUIRED")
    else:
        for deployment in deployments_to_restart:
            print(f"Restarting GKE deployment: {deployment} in namespace {namespace}...")
            try:
                subprocess.run(
                    ["kubectl", "rollout", "restart", f"deployment/{deployment}", "-n", namespace],
                    check=True
                )
                subprocess.run(
                    ["kubectl", "rollout", "status", f"deployment/{deployment}", "-n", namespace],
                    check=True
                )
            except subprocess.CalledProcessError as e:
                sys.stderr.write(f"Error restarting GKE deployment {deployment}: {e}\n")
                sys.exit(1)
                
    print("Build and deploy steps completed successfully.")

if __name__ == '__main__':
    main()
