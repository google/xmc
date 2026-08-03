# Networking Terraform Module

This directory contains the Terraform module for managing XManager on Cloud (XMC) GCP networking infrastructure.

---

## Files & Capabilities

- **`main.tf`**: Compute API enablement and static external IP address reservations for Actuator, ESS, and UI services.
- **`ui.tf`**: Configures Cloud Run Serverless Network Endpoint Group (NEG), External HTTPS Load Balancer, Managed SSL Certificates, and Identity-Aware Proxy (IAP) access policies.
- **`dns.tf`**: Configures Cloud DNS records and managed private zones for internal `.xmc.internal` service routing.
- **`metrics.tf`**: Configures VPC Peering and Layer 4 (L4) firewall rules between the XMC Application VPC and external Bring-Your-Own (BYO) Redpanda and ClickHouse VPCs.
- **`variables.tf`**: Defines input variables for networking components, IAP OAuth credentials, domain names, and metrics VPC peering parameters.
- **`outputs.tf`**: Exports static IP addresses, UI URLs, and VPC peering connection IDs.
- **`versions.tf`**: Provider requirements (`google >= 5.0.0`).

---

## Metrics Networking (`metrics.tf`)

The `metrics.tf` configuration manages cross-VPC connectivity and security boundaries for XMC's telemetry infrastructure (Redpanda & ClickHouse):

### 1. VPC Peering
- Bi-directional peering (`xmc-to-redpanda-peering`, `redpanda-to-xmc`) between the XMC Application VPC and external BYO Redpanda VPC.
- Bi-directional peering (`clickhouse-to-redpanda-peering`, `redpanda-to-clickhouse`) between external BYO ClickHouse VPC and external BYO Redpanda VPC.

### 2. Layer 4 Firewall Rules (`google_compute_firewall`)
- **Gateway -> Redpanda**: Priority 1000 ingress rule allowing TCP ports `9092` (Kafka API), `9094` (Kafka External/SSL), and `8081` (Schema Registry) from Gateway source CIDRs (`gateway_source_cidrs`).
- **ClickHouse -> Redpanda**: Priority 1000 ingress rule allowing TCP ports `9092`, `9094`, and `8081` from ClickHouse source CIDRs (`clickhouse_source_cidrs`).

---

## Key Variables

| Variable Name | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `enable_metrics_networking` | `bool` | `false` | Master toggle to enable metrics VPC peering and firewall rules. |
| `xmc_vpc_network_id` | `string` | `""` | ID or self-link of the XMC Application VPC network. |
| `byo_redpanda_vpc_id` | `string` | `""` | ID or self-link of the BYO Redpanda VPC network. |
| `byo_clickhouse_vpc_id` | `string` | `""` | ID or self-link of the BYO ClickHouse VPC network. |
| `gateway_source_cidrs` | `list(string)` | `[]` | Allowed CIDRs for Gateway service ingress to Redpanda. |
| `clickhouse_source_cidrs` | `list(string)` | `[]` | Allowed CIDRs for ClickHouse service ingress to Redpanda. |
