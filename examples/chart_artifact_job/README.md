# Chart Artifact Example Job

This example launches an experiment whose worker streams randomized training metrics into
ClickHouse, then attaches a **Chart artifact** to the WorkUnit and provisions a
**Dashboard** that plots those metrics.

It follows the same ingestion path as [`metrics_job`](../metrics_job): the worker calls
`xmc.log_metric()`, which posts to the Ingestion Gateway; the gateway publishes to
Redpanda, and a ClickHouse materialized view lands the rows in `default.xmc_metrics`.

> [!NOTE]
> The WorkUnit **Artifacts** tab renders a Chart artifact as its metric list plus the
> chart resource the server provisioned for it — it does not draw plots inline, and it
> does not render that artifact's URL as a link. Plots are drawn on the dashboard page.
> So the launcher also attaches a small **URL artifact** next to the chart artifact,
> which the tab does render as a clickable link straight to the dashboard.

## Contents

1. **[training_metrics.py](./training_metrics.py)**:
   - Metric names and the randomized sampler, shared by the worker and the launcher so
     the logged metric names and the plotted metric names cannot drift apart.

2. **[worker.py](./worker.py)**:
   - Training worker that logs `chart_demo/loss`, `chart_demo/accuracy` and
     `chart_demo/learning_rate` once per step via `xmc.log_metric()`.

3. **[dashboard_rest_client.py](./dashboard_rest_client.py)**:
   - Minimal HTTP client for the Dashboard Service's gRPC-transcoded REST endpoints. The
     Dashboard Service gRPC port is cluster-internal, so an out-of-cluster launcher
     reaches it over HTTPS through the same gateway that fronts the UI. It uses
     `CreateDashboard`'s inline `charts` field, which writes the dashboard and its charts
     in one transaction.

4. **[iap_identity_token.py](./iap_identity_token.py)**:
   - Mints the OIDC identity token used as the `Authorization: Bearer` credential, by
     impersonating `XMC_CLIENT_SA` for the `IAP_CLIENT_ID` audience.

5. **[Dockerfile](./Dockerfile)** / **[build_and_push.sh](./build_and_push.sh)**:
   - Package the worker and `sdks/python/xmc` into a Python 3.11 image and push it to
     `us-docker.pkg.dev/<PROJECT_ID>/xmc/chart-artifact-job:latest`. The build context is
     the repository root, so the SDK sources are visible to `COPY`.

6. **[launch.py](./launch.py)**:
   - Creates the experiment, the WorkUnit, the Dashboard with its Chart, and the two
     artifacts.

---

## Prerequisites

1. A deployment with `dashboardServiceDeployment.enabled=true`. It both serves the
   endpoints this example calls and gates the UI's `/dashboards` route, which is where
   the plots are drawn.
2. A `.env` file in the repository root, as described in the CLI installation
   instructions. This example reads `PROJECT_ID`, `IAP_CLIENT_ID`, `XMC_CLIENT_SA` and
   `XMANAGER_UI_URL` (or `XMC_DOMAIN`).
3. The CLI virtual environment:
   ```bash
   source ../../deploy/cli/venv/bin/activate
   ```
4. The worker image:
   ```bash
   ./build_and_push.sh
   ```

## Running the Example

```bash
python3 launch.py
```

The launcher will:

1. Create an experiment and a single-job WorkUnit running `worker.py`. It passes
   `XMC_XID` and `XMC_GATEWAY_URL` to the job; the actuator injects `XMC_WORK_UNIT_ID`
   and `XMC_EXECUTION_ID`.
2. Create a `Dashboard` and, in the same transaction, the `Chart` it renders: summary
   boxes for the latest loss and accuracy, plus scatter plots of all three metrics
   against training steps.
3. Create a `ChartArtifact` on the WorkUnit listing the three metric names.
4. Create a `UrlArtifact` on the same WorkUnit linking to `/dashboards/<dashboard_id>`.

It prints both the WorkUnit and Dashboard URLs at the end.

### Configuration

| Variable                 | Default                                                        | Effect                                     |
| ------------------------ | -------------------------------------------------------------- | ------------------------------------------ |
| `IMAGE_PATH`             | `us-docker.pkg.dev/<PROJECT_ID>/xmc/chart-artifact-job:latest`  | Worker image to run.                       |
| `QUEUE_NAME`             | `cpu-queue`                                                     | Kueue queue the WorkUnit is submitted to.  |
| `CHART_DEMO_TOTAL_STEPS` | `60`                                                            | Steps the worker simulates (worker-side).  |
| `CHART_DEMO_SEED`        | random                                                          | Sampler seed, to reproduce a run.          |

## Verification

Confirm the metrics landed in ClickHouse (substitute the printed `xid`):

```bash
kubectl exec -n xmc deployment/clickhouse -- clickhouse-client --query \
  "SELECT metric_name, count() FROM default.xmc_metrics WHERE xid = <xid> GROUP BY metric_name"
```

You should see one row per `chart_demo/*` metric. The dashboard plots populate as soon as
those rows exist; the artifact's `status` flips from `PROVISIONING` once the server has
provisioned its own chart resource for the artifact.

## Notes on the artifact payload

`CreateArtifact` rejects a `ChartArtifact` that sets `chart`, `status` or `error_message`
— they are `OUTPUT_ONLY`. The client supplies only `title` and `metric_names`; the server
marks the artifact `PROVISIONING` and fills the rest asynchronously.

The dashboard URL is repeated in three places because each is read by something
different: `Artifact.url` for API consumers, the artifact's notes for anyone reading the
card, and the companion `UrlArtifact` payload, which is the only one the UI turns into a
clickable link.
