# Amazon RDS for PostgreSQL.
#
# Satisfies the SAME contract as the gcp and azure modules: variables.tf and
# outputs.tf here are byte-identical to theirs.
#
# RDS does not offer TimescaleDB either, and as with the other two it does not
# matter: migration 000008 builds metric_point as a natively partitioned table
# when the extension is absent, and no application code changes.

terraform {
  # Pinned so a module cannot be planned by a Terraform old enough to
  # mis-handle it. Same value in every module, on every cloud.
  required_version = ">= 1.10"

  required_providers {
    aws    = { source = "hashicorp/aws", version = "~> 5.0" }
    random = { source = "hashicorp/random", version = "~> 3.6" }
  }
}

locals {
  sizes = {
    small  = "db.t4g.small"
    medium = "db.t4g.medium"
    large  = "db.m6g.large"
  }

  host = aws_db_instance.this.address
}

resource "random_password" "db" {
  length  = 32
  special = false
}

resource "aws_db_subnet_group" "this" {
  name = var.name
  # The aws network module emits its private subnet ids as a comma-joined string,
  # because network_id is a single string in the shared contract. Translating it
  # here is the price of keeping that contract identical across three clouds, and
  # it is a price worth paying.
  subnet_ids = split(",", var.network_id)
  tags       = var.tags
}

resource "aws_db_instance" "this" {
  identifier     = var.name
  engine         = "postgres"
  engine_version = var.postgres_version

  instance_class    = local.sizes[var.instance_size]
  allocated_storage = var.storage_gb
  storage_encrypted = true
  storage_type      = "gp3"

  db_name  = var.name
  username = var.name
  password = random_password.db.result

  db_subnet_group_name = aws_db_subnet_group.this.name
  publicly_accessible  = false
  multi_az             = var.high_availability

  backup_retention_period = var.backup_retention_days
  skip_final_snapshot     = true
  apply_immediately       = true

  tags = var.tags
}
