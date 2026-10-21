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
   - The CLI attaches a bearer token to the call metadata: a Google-signed ID token (for example from a service account's `generateIdToken`), a token from the configured OIDC provider, or an XMC session token.
   - Each gRPC service verifies the token, rejects Google and OIDC tokens whose `email_verified` claim is false (Google tokens must carry the claim), and then runs the same deployment admission check as the HTTP API (see [Client Authorization](#client-authorization)) before processing the call.

---

### 3. CLI Login (Browser Loopback)

`xmc login` signs the user in through the same OAuth client, identity provider and `/api/callback` redirect URI as the Dashboard ([google/xmc#1309](https://github.com/google/xmc/issues/1309)).

1. **Login ticket**: `/api/cli/login` accepts only a numeric loopback `port` (1024 to 65535), a CLI `state`, an S256 `code_challenge` and an optional `login_hint`. It stores them in the `cli_login` cookie, signed with the JWT secret (`aud: xmanager-cloud-cli-login`) and scoped to `/api/callback`, together with the hash of the `oauth_state` it generates. To stop other web pages from starting a login in the user's browser (web-origin login CSRF) or embedding it, browsers that send fetch metadata must send `Sec-Fetch-Site: none` and `Sec-Fetch-Dest: document`. Note that `Sec-Fetch-Site: none` is also sent for links clicked in non-browser desktop applications (such as chat or email clients) or pasted into the address bar, and browsers without fetch metadata (or plain-HTTP non-localhost origins) omit these headers.

The CLI login endpoints are registered only when authentication is enabled (no `MOCK_AUTH` or `DISABLE_AUTHENTICATION`).

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

Callers that authenticate but are not admitted receive `PERMISSION_DENIED` on gRPC and `403` on HTTP. With `DISABLE_AUTHENTICATION=true` (local development only), gRPC skips admission. The Component Manager Service additionally admits the workload service account that `xmc-bridge` runs as (`ess_service_account`); no other service admits it by identity.
