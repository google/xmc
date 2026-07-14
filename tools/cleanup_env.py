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

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
from typing import Dict, List

from tool_utils import HandledError, run_tool


def run_command(
    cmd: List[str], dry_run: bool = False
) -> subprocess.CompletedProcess:
  """Runs a system command and returns the result."""
  if dry_run:
    print(f"[DRY RUN] Would run: {' '.join(cmd)}")
    return subprocess.CompletedProcess(
        args=cmd, returncode=0, stdout="", stderr=""
    )
  return subprocess.run(cmd, capture_output=True, text=True)


def parse_tfvars(tfvars_path: str) -> Dict[str, str]:
  """Parses a tfvars file and returns a key-value dictionary."""
  config = {}
  if not os.path.exists(tfvars_path):
    return config

  with open(tfvars_path, 'r') as f:
    for line in f:
      line = line.strip()
      if not line or line.startswith('#'):
        continue
      # Match key = "value" or key = value
      match = re.match(r'^(\w+)\s*=\s*(.*)$', line)
      if match:
        key = match.group(1)
        val = match.group(2).strip('"\'- ')
        config[key] = val
  return config


def _get_deployment_dir(deployment_name: str) -> str:
  """Resolves and validates the deployment directory."""
  ctk_out_dir = os.path.abspath(
      os.path.join(os.getcwd(), 'ctk-out', deployment_name)
  )
  if not os.path.exists(ctk_out_dir):
    raise HandledError(
        f"Deployment directory not found: {ctk_out_dir}",
        "No active cloud environment was deployed from this folder. Nothing to"
        " clean up.",
    )
  return ctk_out_dir


def _load_config(ctk_out_dir: str) -> Dict[str, str]:
  """Loads and validates the configuration from terraform.tfvars."""
  tfvars_path = os.path.join(ctk_out_dir, 'primary', 'terraform.tfvars')
  if not os.path.exists(tfvars_path):
    raise HandledError(
        f"terraform.tfvars not found at {tfvars_path}",
        "Cannot identify project configuration. Please verify deployment"
        " directory.",
    )

  config = parse_tfvars(tfvars_path)
  project_id = config.get('project_id')
  if not project_id:
    raise HandledError(
        "project_id not found in terraform.tfvars",
        "Cannot proceed without project_id. Check your configuration.",
    )
  return config


def _check_deletion_protection(config: Dict[str, str], force: bool):
  """Checks if deletion protection is enabled and raises error if not forced."""
  deletion_protection = config.get('deletion_protection') == 'true'
  if deletion_protection and not force:
    raise HandledError(
        "Deletion protection is enabled in terraform.tfvars.",
        "Disable deletion protection in your configuration, run gcluster"
        " compile/deploy, or use --force if you intend to bypass this check"
        " (note: destroy will likely fail if resources are protected).",
    )


def _delete_spanner_backups(
    environment_name: str, project_id: str, dry_run: bool
):
  """Lists and deletes Spanner backups for the environment."""
  spanner_instance = f"xmc-{environment_name}"
  print(f"Checking for Spanner backups in instance {spanner_instance}...")
  list_backups_cmd = [
      "gcloud",
      "spanner",
      "backups",
      "list",
      f"--instance={spanner_instance}",
      f"--project={project_id}",
      "--format=json",
  ]

  if dry_run:
    print(
        f"[DRY RUN] Would list backups for instance {spanner_instance} in"
        f" project {project_id}"
    )
    backups = []
  else:
    res = run_command(list_backups_cmd)
    if res.returncode != 0:
      if "NOT_FOUND" in res.stderr or "Instance not found" in res.stderr:
        print(
            f"Spanner instance {spanner_instance} not found. Skipping backup"
            " deletion."
        )
        backups = []
      else:
        print(
            f"Warning: Failed to list backups (might not exist): {res.stderr}",
            file=sys.stderr,
        )
        backups = []
    else:
      try:
        backups = json.loads(res.stdout)
      except Exception as e:
        print(
            f"Warning: Failed to parse backup list JSON: {e}", file=sys.stderr
        )
        backups = []

  if backups:
    print(f"Found {len(backups)} backup(s). Deleting...")
    for backup in backups:
      backup_name = backup.get('name')
      backup_id = backup_name.split('/')[-1]
      print(f"Deleting backup {backup_id}...")
      delete_backup_cmd = [
          "gcloud",
          "spanner",
          "backups",
          "delete",
          backup_id,
          f"--instance={spanner_instance}",
          f"--project={project_id}",
          "--quiet",
      ]
      res = run_command(delete_backup_cmd, dry_run=dry_run)
      if res.returncode != 0:
        raise HandledError(
            f"Failed to delete backup {backup_id}: {res.stderr}",
            f"Manual intervention required: delete backup {backup_id} in"
            f" spanner instance {spanner_instance}",
        )
  else:
    print("No Spanner backups found.")


def _run_gcluster_destroy(
    ctk_out_dir: str,
    deployment_name: str,
    environment_name: str,
    project_id: str,
    config: Dict[str, str],
    args,
):
  """Runs gcluster destroy and handles failures."""
  gcluster_path = os.path.join(os.getcwd(), 'cluster-toolkit', 'gcluster')
  if not os.path.exists(gcluster_path) and not args.dry_run:
    raise HandledError(
        f"gcluster executable not found at {gcluster_path}",
        "Please ensure you are running this from the repository root and"
        " cluster-toolkit is initialized.",
    )

  destroy_cmd = [gcluster_path, "destroy", ctk_out_dir, "--auto-approve"]

  if args.skip_xmc_app:
    destroy_cmd.extend(["--skip", "xmc_app"])
  elif args.skip:
    destroy_cmd.extend(["--skip", args.skip])

  print(f"Running gcluster destroy for {deployment_name}...")
  res = run_command(destroy_cmd, dry_run=args.dry_run)

  if res.returncode != 0:
    if "xmc_app" in res.stderr and not args.skip_xmc_app:
      raise HandledError(
          f"gcluster destroy failed on xmc_app: {res.stderr}",
          "The destroy got stuck on xmc_app. Retry with --skip-xmc-app.",
      )

    cluster_name = f"xmc-{deployment_name}-{environment_name}"
    region = config.get('region') or config.get('zone')
    location_str = f"--location={region}" if region else ""

    raise HandledError(
        f"gcluster destroy failed: {res.stderr}",
        f"If GKE is stuck, try force deleting the GKE cluster:\n"
        f"  gcloud container clusters delete {cluster_name} {location_str}"
        f" --project={project_id} --quiet\n"
        f"And then rerun this script with: --skip xmc_app,kubernetes_prereqs",
    )

  print("gcluster destroy completed successfully.")


def _delete_dns_records(
    environment_name: str, dns_zone: str, dns_project: str, dry_run: bool
):
  """Lists and deletes DNS records matching the environment."""
  if not dns_zone:
    print(
        "DNS zone not specified. Skipping DNS cleanup. (Use --dns-zone to"
        " enable)"
    )
    return

  print(f"Cleaning up DNS records in zone {dns_zone}...")
  list_dns_cmd = [
      "gcloud",
      "dns",
      "record-sets",
      "list",
      f"--zone={dns_zone}",
      f"--project={dns_project}",
      "--format=json",
  ]

  if dry_run:
    print(
        f"[DRY RUN] Would list DNS records for zone {dns_zone} in project"
        f" {dns_project}"
    )
    records = []
  else:
    res = run_command(list_dns_cmd)
    if res.returncode != 0:
      print(
          f"Warning: Failed to list DNS records: {res.stderr}",
          file=sys.stderr,
      )
      records = []
    else:
      try:
        records = json.loads(res.stdout)
      except Exception as e:
        print(f"Warning: Failed to parse DNS records: {e}", file=sys.stderr)
        records = []

  records_to_delete = []
  for record in records:
    name = record.get('name')
    type = record.get('type')
    if type == 'A' and (
        name.startswith(f"{environment_name}.")
        or name.startswith(f"api.{environment_name}.")
        or name.startswith(f"grpc.{environment_name}.")
    ):
      records_to_delete.append(record)

  if records_to_delete:
    print(f"Found {len(records_to_delete)} DNS record(s) to delete.")
    for record in records_to_delete:
      name = record.get('name')
      type = record.get('type')
      print(f"Deleting DNS record {name} ({type})...")
      delete_dns_cmd = [
          "gcloud",
          "dns",
          "record-sets",
          "delete",
          name,
          f"--type={type}",
          f"--zone={dns_zone}",
          f"--project={dns_project}",
          "--quiet",
      ]
      res = run_command(delete_dns_cmd, dry_run=dry_run)
      if res.returncode != 0:
        print(
            f"Warning: Failed to delete DNS record {name}: {res.stderr}",
            file=sys.stderr,
        )
  else:
    print("No matching DNS records found to delete.")


def _cleanup_kubeconfig(environment_name: str, dry_run: bool):
  """Deletes kubeconfig contexts matching the environment."""
  print("Cleaning up kubeconfig contexts...")
  list_contexts_cmd = ["kubectl", "config", "get-contexts", "-o", "name"]
  if dry_run:
    print("[DRY RUN] Would list kubeconfig contexts")
    contexts = []
  else:
    res = run_command(list_contexts_cmd)
    if res.returncode == 0:
      contexts = res.stdout.splitlines()
    else:
      print(
          f"Warning: Failed to list kubeconfig contexts: {res.stderr}",
          file=sys.stderr,
      )
      contexts = []

  pattern = re.compile(f"xmc-.*{environment_name}")
  cleaned_contexts = 0
  for context in contexts:
    if pattern.search(context):
      print(f"Deleting kubeconfig context and cluster: {context}")
      delete_context_cmd = ["kubectl", "config", "delete-context", context]
      delete_cluster_cmd = ["kubectl", "config", "delete-cluster", context]
      run_command(delete_context_cmd, dry_run=dry_run)
      run_command(delete_cluster_cmd, dry_run=dry_run)
      cleaned_contexts += 1

  if cleaned_contexts == 0:
    print("No matching kubeconfig contexts found.")


def _delete_deployment_dir(ctk_out_dir: str, dry_run: bool):
  """Removes the local deployment directory."""
  print(f"Removing deployment directory: {ctk_out_dir}...")
  if dry_run:
    print(f"[DRY RUN] Would remove directory: {ctk_out_dir}")
  else:
    try:
      shutil.rmtree(ctk_out_dir)
      print(f"Removed deployment directory: {ctk_out_dir}")
    except Exception as e:
      raise HandledError(
          f"Failed to remove deployment directory {ctk_out_dir}: {e}",
          "Please manually delete the directory.",
      )


def cleanup_env(args):
  """Main cleanup logic."""
  deployment_name = args.env
  environment_name = args.environment_name or deployment_name

  ctk_out_dir = _get_deployment_dir(deployment_name)
  config = _load_config(ctk_out_dir)
  project_id = config.get('project_id')

  _check_deletion_protection(config, args.force)

  _delete_spanner_backups(environment_name, project_id, args.dry_run)

  _run_gcluster_destroy(
      ctk_out_dir,
      deployment_name,
      environment_name,
      project_id,
      config,
      args,
  )

  dns_project = args.dns_project or project_id
  _delete_dns_records(
      environment_name, args.dns_zone, dns_project, args.dry_run
  )

  _cleanup_kubeconfig(environment_name, args.dry_run)

  _delete_deployment_dir(ctk_out_dir, args.dry_run)

  return f"Environment {deployment_name} cleaned up successfully."


def main():
  parser = argparse.ArgumentParser(
      description="Safely clean up an XMC environment."
  )
  parser.add_argument(
      "--env", required=True, help="Deployment name (e.g., my-dev)"
  )
  parser.add_argument(
      "--environment-name",
      help="Environment name if different from deployment name",
  )
  parser.add_argument("--dns-zone", help="Cloud DNS managed zone name")
  parser.add_argument(
      "--dns-project", help="GCP project hosting the DNS zone (defaults to env project)"
  )
  parser.add_argument(
      "--skip-xmc-app",
      action="store_true",
      help="Skip xmc_app group during destroy (mitigates helm timeout)",
  )
  parser.add_argument(
      "--skip",
      help="Comma-separated list of groups to skip during destroy",
  )
  parser.add_argument(
      "--dry-run",
      action="store_true",
      help="Show commands that would be executed without running them",
  )
  parser.add_argument(
      "--force",
      action="store_true",
      help="Bypass deletion protection check",
  )

  run_tool(parser, cleanup_env)


if __name__ == "__main__":
  main()
