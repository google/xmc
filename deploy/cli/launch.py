"""Toy launch script for development purposes.

We will add features to this script as we progress with the implementation of
the XMC client.

Usage:
  
"""

from collections.abc import Sequence
import logging
import os

import dotenv

# Suppress python-dotenv parsing warnings for non-key-value lines (like echo) in the .env file.
logging.getLogger("dotenv.main").setLevel(logging.ERROR)
dotenv.load_dotenv()

from absl import app
from xmanager import xm
from xmanager import xm_cloud

PROJECT_ID = os.environ.get("PROJECT_ID")
REGISTRY_NAME = "xmc"
CONTAINER_REGISTRY = f"us-docker.pkg.dev/{PROJECT_ID}/{REGISTRY_NAME}"


def main(argv: Sequence[str]) -> None:
  if len(argv) > 1:
    raise app.UsageError("Too many command-line arguments.")

  # Create an experiment.
  with xm_cloud.create_experiment(
      experiment_title="My XMC experiment - " + os.environ.get("USER"),
      # Specify ACLs to allow specific users (in this example, all authenticated
      # users) to read the experiment.
      acls=xm_cloud.Acls(
          owners=[xm_cloud.Acls.Role(all_authenticated_users=True)]
      ),
      # URLs displayed on the experiment page in the XManager UI.
      urls=[
          xm_cloud.Url(
              url="https://www.google.com",
              icon="link",
              display_name="Google homepage",
          )
      ],
  ) as experiment:
    # Package your executables (in this example, a single Docker image).
    executables = experiment.package([
        xm.container(
            image_path=f"us-docker.pkg.dev/{PROJECT_ID}/xmc/example-container:latest",
            executor_spec=xm_cloud.KubernetesJobExecutorSpec(
                container_registry=CONTAINER_REGISTRY,
            ),
        )
    ])

    # For each executable, add a job to the experiment.
    for executable in executables:
      # Each call to experiment.add creates a new work unit.
      # You can either add a single Job or a JobGroup to a work unit.
      experiment.add(
          xm.Job(
              name="client",
              executable=executable,
              executor=xm_cloud.KubernetesJobExecutor(
                  # Specify the resource requirements for this job.
                  requirements=xm.JobRequirements(
                      resources={
                          xm.ResourceType.CPU: 0.1,
                          xm.ResourceType.MEMORY: 100 * xm.MiB,
                      },
                      replicas=3,
                  ),
                  replica_retry_limit=3,
              ),
          ),
          # Specify the settings for this work unit (URLs to show in the UI,
          # notes, tags, scheduling constraints, etc.)
          settings=xm_cloud.WorkUnitSettings(
              scheduling_constraints=xm_cloud.SchedulingConstraints(
                  queue_name="cpu-queue"
              ),
              tags=["test", "test2"],
              notes="This is a test note.",
              urls=[
                  xm_cloud.Url(
                      url="https://www.google.com",
                      icon="link",
                      display_name="Google",
                  )
              ],
          ),
      )
      print(experiment.work_units)


if __name__ == "__main__":
  print(f"XMC_CLIENT_SA: {os.environ.get('XMC_CLIENT_SA')}")
  print(f"IAP_CLIENT_ID: {os.environ.get('IAP_CLIENT_ID')}")
  print(f"PROJECT_ID: {os.environ.get('PROJECT_ID')}")
  print(f"CONTAINER_REGISTRY: {os.environ.get('CONTAINER_REGISTRY')}")
  app.run(main)