# Local multi-cloud with floci

Run the whole cloud-migration story **offline** against [floci](https://floci.io)
(AWS/Azure/GCP local emulators) instead of a real cloud account. This exercises
the same per-cloud Terraform as `deploy/terraform/envs/prod-*`, proving the
"migration is a 4-line diff" claim without spending a cent.

## Prerequisites

- Docker Desktop running.
- Terraform (`>= 1.10`).

## 1. Start the emulators

```bash
cd deploy/floci
docker compose up -d          # aws:4566, azure:4577 (TLS), gcp:4588
```

The Azure emulator serves ARM metadata over HTTPS (the `azurerm` provider
requires it). Trust its self-signed CA once (no admin needed):

```bash
curl -s http://localhost:4577/_floci/tls-cert > keys/floci-az-ca.pem
certutil -user -addstore -f Root keys/floci-az-ca.pem     # Windows
# Linux/mac: install keys/floci-az-ca.pem into the system trust store,
#            or export ARM_CA_BUNDLE=$PWD/keys/floci-az-ca.pem
```

## 2. Deploy each cloud

The `deploy/terraform/envs/local-*` overlays are the `prod-*` stacks pointed at
floci: dummy credentials, provider endpoints on the local ports, **local state**
(no R2 backend), and the Cloudflare storage/DNS modules omitted (floci emulates
the three clouds, not Cloudflare).

```bash
cd deploy/terraform
terraform -chdir=envs/local-aws   init && terraform -chdir=envs/local-aws   apply -auto-approve
terraform -chdir=envs/local-azure init && terraform -chdir=envs/local-azure apply -auto-approve
terraform -chdir=envs/local-gcp   init && terraform -chdir=envs/local-gcp   plan
```

## 3. The migration test

The migration between clouds is the four `source =` lines on
network/database/cache/compute (plus the provider and, for Azure, a resource
group):

```bash
diff envs/prod-aws/main.tf envs/prod-azure/main.tf   # ← that diff IS the migration
```

The cutover lifecycle, run locally: stand up the target cloud, then decommission
the source cloud.

```bash
terraform -chdir=envs/local-azure apply -auto-approve   # target cloud up
terraform -chdir=envs/local-aws   destroy -auto-approve  # source cloud torn down
```

## What actually runs on floci (measured)

| Cloud | Result |
|---|---|
| **AWS** (`local-aws`) | **Full apply.** VPC, subnets, IGW, route tables, security group, EC2 instance, RDS Postgres, key pair — all created (floci is LocalStack-based, and the `aws` provider has first-class endpoint overrides). |
| **Azure** (`local-azure`) | **Network + compute apply** (resource group, vnet, subnets, NSG, public IP, NIC, Linux VM). The `metadata_host` redirect + trusted CA let `azurerm` talk to floci-az. The PostgreSQL Flexible Server is *created* by floci but answered with HTTP 201 where the provider expects the async shape, so it is excluded from the local overlay (documented in `local-azure/main.tf`). |
| **GCP** (`local-gcp`) | **`init` + `plan` only.** The `google` provider connects via `*_custom_endpoint`, but floci-gcp's compute REST surface returns 405 on resource creation — an emulator-coverage gap, not an IaC problem. |

### floci coverage gaps hit (not IaC bugs)
- **AWS ElastiCache**: `CreateCacheSubnetGroup` → `UnsupportedOperation` (managed
  Redis isn't emulated), so the **cache module is omitted from every local
  overlay**. `prod-*` still provisions it; `plan` validates it.
- **Azure PostgreSQL Flexible Server**: created, but wrong HTTP status → provider
  error. Excluded from `local-azure`.
- **GCP compute**: creation not implemented (405).

## Teardown

```bash
terraform -chdir=envs/local-aws   destroy -auto-approve
terraform -chdir=envs/local-azure destroy -auto-approve
cd deploy/floci && docker compose down
```

State files, provider plugins, and the CA/keys under these directories are local
artifacts and are git-ignored.
