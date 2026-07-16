"""Toy launch script for development purposes.

We will add features to this script as we progress with the implementation of
the XMC client.
"""

from collections.abc import Sequence
import logging
import os

from absl import app
import dotenv
from xmanager import xm
from xmanager import xm_cloud

# Suppress python-dotenv parsing warnings for non-key-value lines in the .env file.
logging.getLogger("dotenv.main").setLevel(logging.ERROR)
dotenv.load_dotenv()

PROJECT_ID = os.environ.get("PROJECT_ID")
if not PROJECT_ID:
    raise ValueError(
        "PROJECT_ID environment variable is not set. Please configure it in your"
        " .env file."
    )
REGISTRY_NAME = "xmc"
CONTAINER_REGISTRY = f"us-docker.pkg.dev/{PROJECT_ID}/{REGISTRY_NAME}"


def main(argv: Sequence[str]) -> None:
    """Main entrypoint for launch script."""
    if len(argv) > 1:
        raise app.UsageError("Too many command-line arguments.")

    # Create an experiment.
    with xm_cloud.create_experiment(
        experiment_title=(
            f"My XMC experiment - {os.environ.get('USER', 'unknown')}"
        ),
        acls=xm_cloud.Acls(
            owners=[xm_cloud.Acls.Role(all_authenticated_users=True)]
        ),
        urls=[
            xm_cloud.Url(
                url="https://www.google.com",
                icon="link",
                display_name="Google homepage",
            )
        ],
    ) as experiment:
        executables = experiment.package([
            xm.container(
                image_path=(
                    f"us-docker.pkg.dev/{PROJECT_ID}/xmc/example-container:latest"
                ),
                executor_spec=xm_cloud.KubernetesJobExecutorSpec(
                    container_registry=CONTAINER_REGISTRY,
                ),
            )
        ])

        for executable in executables:
            experiment.add(
                xm.Job(
                    name="client",
                    executable=executable,
                    executor=xm_cloud.KubernetesJobExecutor(
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
    print(f"CONTAINER_REGISTRY: {CONTAINER_REGISTRY}")
    app.run(main)