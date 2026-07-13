# ElastiCache for Redis.
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
  host     = aws_elasticache_replication_group.this.primary_endpoint_address
  port     = 6379
  password = random_password.auth.result
}

resource "random_password" "auth" {
  length  = 48
  special = false # ElastiCache AUTH tokens reject most punctuation
}

resource "aws_elasticache_subnet_group" "this" {
  name       = var.name
  subnet_ids = split(",", var.network_id)
  tags       = var.tags
}

resource "aws_elasticache_replication_group" "this" {
  replication_group_id = var.name
  description          = "InfluAudit cache + asynq broker"

  engine         = "redis"
  engine_version = "7.1"
  node_type      = var.memory_gb <= 1 ? "cache.t4g.small" : "cache.t4g.medium"

  num_cache_clusters         = var.high_availability ? 2 : 1
  automatic_failover_enabled = var.high_availability

  subnet_group_name = aws_elasticache_subnet_group.this.name

  # in-transit encryption is what makes redis.tls=true correct here too.
  transit_encryption_enabled = true
  at_rest_encryption_enabled = true
  auth_token                 = random_password.auth.result

  tags = var.tags
}
