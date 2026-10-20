# XManager on Cloud (XMC) - Helm Chart (`deploy/helm`)

The `xmanager-cloud` Helm chart packages and deploys the core microservices of the XManager on Cloud (XMC) platform onto Kubernetes clusters (such as Google Kubernetes Engine or local KinD).

---

## Architecture Overview

The chart orchestrates the following backend and frontend microservices:

| Microservice | Role & Responsibility | Primary Dependencies |
| :--- | :--- | :--- |
| **`ess`** | Experiment State Server exposing gRPC and HTTP APIs for experiment/work unit lifecycle management. | Cloud Spanner (`experiment-db`), Redis |
| **`actuator`** | Workload creation engine translating pending work units into Kubernetes `JobSet` resources. | Cloud Spanner, Kubernetes API (In-Cluster or Remote), Redis |
| **`reconciler`** | Workload status watcher observing Kubernetes `JobSet` pod events and synchronizing state back to the database. | Cloud Spanner, Kubernetes API (In-Cluster or Remote) |
| **`xid_service`** | Unique distributed identifier generator for experiments and work units. | Cloud Spanner (`xids-db`) |
| **`ui`** | Angular-based web management console. | ESS, Dex / OAuth2 Gateway |
| **`health_service`** | Platform health diagnostics, runtime configuration inspection, and global workload scheduling pause/resume controls. | Cloud Spanner (`experiment-db`, `xids-db`), Redis, ESS, XID, Cloud Run, Cloud Identity |
| **`log_monitor`** | Pub/Sub subscriber turning filtered workload log entries into work unit status messages. Needs the subscription from `deploy/ctk_v2/modules/log_monitor`. | Pub/Sub, Cloud Spanner (`experiment-db`) |

---

## Configuration Reference (`values.yaml`)

### 1. Global & Environment Settings

```yaml
global:
  environment: "dev"       # Deployment environment tag ("dev", "staging", "prod")
  projectId: ""            # Google Cloud Project ID
  region: "us-central1"    # Primary GCP region

isDevEnvironment: false    # Set to true for local development / KinD clusters

featureFlags:
  pause_scheduling_enabled: true  # Enable global workload scheduling pause (XMC_PAUSE_SCHEDULING_ENABLED)
```

### 2. Microservice Deployment Settings

Each microservice (`actuator`, `reconciler`, `ess`, `xid`, `ui`, `healthService`, `logMonitor`) can be independently scaled, configured with resource requests/limits, and assigned container image coordinates:

```yaml
actuator:
  enabled: true
  replicas: 1
  image:
    repository: "us-docker.pkg.dev/my-project/xmc/actuator"
    tag: "latest"
    pullPolicy: "IfNotPresent"
  resources:
    limits:
      cpu: "1000m"
      memory: "1Gi"
    requests:
      cpu: "250m"
      memory: "256Mi"

healthService:
  cacheTTL: "15s"
  forceRefreshMaxRequests: 5
  forceRefreshWindow: "5s"
  # Paused task resumption tuning (when featureFlags.pause_scheduling_enabled is true)
  schedulingResumeBatchSize: 100
  schedulingResumeMaxBacklog: 500
```

---

## Login session lifetime

`auth.sessionTtl` sets the absolute lifetime of both the signed session token and
its browser cookie. The chart passes it to the backend as `SESSION_TTL`. Use Go
duration syntax, for example `168h` for a week or `720h` for thirty days. The
value must be at least `1s`; an invalid value prevents the backend from starting.
The default is defined in `values.yaml` and `defaultSessionTTL` in
`pkg/auth/oauth2/config.go`.

```yaml
auth:
  sessionTtl: "168h"
```

Activity does not extend the lifetime. The setting applies to sessions issued
after the backend restarts; existing tokens retain their signed expiry. Longer
lifetimes also extend the period in which a copied token can be used. Logout
clears the browser cookie without revoking the token, and disabling a Google
account does not invalidate an existing session admitted by the domain allowlist.

---

## Sign-in identity checks

Sign-in requires the identity provider to report a verified email
(`email_verified`). With Google sign-in, a user whose email domain is in
`auth.allowedDomains` must also belong to a Google Workspace whose primary domain
(the `hd` claim) is in `auth.allowedDomains`, even if the address is also in
`auth.allowedEmails`. This refuses consumer Google accounts registered with a
company address, and still admits Workspace users whose address uses an alias or
secondary domain when both domains are listed. Other OIDC providers, such as Dex,
do not set `hd`, so only `email_verified` applies to them.

Emails are trimmed and lowercased before admission and in issued session tokens.
Per-experiment ACL matching is still case-sensitive, so list ACL users in
lowercase.

---

## CLI login

`xmc login` signs the user in through the browser and stores an XMC session token
for the CLI ([google/xmc#1309](https://github.com/google/xmc/issues/1309)). The
experiment state server starts the flow on `/api/cli/login` and finishes it on the
existing `/api/callback`, reusing the Dashboard OAuth client and its redirect URI,
so no OAuth client changes are needed. The callback applies the
[sign-in identity checks](#sign-in-identity-checks) and
[deployment admission](#deployment-admission), then hands the CLI a one-time code
through its `127.0.0.1` listener. Codes live in the `CliLoginCodes` Spanner table
(migration 016) for at most 60 seconds (120 seconds in manual mode).

`/api/cli/login` only starts a login in a page the user opened directly, such as
the browser window the CLI launches: browsers that report fetch metadata must send
`Sec-Fetch-Site: none` and `Sec-Fetch-Dest: document`. Browsers that omit these
headers are allowed.

Over SSH, or on a machine without a display, `xmc login --no-browser` (also chosen
automatically) uses manual mode: the CLI prints a `/api/cli/login?mode=manual` URL
for the user to open in a browser on any device. After sign-in and admission, the
callback shows a confirmation page that names the account and warns the user to
continue only if they started `xmc login --no-browser` themselves. Only when the
user selects **Continue** (a same-origin `POST /api/cli/code`, with admission
checked again) does the server mint a code, valid for 120 seconds and shown only
in the page body, never in a URL. The user pastes it into the terminal. Each
confirmation page works once (**Cancel** uses it up too) and for at most 5
minutes. A user can still be tricked into opening an attacker's link, confirming,
and handing over the code (consent phishing). This residual risk is the same as
for `gcloud auth login --no-browser`; the confirmation page and the code page warn
against it. The confirmation is also not bound to the browser: for its 5 minutes,
anyone who obtains the page's form value can mint the code once, which helps only
an attacker who also holds the PKCE verifier, that is, one who started the login.

The CLI then exchanges the code and its PKCE verifier on `/api/cli/token` for a
CLI session token, and sends the browser to `/api/cli/success` so the code leaves
the address bar. `auth.cliSessionTtl` sets the token's lifetime; the chart passes
it to the backend as `CLI_SESSION_TTL`. It accepts Go duration syntax from `1m` to
`720h`; any other value prevents the backend from starting. CLI tokens cannot be
refreshed: the user runs `xmc login` again when the token expires.

```yaml
auth:
  cliSessionTtl: "12h"
```

The CLI token is accepted only as a bearer token, never as the Dashboard cookie,
and every request re-runs [deployment admission](#deployment-admission).

Each CLI token is recorded in the `CliSessions` Spanner table (migration 017) when
it is issued; if the record cannot be written, no token is issued. `xmc logout`
calls `POST /api/cli/logout` with the token, which revokes its session. Every
service that accepts CLI tokens (the experiment state server, XID, Dashboard,
health and ROD services) rejects revoked or unrecorded sessions. They look sessions
up in the experiment state database and cache active ones for up to 60 seconds, so
a revoked token can still work for up to a minute on other replicas. If the lookup
fails, the request is refused (HTTP `503`, gRPC `UNAVAILABLE`). The XID service
reads that database through `xid_ess_spanner_database_name`, which the chart sets
from `experimentStateServer.spannerDatabaseName`; its service account already
holds `roles/spanner.databaseUser` on that database. There is no admin command to
revoke another user's sessions yet; an operator can revoke them in Spanner:

```sql
UPDATE CliSessions SET RevokedAt = CURRENT_TIMESTAMP()
WHERE Email = 'user@example.com' AND RevokedAt IS NULL;
```

The CLI login endpoints are not served with `auth.mockAuth` or
`auth.disableAuthentication`, or when the server has no Spanner database.

---

## Deployment admission

Every authenticated caller must be admitted to the deployment before any per-experiment ACL is
checked. This applies to browser and HTTP API requests, to gRPC calls to the experiment state
server (exposed through the gateway), and to the in-cluster gRPC APIs of the XID, dashboard and
health services. The rod-data-provider gRPC port is in-cluster only, is not part of its Service,
and performs no authentication. A caller is admitted when it matches, in order:

1. `auth.allowedEmails` (`ALLOWED_EMAILS`),
2. `auth.allowedDomains` (`ALLOWED_DOMAINS`), or
3. a direct binding of `auth.authorizedIamRole` (`AUTHORIZED_IAM_ROLE`) on `projectId` for the
   `user:`, `serviceAccount:` or `domain:` principal. Owner, editor and group bindings do not count.

Human users and service accounts that call gRPC directly, such as CI runners or workloads using
their own ID token, need one of these entries. The Component Manager Service additionally admits
`gcpServiceAccount`, the account `xmc-bridge` runs as; no other service admits it by identity.
`auth.disableAuthentication: true` skips admission on gRPC; use it only on local development
clusters.

---

## Additional OAuth client audiences

By default the services accept Google ID tokens only when their audience (`aud`) is
`auth.iap.clientId` (plus the IAP audiences on gRPC). `auth.additionalAudiences` lists other OAuth
client IDs, owned by you, whose ID tokens are also accepted. The chart passes them to every service
as the comma-separated `ADDITIONAL_AUDIENCES`.

This is an advanced option for scripts and custom tools that obtain Google ID tokens themselves.
For interactive use, prefer `xmc login` ([google/xmc#1309](https://github.com/google/xmc/issues/1309)),
which issues an XMC session token. Service accounts need no extra audience: they can mint an ID
token for `auth.iap.clientId` directly, for example with
`gcloud auth print-identity-token --impersonate-service-account=SA_EMAIL --audiences=CLIENT_ID`.

To let users sign in with their own tool:

1. In the deployment project, create an OAuth client of type **Desktop app**
   (**Google Auth Platform > Clients**) and download its client secret JSON.
2. Add its client ID:

   ```yaml
   auth:
     additionalAudiences:
       - "123456789012-abcdef.apps.googleusercontent.com"
   ```

3. Obtain an ID token through that client with the `openid` and `email` scopes. For example, with
   `google-auth-oauthlib`:

   ```python
   from google_auth_oauthlib.flow import InstalledAppFlow

   flow = InstalledAppFlow.from_client_secrets_file(
       "client_secret.json", scopes=["openid", "https://www.googleapis.com/auth/userinfo.email"]
   )
   credentials = flow.run_local_server(port=0)
   id_token = credentials.id_token  # aud is the Desktop client ID; send as "Authorization: Bearer"
   ```

`gcloud auth print-identity-token` cannot produce such a token for a user account: the token's
audience is always the Google Cloud SDK client, and `--audiences` is supported only for service
accounts. Services fail to start if `auth.additionalAudiences` contains a shared public Google
client, namely the Google Cloud SDK client (`32555940559.apps.googleusercontent.com`) or the
application-default credentials client
(`764086051850-6qr4p6gpi6hn506pt8ejuq83di341hur.apps.googleusercontent.com`). Any Google user can
mint ID tokens for those clients, which would leave [deployment admission](#deployment-admission) as
the only control. Callers using an additional audience still need to pass deployment admission.

---

## Out-of-Cluster & Remote Kubernetes Configuration (`k8s.remote`)

By default, the `actuator` and `reconciler` controllers manage `JobSet` and `Workload` resources on the Kubernetes cluster where they are deployed (using standard in-cluster service account tokens via `rest.InClusterConfig()`).

For hybrid topologies—such as running the control plane locally inside a lightweight KinD cluster while executing real workloads on a remote Google Kubernetes Engine (GKE) cluster—the chart supports mounting an external static kubeconfig.

### Configuration

Set the `k8s.remote` values in your Helm overrides:

```yaml
k8s:
  remote:
    enabled: true                     # Enable out-of-cluster Kubernetes client routing
    secretName: "remote-k8s-config"   # Name of the Kubernetes Secret containing the kubeconfig
    kubeconfigKey: "kubeconfig"       # Key in the Secret holding the raw kubeconfig YAML
```

### How It Works

1. **Volume Mount**: When `k8s.remote.enabled` is `true`, the chart creates a Secret volume mount in both the `actuator` and `reconciler` Deployments:
   - Mount Path: `/etc/remote-k8s`
   - File Path: `/etc/remote-k8s/kubeconfig` (or the key configured in `kubeconfigKey`)
2. **Environment Variable Injection**: Injects `KUBECONFIG=/etc/remote-k8s/kubeconfig` into the controller containers.
3. **Client Initialization**:
   - The Go Kubernetes client loading rules (`cmd/actuator/k8s_client/client.go`) inspect `KUBECONFIG` and fall back to `/etc/remote-k8s/kubeconfig` if present.
   - If `k8s.remote.enabled` is `false`, the controllers communicate strictly with the in-cluster API server via in-cluster service account tokens.
4. **Token Authentication Parity**: Because the target kubeconfig uses a static ServiceAccount bearer token minted on the remote cluster (e.g. via `kubectl create token actuator-service-account`), no Google Cloud SDK or `gcloud` binaries are required inside the controller images.

### Creating the Remote Kubeconfig Secret Manually

If you are deploying Helm manually rather than using `tools/dev_env/dev_env.py up --remote-k8s`, provision the Secret before installing the chart:

```bash
kubectl create secret generic remote-k8s-config \
  --namespace xmc \
  --from-file=kubeconfig=/path/to/target/cluster/kubeconfig
```

---

## Deployment Modes

### Local KinD Deployment

When deploying locally with KinD, use the local development values profile:

```bash
helm upgrade --install xmc deploy/helm \
  -n xmc \
  --create-namespace \
  -f deploy/helm/values.yaml \
  -f deploy/helm/values-local.yaml
```

### Production GKE Deployment

Production installations typically deploy via HPC Cluster Toolkit blueprints (`deploy/ctk_v2/`) which render values dynamically from `xmanager-cloud-deployment.yaml`.
