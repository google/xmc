# XMC Admin Panel: Health service

| \#begin-approvals-addon-section Username Role Status Last change [atyburcy](http://teams/atyburcy) Approver 🟢 Approved May 29, 2026 [vluk](http://teams/vluk) Approver 🟢 Approved Jun 12, 2026 [fjchaves](http://teams/fjchaves) Reviewer 🟢 LGTM Jun 2, 2026 [janzak](http://teams/janzak) Reviewer 🟢 LGTM Jun 10, 2026      ![][image1] Approval Instructions: Please approve or LGTM through the [G3 Assist](https://goto.google.com/g3a-approvals-reviewing) sidebar. For more information, see [go/g3a-approvals-reviewing](https://goto.google.com/g3a-approvals-reviewing)  |
| ----- |

**Self link:** [go/xmc-admin-panel-dd](http://goto.google.com/xmc-admin-panel-dd)  
**Visibility**: Confidential   
**Status**: Current  
**Authors**: [Andrzej Fudro](mailto:andrzejfudro@google.com)  
**Team**: [*team/2041806362319*](http://team/2041806362319)   
**Tracking Buganizer issue/hotlist**: [b/489058919](http://b/489058919)  
**UI Design:** [go/uxd-xmanager-adminpanel](http://goto.google.com/uxd-xmanager-adminpanel)   
**Last major revision**: Jun 12, 2026

# Context

## Objective

The objective of this project is to provide a dedicated panel for the Platform Admins to get an overview of the current state of the XMC infrastructure, understand the health of its components and current configuration.

## Background

Currently, XMC lacks a centralized dashboard for component health, forcing administrators to check individual services manually. Although components expose health endpoints, these are only checked during initial installation. If a component stops responding later, admins are not alerted. A dedicated Admin Panel UI will allow admins to inspect XMC's health and configuration at any time.

## Current state

### Architecture

![\[Graph\]: Graphsmith Diagram. This flowchart depicts the XMC system architecture, illustrating the flow from client-side SDKs and UI through an authentication and control plane, which manages task execution via Kubernetes and persists data in Cloud Spanner with integrated logging observability. |||GraphData:{"state":"rendered","type":"reg","sketch":false,"diagramType":"flowchart","textFormat":"compressed","text":"H4sIAAAAAAAAA4WTUW+bMBDH3/cpTkiVtofuOU+TEEEpI11dTKVKqA8GrmCF2NnZzhoJ9bNPpgkoJFt5QMb/893v8P+gIbFrIV9+geG5uYGok6gsrMUB6bjLl2kRPN9HwA621cp/By9H7Sn5kJ4SCFXjOkHAWTjIU85ENYTGACP9djhLHTrbFoF/z45EmhAirSzpDlgnFB61mPMiiN92SHLrQbkVFoEj7ZG8OJI9J8sieE6WgyYrHGqBUDWE0XqMyh+LIBdmA48OHUKGtTTALaHYmhlS6kokhRYNxG9YOSu1glg1cmQLo7wIwso6YTWNJdIFL4J0YSBkCazSGH7qkqMdUFJfdYzM4qgIMqy0qmSHNKu/1k0jVTOceygN0l6UspP2cAxhvAhWEQPmSu5KYLr2R8yYff2wKoK1buBeK3kCnNIzJCONRVXh+e2z8FcRRJ12NfCdUOoCLBNq48FeNW2FtX75NVF7aWTZIXRSbQxYDaheNVUIVYdCwR7Jykp00EokQVV7+DbdMLy/v/u/OdvJ4mi2s35Yndvmw70ZdsJfj2nlzkwuhtvbH32TsagfzDBaeNi/y3MGmuBCH3zjI/wClae2WEOGvx0aayAJGdyhqJFM78E+8fFVON/PCe7YRO8dPJMHpzLStauQesgfZzq3mkSDYwZ/d9M4/C/kE5tfYw6jfIKKtDJuewZ10o/dd0iwdX5aDaQL0/u5mHw/j/wjbNX+O/LTLs4G5Cp+uuBw+91XHax9mi4u1aYHxqeh+SjoSlORLH2Hl+IVmr8hEtLuWwUAAA==","desc":null}][image2]

#### Existing health checks

Each XMC instance currently exposes a */healthz* endpoint that returns the service's state. These endpoints are configured as Kubernetes liveness probes and check the state of underlying dependencies:

1. **ESS**: Checks **experiment-db spanner database** (via `SELECT 1` query), verifies the **XID gRPC Client** connection, and pings **Redis** (`PingRedis`).  
2. **XID Service**: Checks **xids-db spanner** **database**(`SELECT 1` query).  
3. **Actuator**: Checks the **Kubernetes API** reachability (`CheckKubernetes`), checks **experiment-db spanner database** and pings **Redis** (`PingRedis`).  
4. **Reconciler**: Checks the **Kubernetes API** reachability (`CheckKubernetes`) and checks **experiment-db spanner database**.  
5. **Log Monitor**: Checks **experiment-db spanner database** and verifies the **Pub/Sub Listener** is active and healthy (`IsHealthy`).

Each */healthz* handler returns an HTTP 200 with the text "OK" on success, or an error for the first check that fails.

*Note: Currently, XID is not connected to the Kubernetes liveness probe and does not expose an HTTP endpoint. This should be changed soon and the design doc assumes that the change is in place:*  [\[XID\] add healthcheck endpoint](b/515674790)

# Requirements

## Functional Requirements

* The system **must** provide a dedicated User Interface (Admin Panel) within XMC for administrators to view system health and configurations.  
* The system **must** include a backend service responsible for actively polling and aggregating health information from various XMC components and their dependencies.  
* The backend service **must** monitor the health status of:  
  * Kubernetes-hosted XMC instances (ESS, XID, Actuator, Reconciler).  
  * The Log Monitor Cloud Run service.  
  * Essential dependencies (e.g., Spanner, Redis).  
* The Admin Panel UI **must** feature a "Refresh" button allowing administrators to trigger immediate, on-demand health status updates.  
* The Admin Panel UI **must** display relevant non-sensitive configuration details.  
* The UI **must** hide the admin panel section if the current user is not an administrator.

## Non-Functional Requirements

* **Access Control:** Access to the Health Admin Panel and its data **must** be restricted to authorized administrators. User-facing endpoints **must** validate user permissions.   
* **Data Exposure**: The system **must not** display or expose any sensitive information, passwords, or secrets.   
* **Usability:** Health status and configurations should be presented clearly and concisely for administrators.  
* **Reliability:** The Admin Panel UI and underlying health-service backend **must** remain operational and capable of displaying diagnostic data, even when one or more monitored XMC components (e.g. ESS) or dependencies (e.g. Spanner) are unresponsive or failing.

## Out of Scope

* The system **will not** provide real-time, push-based notifications. Health status is primarily on-demand or based on internal polling.  
* The system **will not** perform any automated remediation or attempts to fix unhealthy components.  
* The system **will not** store, analyze, or provide access to historical health data. The focus is on the current status.  
* The Admin Panel UI **will not** allow users to modify any configuration values or restart services; it is read-only.  
* The system **will not** capture or report granular pod-level health metrics (e.g. how many instances are healthy). It will only report whether a given service as a whole is available and healthy.  
* The system **will not** report the underlying root cause or specific error messages when a core service fails its health check (only general error status is reported).  
* The Admin Panel UI **will not** be available during an outage of the main XMC Angular frontend.  
* XMC **will not** manage IAM role bindings or user lifecycles. XMC only verifies whether a user possesses the configured IAM role; assigning or revoking admin privileges is managed externally via standard GCP IAM tools.

# Design

## Overview

To decouple health monitoring from core services, we will create a standalone Kubernetes deployment `health-service` in the same cluster and namespace as other XMC deployments. The new service will report the health of XMC instances and aggregate their configurations. The service will expose three endpoints:

1. `GET /health/status`: Provides general system information and health status of the XMC components  
2. `GET /health/configs`: Provides a structured list of XMC configurations.  
3. `GET /health/userinfo`: Provides information about whether the user has the admin IAM role (needed for UI section visibility).

The health service will communicate with other XMC services via their */readyz* endpoint to report their health state and will directly probe essential external dependencies (Spanner, Redis):

![\[Graph\]: Graphsmith Diagram. This flowchart depicts the XMC system architecture, illustrating the flow from client-side SDKs and UIs through a central control plane and Kubernetes execution engine, all monitored by a dedicated health service and supported by unified logging and persistent storage. |||GraphData:{"state":"rendered","type":"reg","sketch":false,"diagramType":"flowchart","textFormat":"compressed","text":"H4sIAAAAAAAAA5VU0W7bOBB871csFBToAUpRqz3El4cDBMWIfXYbRnSBAMI9UNJaIkyTLkmlMWDk2w+UZZuxlTbnB0PSDLmz5MwCVJqta5jfvIPu9/49JIKjtDBjG9SH7/RmmgUPXxMgG1sr6d6Dfw/o98kO/D6BWFaNYBooiTuCv/dEVhqNAaLV0+akRNzYOgvcf8/CMTJha/iqJLdKc1kBRf3ICzyQxjQLOlYH9WyTKI2QKGm1EkAEk8f1I0qzYPS0Rs1Xrn9qmcV2K9QO9Np9mNxkwcPkZl+olQ5MlhAnM483v8+COTNLuG+wQUix5Aao1chWpkfctMlRS7RoYPSERWO5kjCSFfdUxsk8C+LCNswq7ZWaDmkWTIcGYjKB2+kI/lE5RduKmrrqHjcdJVmQYqFkwQXqHiUzVVXujN3qu9ygfmQ5F9xuDiRCs+A2IUCanDY5EFW6RcarMru7zYKZqvZ31lOGoDbcWJQFntqNxN+yIBGqKYGumZS9MqndCCdzoTS8vHqQqjwemrEbgTCmsOBCXF/k0SLK89BYrZZ4fRHlQ/YZu9fLn7y09XW0fgoLJZS+vvg0vFr8mZ/VTplcdrVXzFr3+GEiH7nhuUAQXC4NWAUoF0oXCIVAJuERteUFE1Bz1EwX9eYP33/w/PzsbvjsWzpKzr7N7m7P7b2LboqCOfOYmq+NH2G4vPx7W6Uk2baW9fLbIuP5nIDS0MNoHe447gGl68JiCSn+aNBYA5OYuCsoUZutk/iG5L0i0/W3l9k1tHWJOyO0ySJalU2Begvz+zMGtUqzCg+7OFf5If416bfR7NcfJ/OjvERJ06xO5O0Z3WkI1LBq3LQxMB2arcuyn9VT7k9mi/pX3Dd19CLWr7QyHVK4/Oiqt0HcTwXK5XILhPpR3xVuclNonrt+++C36DrJ8Stn/H/cOD4qGdODz48uPQN8s70A/Gi+APx8dsDuyNJGuom8PaTVY9w3qDcnpuwgwmV1sMwbJ95u5HzgsuQFGhhcufETfTkOGEeg7SQcXIWDYTj4K4w+hdEgjKIw+hxGX+A3E/Fdq+U/0jeiZjUIAAA=","desc":null}][image3]

## 

## Create dedicated readiness probes (`/readyz`) for core microservices

Currently, each core XMC microservice exposes a single `/healthz` endpoint, which `Kubelet` uses as a liveness probe to verify basic container reachability and underlying connectivity.

To support additional checks for Spanner Schema ([described later in this document](?tab=t.0#bookmark=id.hpp2fwpnjkgd)), we will introduce a dedicated *`/readyz`* endpoint on each core microservice to serve as the `Kubernetes` readiness probe. The endpoint will execute the same checks as the current */healthz* but extended with additional verification of running against the proper Spanner schema version. The separation of liveness and readiness probes allows us to verify the database schema version and stop network traffic in case of a version mismatch but without crash-looping the pods.

Since */readyz* endpoints will have the same basic checks as */healthz* and a new additional one, these will be called by the health-service.

## Expose health status data via API

### Data required on the UI

![][image4]

We need to expose the endpoint that will be used to fill the fields above on the UI:

1. App Version  
2. ESS DB Schema version  
3. XID DB Schema version  
4. System observability \- visible only if user specifies link to the custom monitoring tool in helm  
5. Overall Health status  
6. State of Pause Scheduling option  
7. List of health for each component   
   1. its health status  
   2. its display name  
   3. (optional) error message  
8. Time of the health check

### Endpoint

We will expose a GET endpoint that will return the information for the fields above. 

```
/health/status
```

The endpoint will call all the core XMC instances and relevant dependencies, and aggregate them into the list. Additionally, it queries the XID and experiment databases to retrieve current schema versions:

![\[Graph\]: Graphsmith Diagram. This flowchart illustrates a health monitoring architecture where a central Health Service aggregates status probes from various core microservices, databases, and infrastructure components, all triggered by a user-initiated request from the XMC UI. |||GraphData:{"state":"rendered","type":"reg","sketch":false,"diagramType":"flowchart","textFormat":"compressed","text":"H4sIAAAAAAAAA5WUUWvbMBSF3/crLi4dGziwpuua5mFgHNOYpqsXuRAwY8j2TSyiSkGSt2b4xw85juukLtn8ELD0Wffcq3MCsFJ0U8Bs/g6a5/wcfM5QGJjRLap2/TFMnMW9D48heGJVcqqARJ7zowW80hSJY3+bxe6RU6TcFHAvBTNSMbECguoXy7CFpiRxGqrZ6jnGlwr3h2AOodCGigx1ywSEJE5ASEfXIpwkziKcHB1bS/bjxPEyU1IjVWd9HviJM8dMioxx7O7MHm4TZyZXexE9Gh9FjopvbY+hWCqqjSozUyqE9zDBDYocRcY6mknkffsZLKLE8bkscyAbKgQqwOcNKvaEwgzytKOh5uuuDvlnlutDch5MQmI7yZmGmOo1fC+x7E7gbkQS565MUQk0qMGLwp6WiNly289SKji8IxAyf7lDbbYcYUpgyTgfn6XD5TBNXW2UXOP4bJiO6CU2r4PfLDfFeLh5djPJpRqffRpdL6/S1+MMwcijqh1TwmDwtZrGcQS3QQzFDtOGmlJXtSUP/GlpmJK37LlvKlIyRW3r1oZ7bbMpOayrkObbP5W130lmEU5OMp4fn2TmgX/M7PwwL4W9x8qa9ZggwSzwYyBZgU/0nq0UNUwKXbUm/L8PenqJmFhVO+f1x7edJoTCoBKUv8Ri24y+m+eujIseoXvEFq6d3q0Ouz+AvjO62j0/3g2wwGz9EofK5qMfakLXo2ePHQ9iF0j/dJ0D6O06s4fbU9g/ZpgzsdbwgYmc2Wu5sMa/+dh+bLdJnewLd+heup/dK/eLe+2O3Bs4ke13tYa/9pmjtWYGAAA=","desc":null}][image5]

### Proto definitions

The endpoint response will combine component health statuses and general system information (e.g. running app version):

```protobuf
syntax = "proto3";

package xmanager_cloud.health;

import "google/protobuf/timestamp.proto";

enum ComponentType {
   COMPONENT_TYPE_UNSPECIFIED = 0;
   COMPONENT_TYPE_CORE_SERVICE = 1;
   COMPONENT_TYPE_EXTERNAL_DEPENDENCY = 2;
   COMPONENT_TYPE_CUSTOM_COMPONENT = 3;
}

enum HealthState {
   HEALTH_STATE_UNSPECIFIED = 0;
   HEALTH_STATE_HEALTHY = 1;
   HEALTH_STATE_UNHEALTHY = 2;
}

message SystemInfo {
  string app_version = 1;
  string ess_db_schema_version = 2;
  string xid_db_schema_version = 3;
  string observability_url = 4;
  bool is_scheduling_paused = 5;
}

message ComponentHealth {
  string name = 1;
  ComponentType type = 2;
  HealthState health_state = 3;
  string error_message = 4;
}

message HealthStatusResponse {
  SystemInfo system_info = 1;
  HealthState health_state = 2;
  repeated ComponentHealth components = 3;
  google.protobuf.Timestamp health_check_time = 4;
}

```

The component health list is flattened in the response; the UI will group and display them in tables based on ComponentType.

To support future extendability (e.g. introducing state of partial availability) *health\_state* is defined as an enum instead of a boolean.

The top-level health\_state will be set to *HEALTH\_STATE\_UNHEALTHY* if at least one component (regardless of its type) is marked as unhealthy.

## Calculate health status

To populate the ComponentHealth array in the response, the health-service will need to discover the health status of core XMC instances and its dependencies.

### Calculation of health status of core Kubernetes instances

The health-service will do direct calls via HTTP to all the core XMC instances:

1. ESS (`http://http-service:8080/readyz`)  
2. XID (`http://xid-service:8080/readyz`)  
3. Actuator (`http://actuator-service:8080/readyz`)  
4. Reconciler (`http://reconciler-service:8080/readyz`)

### Calculating health of log-monitor

For `log-monitor`, we must use a different mechanism than for other core services because it runs on Cloud Run. Similar to `install.sh`, we will use the Cloud Run API to check the service's health condition (which reflects its connected [health probe](https://source.corp.google.com/piper///depot/google3/third_party/xmanager_cloud/log_monitor/health.go;rcl=910643521;l=29)). This requires binding the `health-service`'s Kubernetes Service Account to a GCP Service Account with `roles/run.viewer` or `run.services.get` permissions via Workload Identity.

Log-monitor handling will be an exception for now but in the future if we migrate it to kubernetes it can be changed and done in a consistent way with other k8s deployments.

### Calculating health of XMC dependencies

The Admin Panel should display the health of three critical dependencies: experiment-db, xids-db, and Redis.

 `health-service` will initialize direct Spanner client connections (`experiment-db`, `xids-db`) and Redis ping within its own process, probing them directly.

### Calculating health of XMC Custom Components

Health Service will use `CustomComponentsInfoProvider.GetComponentsHealth` method (defined in [Custom Component support](https://docs.google.com/document/d/1OulHPZDytArKTOizRWeRr-aE9MI83PgzUz7uwTYWPUw/edit?resourcekey=0-j8WpJnOUBLTEc2-4ZQrWOQ&tab=t.0#bookmark=id.qdhc7nbjadgw) section) to fetch health status of custom components.

### Caching and refresh behavior

#### Cache

The `health-service` will utilize an in-memory caching mechanism for the `HealthStatusResponse` to mitigate elevated latency and GCP API rate limiting (such as Cloud Run API). This cache will feature a default 5-second TTL that can be adjusted via configuration. `health_check_time` in the response will either return the current timestamp or timestamp of the used cached value.

#### Force refresh

Administrators can trigger an immediate health update by using the "Refresh" button in the UI, which adds a `?forceRefresh=true` query parameter to the `GET /health/status` request. Upon receiving this parameter, the backend performs a synchronous fan-out check, bypassing the cache to refresh it with current data. Regular requests omitting this parameter will continue to be served from the existing cache. 

##### Rate limiting

To avoid overloading backend dependencies if administrators force a refresh frequently, `health-service` uses an in-memory Token Bucket rate limiter (`golang.org/x/time/rate`). The rate limiter allows a burst of 3 forced refreshes and refills at a rate of 1 token per 5 seconds. If the rate limit is exceeded, the backend ignores the force refresh flag and returns the cached response.

### Limitations

There are a few limitations with the selected approach:

1. **Rollout upgrades:** During a rolling upgrade where a new pod is spawned but unhealthy, `health-service` HTTP probers will be routed to the older, healthy pod. Consequently, the Admin Panel will report the service as healthy, masking the stuck rollout in the background.   
2. **Accessing the error details:** Once a core microservice pod fails its readiness probe, Kubernetes Service removes it from its Service endpoints. This means that once pods are unhealthy, the health-service can only report that they are unreachable without the specific error details.  
3. **Cloud Run API response**: Because the GCP Cloud Run API only returns high-level container condition status (e.g. `Ready: True`) and does not return the exact */readyz* error response.

## Calculate SystemInfo

To populate system\_info in the response, the health-service will do the following:

1. #### App version

Before we introduce semantic versioning, we can return the current commit hash (similar to how we currently tag container images). This can be injected during the build by adding:

```
env = {
    "XMC_APP_VERSION": "{BUILD_CHANGELIST}",
},
```

to the BUILD file. The value can then be retrieved in Go code by calling `os.Getenv("XMC_APP_VERSION")`

2. #### Experiment/XID database schema version

The `health-service` initializes Spanner clients for the experiment and XID databases at startup, querying the `SchemaMigrations` table on both to populate `ess_db_schema_version` and `xid_db_schema_version`. If the database connection fails or drops, the error is caught and `UNAVAILABLE` is returned in the response.

3. #### System observability link

Taken from the helm config. Described later in this document ([bookmark](?tab=t.0#bookmark=id.t4as9m9jqg6q)).

4. #### State of Pause Scheduling option

Value indicating if scheduling is paused in the XMC. This will be covered in a [separate tab](?tab=t.4dh48da4bfue#heading=h.rbr90bwbc4mv) inside this design doc.

## Expose configuration entries via API

### Data required on the UI

![][image6]

The *configuration entries* section on the Admin Panel UI will expose key-value parameters that configure deployments and affect their runtime behavior (e.g., batch sizes, intervals, max concurrent updates, database URLs etc). These parameters are grouped and displayed as key-value pairs.

### Endpoint

We will expose a GET endpoint that will return grouped configuration entries:

```
/health/configs
```

### Proto definitions

```protobuf
message ConfigInfo {
  string key = 1;
  string value = 2;
}

message ConfigGroup {
  string display_name = 1;
  repeated ConfigInfo configs = 2;
}

message ListConfigEntriesResponse {
  repeated ConfigGroup config_groups = 1;
}
```

### Config discovery strategy

#### Core XMC Instances

`health-service` will return configuration data for core k8s XMC instances (ESS, XID, Actuator, Reconciler). Currently, XMC microservices receive their configurations via container command-line flags (`args`) and environment variables (`env`) but there is a parallel effort to migrate them to Kubernetes ConfigMaps. To expose data via `/health/configs` two strategies were evaluated:

##### **Option 1**: Mount ConfigMaps as Volumes (Proposed / Selected Approach)

![\[Graph\]: Graphsmith Diagram. This flowchart depicts the XMC Health Service architecture, where the service retrieves health configurations via HTTP and reads them from Kubernetes ConfigMap volumes that are automatically synchronized by the Kubelet. |||GraphData:{"state":"rendered","type":"reg","sketch":false,"diagramType":"flowchart","textFormat":"compressed","text":"H4sIAAAAAAAAA42TYYubMBiAv+9XvFgOOlDoeisr/XAg1q1ylR1NexTkGFFjDZcmJYnbPPrjhzbW2u1c/SBoHpM8+giwk/iQw3L1Aerj7g48RgnXsMQlkebuJoisbejBJgCX7wqGJaAn13oxw26h88iqzvWtdqoFwUznEApOtZCU7wAR+ZMmxCALFFmGMQNXE4Si4Jqk4Ame0V2ID/AsWLEnCoZfKSOqVJrsPxr++fvyh49QZPkI/fXEebMVtQ3mkbUN5v2U660jy010gbWQ/ejK9yJrRRLBE8rIO3CrteEpkaysXsjjVLW0gmGIOd6RFOISHouYMKIbPS882XWeAB+h80688GTWJbbB/JKorbpE43iJ1UZdrPW70kG6ZJVLJiR0PydwkTYfW+mSEVggyChjs0E8zsZxbCstxSuZDcbxFN8Tc+n8oqnOZ+PDbzsRTMjZYDT9kk3i7ksMQIur9c69guM8HBfr9RN889eQn6CkVlHHOtiLdisYFuhf6TYekuBUQSbFHpYiwexcpunxHHS9cNsmrAhOj02aN1DbYH4D5XrrG6iV73WcTFCgSp7kUnD6RjrxafGOltk+OI5zbCZxCy0AlTw5mjK7/1cP2yoalR62FTVCPey1bk+WjPJXBUPKU5oQBaNKfdL8aNUgqmMd2Z/ssX1vf7Yn8J9Q/wDpvpvrSgUAAA==","desc":null}][image7]

* **Mechanism:** The `health-service` Kubernetes deployment spec mounts core service ConfigMaps directly as local volumes, allowing the service to read configuration files from the local filesystem.  
* **Assumption:** This solution assumes that all the sensitive data (password, credentials) will live not in the ConfigMaps but in the Kubernetes Secrets instead.  
* **Pros:**  
  * **Zero API Server Overhead:** Reads directly from the container filesystem, eliminating network latency and protecting the GKE control plane from polling spikes.  
  * **Zero RBAC Surface:** Does not require granting `health-service` any Kubernetes API permissions to read ConfigMaps.  
  * **Automated Kubelet Synchronization:** Kubernetes automatically updates mounted ConfigMap files when the underlying ConfigMap changes.  
* **Cons:**  
  * **Static Manifest Coupling**: Every ConfigMap must be explicitly defined in the health-service's volumes and volumeMounts. Adding a new microservice in the future still requires updating the health-service's Deployment manifest and restarting the pod.  
  * **Lack of Granular Visibility Control:** Mounting ConfigMaps directly into the pod exposes every single key-value pair by default. It does not give us granular control over what is being exposed or redacted.  
  * **Feature dependency:** This solution is blocked on the work to migrate to ConfigMaps.

##### Option 2: Expose configuration via dedicated HTTP endpoint on each microservice

* **Mechanism:** Instead of `health-service` accessing Kubernetes ConfigMaps directly, each core microservice implements a standardized internal diagnostic endpoint (e.g., `GET /config`). `health-service` performs an asynchronous HTTP fan-out to aggregate and display these active runtime configurations.  
* **Pros:**  
  * **Granular Control:** Each microservice retains 100% control over what property is exposed and how. If necessary in the future, it would also allow exposing the configurations that live outside the ConfigMaps.  
  * **Zero K8s API & RBAC Dependencies:** Operations are purely pod-to-pod over internal Kubernetes DNS, completely bypassing the K8s control plane.  
* **Cons:**  
  * **High Implementation Cost:** Requires embedding the `/config` HTTP handler inside every core microservice binary and maintaining a shared reflection library.  
  * **Runtime Scrape Latency:** Introduces network hops and potential timeout risks during the aggregate fan-out scrape.  
  * **Reliability**: if the service fails its healthiness/readiness check the config endpoint will be unreachable

##### Option 3: Query Kubernetes API for ConfigMaps via Label Selectors

* **Mechanism:** `health-service` uses the Kubernetes API to query ConfigMaps matching a specific label selector (e.g, `xmanager.google.com/config-type: xmc-instance-config`). To prevent excessive API server calls, responses must be cached in-memory.  
* **Pros:**  
  * **Dynamic Discovery:** Adding a new core service or custom component config is zero-touch for `health-service`. Developers simply add the label to their ConfigMap manifest, and it is discovered automatically without restarting the `health-service` pod or updating volume mounts.  
  * **Unified Mechanism:** Provides a single discovery pattern that could work identically for both Core Services and Custom Components.  
* **Cons:**  
  * **Increased Permissions:** To allow `health-service` to list ConfigMaps by label, its Service Account must be granted `get` and `list` permissions on all ConfigMaps in the namespace. We cannot limit the permission to allow querying only on the specific selector.  
  * **Network Overhead & API Server Dependency:** Introduces additional network hops to query the Kubernetes API server and couples the endpoint's reliability to control plane availability.

**Reasoning for Option 1:** It is selected to avoid overengineering and go with a simpler solution. Option 2 is also rejected due to the unreachable config values in case of a system failure.

#### Custom Components

Health Service will use `CustomComponentsInfoProvider.GetComponentsConfig` method (defined in [Custom Component support](?tab=t.0#bookmark=id.qdhc7nbjadgw) section) to fetch configuration values for custom components.

## Custom Components support

Reporting the health status and displaying configuration of custom components relies on the XMC Component System implementation ([go/xmc-components-high-level-design](https://goto.google.com/xmanager-cloud-components-high-level-design)). 

XMC Component System should expose a common golang library that the Health Service can use to fetch health and config information:

```go
// CustomComponentsInfoProvider defines the interface for component health check and configuration retrieval.
type CustomComponentsInfoProvider interface {
	// GetComponentsHealth returns health status for all custom components.
	GetComponentsHealth(ctx context.Context) ([]ComponentHealth, error)

	// GetComponentsConfig returns the configuration parameters for all custom components.
	GetComponentsConfig(ctx context.Context) ([]ComponentConfig, error)
}

// ComponentType distinguishes between 1p and 3p custom components.
type ComponentType int
const (
	ComponentTypeUnspecified ComponentType = iota
	ComponentTypeCustom1PComponent
	ComponentTypeCustom3PWrapper
)

// HealthState represents the aggregated, high-level operational status of a component.
type HealthState int
const (
	HealthStateUnspecified HealthState = iota
	HealthStateHealthy
	HealthStateUnhealthy
)

// ComponentHealth represents the health status of a specific component.
type ComponentHealth struct {
	Name          string
	Type          ComponentType
	HealthState   HealthState
	StatusMessage string
}

// ConfigParameter exposes a standardized, human-readable configuration property.
type ConfigParameter struct {
	Key         string
	Value       string
}

// ComponentConfig represents the configuration parameters of a specific component.
type ComponentConfig struct {
	Name             string
	Type             ComponentType
	ConfigParameters []ConfigParameter
}
```

## Permission validation for admin access

*Note: This design decision is subject to change as there is a parallel effort that might potentially change our auth setup.*

Health/Config endpoints should only be available for the admins.

Currently in XMC we only do permission validation on the resource-level ACLs. In the context of the admin panel we need to:

* Hide admin panel section on the UI for non-admin users  
* Block the health/config requests for non-admin users

### Backend permission validation

1. #### Configuration via Terraform / Helm:

   1. We will introduce a new **`adminIAMRole`** variable in the Helm values.  
   2. This variable will be passed to the health service deployment container as an **admin\_iam\_role** argument.  
   3. Terraform config for *health-service* will include definition for adding the **roles/resourcemanager.projectIamAdmin** role for the serviceAccount so the service can use IAM API for checking if a user belongs to the role defined under *admin\_iam\_role*.

2. #### Validation in Health Service Endpoints:

   1. The `health_service` endpoints will verify incoming IAP headers using `AuthenticateUserFromContext`.  
   2. The endpoint handler validates whether the caller has the configured admin\_iam\_role (e.g, *roles/owner* or a custom role) by querying the GCP Cloud Resource Manager API (*projects.getIamPolicy*) to inspect project IAM bindings. This will already be implemented in *IsUserAuthorizedViaIAM* method (pipeline.go) that [oauth2 PR](https://github.com/google/xmc/pull/3) introduces so this logic will be reused. This method uses a 2-minute cache that we will reuse to avoid excessive API calls.  
   3. For data-fetching endpoints (*/health/status*, */health/configs*), if the caller does not possess the role, the requests will be blocked immediately on the backend and return an HTTP `403 Forbidden` response.

## UI health page visibility

### Overview

The admin panel page should be available only to users with the defined `admin_iam_role`. For this purpose, we will introduce a new endpoint to fetch user information and control admin panel visibility.

![\[Graph\]: Graphsmith Diagram. This sequence diagram illustrates the admin authentication and dashboard loading process, where the Angular frontend verifies user privileges with the health-service before fetching system status and configuration data from GCP and Kubernetes dependencies. |||GraphData:{"state":"rendered","type":"reg","sketch":false,"diagramType":"sequenceDiagram","textFormat":"compressed","text":"H4sIAAAAAAAAA4WST2/aQBDF7/kU70RAKonaU8UByTU2oAJBuKGtqgpN1gNexd7d7qypqqrfvbJN/jQo6nF23vP83oyFf9RsFE80HTxVFwBAKliPW2EPEkR5pU1btU1HPmilHZmANGkF5lCX5JF6awKb/Ew2yxpZwVSGYijsj1rxmWjCrlFN4zWu8fG9NA9scjZKs1y0cmMDwx65Y3szYTfC2ytEzpVaUdDW4IO1QYInhx6iOhRIS/uzNTeW4XicJiPcODb4soxxO29baTIcj2fZCNPkE647zuta2Guzt61ilp0UccHqHnqPpo2CBNSsZ6ep2nlb8oP6NOk3nNdHXfKBZYRv6818O18k02QXxXGSZbtospyvdutolSy+488TTOPdNOk9LmctDyYkxZ0ln19CdM535KEDV+hvtei7knGzWnw9J4MjwyVIKRZ5ohm8vtF3V0+zsKKjPrSrfbnEuNTq/pEu6676jPL1zUqgUAv6KQdVIGurwetyZc1eHx71cVcO/r3LlkqdU2CEgsL/jjMet0Ej+WUUUjJDWwecgvSQqYIr6i4tLywbphxLW5vA+YlkSQ5bW9YVC3qIawm2eoB88Td0I7rAGxZnjTD6XY0ePpM32jyLdrIttITug4kJXvMz79Tb2j2iyOAvRYeTrc4DAAA=","desc":null}][image8]

### Expose a New userinfo Endpoint

To provide information about user admin access to the UI, we will implement a new `GET /health/userinfo` endpoint within `health-service`. 

#### Protobuf Definition

```protobuf
enum Privilege {
  PRIVILEGE_UNSPECIFIED = 0;
  PRIVILEGE_ACCESS_ADMIN_PANEL = 1;
}

message UserInfoResponse {
  string user_email = 1;
  repeated Privilege privileges = 2;
}
```

### Request Handling & Authorization Flow

1. **Identity Extraction:** health-service extracts the caller's identity from incoming IAP-signed headers via AuthenticateUserFromContext.   
2. **Role Membership Check:** It will reuse the IAM verification logic described in the backend validation section, the service queries the GCP Cloud Resource Manager API (*projects.getIamPolicy*) to inspect project IAM bindings and verify if the caller possesses *admin\_iam\_role*.  
3. **Response Payload:** Returns a *privilege* array with ACCESS\_ADMIN\_PANEL element.  
4. **Error Handling:** Returns a 200 OK with `empty` privileges array.

### UI Hides Sidebar Elements & Secures Routes

The Angular frontend will use the data returned from /health/userinfo to control admin panel access

* **UserService Initialization:** During application bootstrap, the `UserService` executes the fetch to `/health/userinfo` and populates a global `isAdmin` signal.   
* **Conditional rendering:** Angular uses the signal to hide the admin panel section from the navigation sidebar.  
* **Route Guarding:** A CanActivate guard is applied to the `/admin-panel` route to block direct URL access.

## Add Spanner schema version match to readiness probes

#### Current State

Currently, each of the core services exposes /healthz endpoints and checks their Spanner connection by executing `SELECT 1` query and verifying that it got a successful response.

#### Addition for schema version verification

We will introduce a dedicated `/readyz` (readiness) endpoint for the core services. The `/readyz` handler will perform the same base connectivity checks as `/healthz`, but will additionally verify that the running service version matches the expected Spanner database schema version.

This expected schema version can be injected by adding a `genrule` that determines the highest migration version number at the `BUILD` time. To avoid repeating the `genrule` for every service, we will add a new Golang package that exposes variables for the expected `ESS` and `XID` schema versions, limiting the `genrule` on that shared package's `BUILD` file. Any service importing this package will automatically have access to the calculated expected version.

Prototype for this solution can be checked here: [WIP: schema version health check](https://critique.corp.google.com/cl/908266136)

#### Consequences of the change

If there is a mismatch between the running service version and the database schema version, `/readyz` will return false. Kube-proxy will withhold Service traffic from the pod, and the service will stop serving http traffic until the mismatch is fixed and readiness starts returning true. In contrast to a liveness probe, a failing readiness probe does not cause pod crash loops.

## System Observability section

![][image9]

For the System Observability section we will only redirect to the user-defined monitoring tool. This will be controlled by the optional helm variable `customMonitoringUrl`. If the value is not provided we will hide this section.

## Client-Service Communication

Currently, only `ESS` is exposed publicly via `http-ingress`. Routing traffic directly to `health-service` would require duplicating auth validations (JWT, CORS/CSRF). There is a parallel effort that investigates replacing ingress with GKE Gateway, and potentially introducing BFF (Backend for Frontend) which will enable smoother communication with the service.

The networking part of communication between UI and health-service is **out of scope** for this design doc.

## Reliability

To guarantee that the Admin Panel remains 100% operational and accessible for debugging even during severe cloud outages or database partitions, `health-service` will take two approaches:

### 1\. Spanner Client Connection Pooling & Lazy Initialization

Connections to the experiment and XID databases are needed for schema version information and reporting health state of the spanner instances. 

If `spanner.NewClientWithConfig` fails during bootstrap, `health-service` logs a warning and starts the HTTP server with a `nil` client pointer rather than calling `log.Fatal`. When `GET /health/status` is called, the handler detects the `nil` client and immediately returns `UNAVAILABLE` for schema versions, keeping the Admin Panel responsive. Concurrently, a background goroutine attempts lazy re-initialization periodically (e.g., every minute). Once successful, `s.spannerClient` is populated for future requests.

### 2\. Kubernetes health probes 

To prevent cascading failures during external outages, `health-service` configures independent, lightweight `livenessProbe` and `readinessProbe` handlers that only verify its internal HTTP server status (only returning HTTP 200). By intentionally excluding downstream dependencies (like Spanner or core microservices), we guarantee external outages will never crash-loop the `health-service` pod.

## Rejected alternatives

### Add Spanner schema check to /healthz

We considered adding the Spanner schema match check directly to the existing `/healthz` liveness probe rather than introducing a dedicated `/readyz` probe.

* **Reason for rejection:** A failing liveness probe causes pods to restart until they eventually enter a crash loop. In the case of a schema version mismatch (e.g. during an image or schema upgrade), this would temporarily crash the deployments in Kubernetes rather than simply withholding http traffic.

### Dedicated intermediary services with publishNotReadyAddresses

We considered introducing dedicated secondary Kubernetes Services (e.g. `ess-health-service`) with `publishNotReadyAddresses` set to *true* specifically for `health-service` to communicate with the services if they fail the readiness probe. This would have allowed `health-service` to capture the error response from /readyz endpoint while ensuring that the primary services (e.g. routed via ingress) correctly withhold client traffic.

* **Reason for rejection:** Scrapped due to overengineering for the initial version. This solution would either introduce some edge cases regarding round-robin between healthy and unhealthy pods, or would require re-working it to call all the pods directly. For the initial version it was decided to put detailed error messages out of scope.

### Dynamic dependency discovery via health check endpoints

Instead of `health-service` directly pinging Redis or Spanner databases, we considered extending the existing `/healthz` or `/readyz` endpoints on core microservices to return a structured response with the state of each checked dependency, which `health-service` would dynamically scrape and merge.

* **Reason for rejection:** High risk during failure modes. Even if we would call pods directly to get around failing readiness probes, if a microservice liveness probe were to fail, it would enter `CrashLoopBackOff` and its HTTP probe endpoint becomes unreachable. Consequently, `health-service` would lose all visibility into whether the underlying Spanner database or Redis instance is actually healthy or failing.

### Permission validation via Google Groups

We considered using Google Groups (configured by the *admin\_group\_email* helm variable) to validate whether a caller has administrative privileges to view the Admin Panel and access health/config endpoints.

* **Reason for rejection:**  
  * **Lack of Project-Level Scoping:** Google Groups are organization-level resources in Google Workspace / Cloud Identity. They cannot be natively scoped to a specific GCP project. Managing separate Google Groups for every XMC project deployment creates significant administrative overhead.  
  * **Service Account Permission & Membership Friction:** To verify group memberships programmatically, the health-service's GCP Service Account would either require org-wide directory read permissions to see group memberships, or it would be required to be added directly as a member inside the specified admin group.

## Integration guide for future XMC core services

Future core XMC Services will need to follow these guidelines to integrate with the Health Service:

1. **Expose /readyz Endpoint:**  
   * Implement a standard HTTP /readyz endpoint for health checks by the Health Service.  
2. **/readyz logic:**  
   * The /readyz handler must verify the service is ready to serve traffic. Checks should include:  
     * Critical dependency connections (databases, other services).  
     * Internal state consistency.  
     * **\[If applicable\]** Database schema version validation (e.g., against SchemaMigrations).  
   * Return 200 OK if all checks pass. Otherwise, return a non-200 status (e.g., 503\) with an optional error message in the body.  
3. **Register with Health Service:**  
   * Update the Health Service to poll the new Core Service by adding its address (e.g., http://new-core-service:8080/readyz) to its monitored targets.  
4. **Expose Configuration via ConfigMaps:**  
   * Manage non-sensitive configurations to be displayed in the Admin Panel using Kubernetes ConfigMaps.  
   * Update the health-service Deployment manifest to mount the new service's ConfigMap(s) as volumes.

## Blockers

1. **Endpoint for configs:** blocked on migration of the configs to ConfigMaps  
2. **Returning health and configuration of custom components:** blocked on custom component system implementation  
3. **Production ready deployment of health-service:** blocked on Gateway/BFF initiative

## Future considerations

1. **Returning proper app version:** once we add semantic versioning for our BUILD images, we must make sure that XMC\_APP\_VERSION is injecting the proper value.  
2. **Granular health reporting and detailed error messages**: In the future, if administrators require granular per-pod health status or detailed error message, we can consider accessing each pod directly to get a response body of failing */readyz* check. For the initial version of Admin Panel this is out of scope.