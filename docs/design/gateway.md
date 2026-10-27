# XManager Cloud (XMC) Deployment & Gateway Design

This document describes the deployment architecture of the XManager Cloud (XMC) application, highlighting the GKE (Google Kubernetes Engine) stack, supporting Google Cloud Platform (GCP) services, user authentication, and traffic routing mechanisms for both web-based and CLI clients.

> [!NOTE]
> This page focuses on the edge: routing, authentication, and the GCP services behind them. For the
> full component inventory and how the services interact, see the
> [Architecture Overview](../architecture/overview.md) and the
> [Component Reference](../architecture/components.md).

---

## Architecture Overview

The XMC application is designed around a hybrid model: core application services run inside a **GKE Cluster**, while managed databases, authentication providers, and access control policies are delegated to **GCP Supporting Services**.

```mermaid
graph TD
    subgraph Clients ["Clients (External)"]
        Browser["Web Browser"]
        CLI["CLI Client"]
    end

    subgraph GKE ["GKE Cluster (Kubernetes Boundary)"]
        Gateway["GKE Gateway <br/>(GKE Gateway API)"]
        
        UISvc["ui-service (Service)"]
        UIPod["UI Pod <br/>(Angular/Nginx)"]
        
        ESSSvc["experiment-state-server (Service)"]
        ESSPod["ESS Pod <br/>(Go gRPC & HTTP APIs)"]
        
        XIDSvc["xid-service (Service)"]
        XIDPod["XID Pod <br/>(Monotonic ID Gen)"]
        
        RedisSvc["redis (Service)"]
        RedisPod["Redis Pod <br/>(Session Cache)"]
    end

    subgraph GCP ["GCP Supporting Services (External)"]
        Spanner[("Cloud Spanner <br/>(Database)")]
        IAM["Google Cloud IAM <br/>(Access Control & Workload Identity)"]
        OAuth["Google OAuth2 / Identity Provider"]
    end

    %% Traffic Flows
    Browser -->|"HTTPS (path: /)"| Gateway
    CLI -->|"gRPC / HTTPS (path: /v1/*)"| Gateway
    
    Gateway -->|"/ (Routing)"| UISvc
    UISvc --> UIPod
    
    Gateway -->|"/v1/* & gRPC"| ESSSvc
    ESSSvc --> ESSPod
    
    %% Pod-to-Pod communication
    ESSPod -->|"Retrieve Monotonic IDs"| XIDSvc
    XIDSvc --> XIDPod
    
    ESSPod -->|"Cache Session State"| RedisSvc
    RedisSvc --> RedisPod

    %% External GCP Interactions
    ESSPod -->|"Read/Write State"| Spanner
    ESSPod -.->|"Workload Identity / Role Validation"| IAM
    Browser -->|"OAuth2 Auth Flow"| OAuth
```

---

## Core Components & GKE Deployments

### Running inside GKE (Kubernetes Boundary)

All standard stateless services and in-cluster caching workloads run on GKE:

1. **GKE Gateway**: An external HTTP(S) and gRPC load balancer managed via the **GKE Gateway API** (typically utilizing the `gke-l7-global-external-managed` controller class). It manages TLS terminates, and forwards requests to the appropriate in-cluster services.
2. **UI Deployment (`ui-deployment`)**:
   - **Tech Stack**: Angular SPA compiled and served via an Nginx web server.
   - **Role**: Serves the static frontend assets to the user's browser.
3. **Experiment State Server (ESS) Deployment (`ess-deployment`)**:
   - **Tech Stack**: Go web server exposing both a REST/JSON API and gRPC endpoints.
   - **Role**: The main orchestrator of XMC, coordinating experiment states, workload pipelines, and CLI communications.
4. **XID Service Deployment (`xid-deployment`)**:
   - **Role**: Lightweight internal service providing monotonically increasing, globally unique IDs.
5. **Local Redis Instance Deployment (`redis-deployment`)**:
   - **Role**: Serves as a fast session cache, authentication cache, and pub/sub channel for active workloads.

### GCP Supporting Services (External to GKE)

1. **Google Cloud Spanner**:
   - The primary distributed SQL database system storing persistent experiment schemas, task logs, and metadata.
2. **Google Cloud IAM (Identity and Access Control)**:
   - Grants and verifies fine-grained permissions for the cluster services. It handles **Workload Identity**, allowing GKE Service Accounts to impersonate GCP IAM Service Accounts without managing secrets.
3. **Google OAuth2 / IAP (Identity-Aware Proxy)**:
   - Acts as the global identity provider. Secures authentication flows and validates client tokens.

---

## Traffic Flows & Routing Rules

### 1. Web Browser Traffic Flow (HTTP/S)

```
[ Browser ] --(1. URL Request)--> [ GKE Gateway ]
                                        |
                                        +---> (Path: /) ------> [ UI Pod ]
                                        |
                                        +---> (Path: /v1/*) --> [ ESS Pod ]
```

1. **Initial Request**: The user navigates to the XMC application domain.
2. **TLS Termination**: GKE Gateway terminates TLS using certificates managed by Google Managed Certificates or private GTS/Let's Encrypt issuers.
3. **Path-Based Routing**:
   - Path `/` and other UI routes are matched against `HTTPRoute` rules and forwarded to `ui-service`, which serves the static Angular UI files from the Nginx server in the UI Pod.
   - Path `/v1/*` routes API requests to `experiment-state-server-service` (ESS HTTP API) for fetching dynamic experiment lists, user profiles, etc.
4. **Authentication**:
   - Unauthenticated users are redirected to **Google Accounts** for OAuth2 consent.
   - Once authenticated, the backend issues a signed, stateless session token in an HTTP-only cookie.

---

### 2. CLI Client Traffic Flow (gRPC / HTTPS)

The command-line interface (CLI) client bypasses the web UI entirely, communicating directly with the APIs.

```
[ CLI Client ] --(gRPC API Calls)--> [ GKE Gateway ] --(GRPCRoute)--> [ ESS Pod ]
```

1. **Direct Request**: The CLI initiates requests directly to the dedicated API hostnames (e.g., `api.your-domain.com` or `grpc.your-domain.com`).
2. **Protocol Routing**:
   - **HTTP Requests** targeting `/v1/*` are routed to the ESS HTTP endpoint.
   - **gRPC Requests** (such as `/experiment.ExperimentService/*`) are routed via `GRPCRoute` directly to the gRPC port of the `experiment-state-server`.
3. **Authentication**:
   - The CLI attaches a bearer token to the call metadata: an XMC CLI session token from `xmanager login` (see [CLI Login](#3-cli-login-browser-loopback)), a Google-signed ID token (for example from a service account's `generateIdToken`), a token from the configured OIDC provider, or an XMC session token.
   - Each gRPC service verifies the token, rejects Google and OIDC tokens whose `email_verified` claim is false (Google tokens must carry the claim), and then runs the same deployment admission check as the HTTP API (see [Client Authorization](#client-authorization)) before processing the call.

---

### 3. CLI Login (Browser Loopback)

`xmanager login` signs the user in through the same OAuth client, identity provider and `/api/callback` redirect URI as the Dashboard ([google/xmc#1309](https://github.com/google/xmc/issues/1309)).

```mermaid
sequenceDiagram
    participant CLI as xmanager login
    participant B as Browser
    participant ESS as ESS /api
    participant IdP as Google or Dex
    CLI->>CLI: state, PKCE verifier and S256 challenge, listen on 127.0.0.1:N
    CLI->>B: open /api/cli/login?port=N&state&code_challenge&code_challenge_method=S256
    B->>ESS: GET /api/cli/login
    ESS->>B: cli_login cookie (MAC'd ticket bound to oauth_state) and oauth_state cookie, 307 to IdP
    B->>IdP: sign in
    IdP->>B: 307 /api/callback?code&state
    B->>ESS: GET /api/callback
    ESS->>IdP: exchange code, read userinfo
    ESS->>ESS: require email_verified, run admission
    ESS->>ESS: store SHA-256 of a one-time code (60s) in CliLoginCodes
    ESS->>B: 302 http://127.0.0.1:N/callback?code&state
    B->>CLI: GET /callback
    CLI->>B: 302 /api/cli/success
    CLI->>ESS: POST /api/cli/token (code, code_verifier)
    ESS->>ESS: consume code, verify PKCE, re-run admission, sign token
    ESS->>CLI: access_token (aud xmanager-cloud-cli, jti, CLI_SESSION_TTL)
```

1. **Login ticket**: `/api/cli/login` accepts only a numeric loopback `port` (1024 to 65535) or `mode=manual` (see [Headless (Manual) Mode](#headless-manual-mode)), a CLI `state`, an S256 `code_challenge` and an optional `login_hint`. It stores them in the `cli_login` cookie, signed with the JWT secret (`aud: xmanager-cloud-cli-login`) and scoped to `/api/callback`, together with the hash of the `oauth_state` it generates. To stop other web pages from starting a login in the user's browser (web-origin login CSRF) or embedding it, browsers that send fetch metadata must send `Sec-Fetch-Site: none` and `Sec-Fetch-Dest: document`. Note that `Sec-Fetch-Site: none` is also sent for links clicked in non-browser desktop applications (such as chat or email clients) or pasted into the address bar, and browsers without fetch metadata (or plain-HTTP non-localhost origins) omit these headers.
2. **Callback**: `/api/callback` completes a CLI login only when the `cli_login` ticket is authentic, unexpired and bound to the `oauth_state` of the same round trip; otherwise it signs the user in to the Dashboard as before. `/api/login` deletes any `cli_login` cookie. In loopback mode, every outcome goes to the loopback URL, which is built from the ticket's port, never from the request: an identity provider error or cancellation, or a rejected user, is sent `error=access_denied`, and a failed code exchange or other server failure `error=server_error`. The email never appears in the URL.
3. **Token exchange**: `/api/cli/token` rejects malformed requests before reading the store, consumes the code in a Spanner read-write transaction before checking the PKCE verifier, and answers every code or verifier problem with `400 invalid_grant`. A user who lost admission since signing in gets `403 access_denied`. Responses are `no-store`.
4. **Use**: the CLI token is an HS256 session token with audience `xmanager-cloud-cli`. HTTP accepts it only as a bearer token, never as the session cookie, and gRPC accepts it in call metadata. Every call re-runs deployment admission.

#### Headless (Manual) Mode

Over SSH or without a display, `xmanager login --no-browser` (also chosen automatically) sends `mode=manual` instead of `port`. `/api/cli/login` requires exactly one of the two. The user opens the printed URL in a browser on any device and pastes the resulting code into the terminal.

```mermaid
sequenceDiagram
    participant CLI as xmanager login --no-browser
    participant B as Browser (any device)
    participant ESS as ESS /api
    participant IdP as Google or Dex
    CLI->>CLI: state, PKCE verifier and S256 challenge
    CLI->>B: print /api/cli/login?mode=manual&state&code_challenge&code_challenge_method=S256
    B->>ESS: GET /api/cli/login
    ESS->>B: cli_login cookie (mode manual) and oauth_state cookie, 307 to IdP
    B->>IdP: sign in
    IdP->>B: 307 /api/callback?code&state
    B->>ESS: GET /api/callback
    ESS->>ESS: identity checks and admission
    ESS->>B: confirmation page naming the account, signed confirmation in the form
    B->>ESS: POST /api/cli/code (Continue)
    ESS->>ESS: verify confirmation, re-run admission, store SHA-256 of a one-time code (120s)
    ESS->>B: code page (no-store, no-referrer)
    B->>CLI: user pastes the code
    CLI->>ESS: POST /api/cli/token (code, code_verifier)
    ESS->>CLI: access_token
```

1. **Confirmation first**: after the identity checks and admission, the callback mints no code. It shows a page naming the account and telling the user to continue only if they started `xmanager login --no-browser` themselves, just now. The page's form carries a confirmation (email, name, PKCE challenge, 5-minute expiry) signed with the JWT secret (`aud: xmanager-cloud-cli-confirm`), never a code.
2. **Code delivery**: `POST /api/cli/code` accepts only same-origin form posts (`Sec-Fetch-Site: same-origin` and `Sec-Fetch-Dest: document` when sent, on top of `CrossOriginProtection`), verifies the confirmation token, re-runs admission, and shows a 120-second code in the page body, never in a URL. **Cancel** verifies the confirmation too and issues no code; the form accepts only `action=continue` or `action=cancel`. Pages are `no-store` with `Referrer-Policy: no-referrer` and an added `Content-Security-Policy: frame-ancestors 'none'; form-action 'self'`, use no script, and escape every dynamic value. Failures, including a denied user, show an error page that does not name the user, because there is no loopback listener to report them to.
3. **Residual risk**: an attacker can still send a victim a manual-mode link built with the attacker's own PKCE challenge, and talk the victim into confirming and handing over the code (consent phishing). The confirmation page, the warnings and the short code lifetime reduce this to the same level as `gcloud auth login --no-browser`. In addition, the confirmation is a bearer value not bound to the browser session: for its 5 minutes, whoever obtains the page's form value can mint a code, which is only redeemable with the PKCE verifier from the login start.

The CLI login endpoints are registered only when authentication is enabled (no `MOCK_AUTH` or `DISABLE_AUTHENTICATION`) and the server has a CLI login code store.

---

## Access Control & GCP IAM Integrations

XMC strictly enforces access control policies using native Google IAM mechanisms:

```
[ GKE Pod ] --(Workload Identity)--> [ Google Cloud IAM ] --(Verifies permissions)--> [ Cloud Spanner ]
```

### Workload Identity
Each pod that interacts with Cloud Spanner or other GCP APIs runs under a dedicated GKE Service Account that is bound to a Google Cloud Service Account. 
- **Kubernetes Service Account**: `xid-service`, `actuator-service-account`, `reconciler-service-account`.
- **GCP IAM Mapping**: The GKE service account is annotated to impersonate a GCP IAM Service Account:
  ```yaml
  metadata:
    annotations:
      iam.gke.io/gcp-service-account: "your-gcp-service-account@your-project.iam.gserviceaccount.com"
  ```
- **Resource Access**: The GCP Service Account is assigned specific IAM roles (e.g., Spanner Database User, Secret Manager Secret Accessor) providing secure, passwordless resource access.

### Client Authorization
Every authenticated caller, whether it arrives with a browser session cookie, an HTTP bearer token or gRPC metadata, must be admitted to the deployment before any per-experiment ACL is evaluated. HTTP and gRPC share one admission pipeline (`oauth2.Authenticator.IsUserAuthorized`), which admits the caller if it matches, in order:
1. the email allowlist (`ALLOWED_EMAILS`),
2. the domain allowlist (`ALLOWED_DOMAINS`), or
3. a direct binding of the authorized IAM role (`AUTHORIZED_IAM_ROLE`, `roles/viewer` by default) on the deployment project for the caller's `user:`, `serviceAccount:` or `domain:` principal. Owner, editor and group bindings do not satisfy this check.

Callers that authenticate but are not admitted receive `PERMISSION_DENIED` on gRPC and `403` on HTTP. With `DISABLE_AUTHENTICATION=true` (local development only; honoured only by binaries built with `gotags = ["mockauth"]`, while production binaries refuse to start), gRPC skips admission. The Component Manager Service additionally admits the workload service account that `xmc-bridge` runs as (`ess_service_account`); no other service admits it by identity.
