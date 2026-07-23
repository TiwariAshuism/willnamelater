terraform {
  # Pinned so a module cannot be planned by a Terraform old enough to
  # mis-handle it. Same value in every module, on every cloud.
  required_version = ">= 1.10"

  required_providers {
    aws = { source = "hashicorp/aws", version = "~> 5.0" }
  }
}

locals {
  # AWS attaches databases to a SUBNET GROUP — a list. The shared contract emits a
  # single string, so the private subnet ids are comma-joined here and split apart
  # again in the database and cache modules. Bending the contract to AWS's shape
  # would mean it no longer describes GCP or Azure, so the translation lives here,
  # which is the only place allowed to know it is AWS.
  network_id  = join(",", aws_subnet.private[*].id)
  firewall_id = aws_security_group.this.id
}

data "aws_availability_zones" "available" {
  state = "available"
}

resource "aws_vpc" "this" {
  cidr_block           = var.cidr
  enable_dns_support   = true
  enable_dns_hostnames = true
  tags                 = merge(var.tags, { Name = var.name })
}

resource "aws_subnet" "public" {
  vpc_id                  = aws_vpc.this.id
  cidr_block              = cidrsubnet(var.cidr, 8, 1)
  availability_zone       = data.aws_availability_zones.available.names[0]
  map_public_ip_on_launch = true
  tags                    = merge(var.tags, { Name = "${var.name}-public" })
}

# Two, in different AZs: RDS requires a subnet group spanning at least two.
resource "aws_subnet" "private" {
  count             = 2
  vpc_id            = aws_vpc.this.id
  cidr_block        = cidrsubnet(var.cidr, 8, count.index + 10)
  availability_zone = data.aws_availability_zones.available.names[count.index]
  tags              = merge(var.tags, { Name = "${var.name}-private-${count.index}" })
}

resource "aws_internet_gateway" "this" {
  vpc_id = aws_vpc.this.id
  tags   = var.tags
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.this.id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.this.id
  }

  tags = var.tags
}

resource "aws_route_table_association" "public" {
  subnet_id      = aws_subnet.public.id
  route_table_id = aws_route_table.public.id
}

# The three exceptions below are deliberate, and each one is a decision rather than an
# oversight. They are annotated so the security scan can be ENFORCED in CI (exit-code 1)
# instead of being a report nobody reads — an unexplained finding and an accepted risk
# look identical in a scanner's output, and only one of them is fine.
#
# trivy:ignore:AVD-AWS-0107 Unrestricted ingress on 80/443. This is a public web server.
#   That is what it is for.
# trivy:ignore:AVD-AWS-0104 Unrestricted egress. The VM must reach GHCR, the managed
#   Postgres and Redis, Cloudflare R2, Anthropic, Razorpay, the SMTP relay, and every
#   creator platform's API. An egress allowlist over that set would be a list of CIDRs
#   owned by other people, which changes without telling us and fails closed at 3am.
resource "aws_security_group" "this" {
  name   = var.name
  vpc_id = aws_vpc.this.id
  tags   = var.tags

  ingress {
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  ingress {
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  # SSH defaults to open, and the scanner is right to dislike it. See
  # variables.tf:ssh_source_cidrs — the reason is that CI deploys from GitHub's runners,
  # whose addresses are not stable, and the mitigation is that the key on the far end is
  # pinned to a forced command (deploy/scripts/ssh-entrypoint.sh) so it can ask for a
  # deploy or a rollback and NOTHING else. It cannot get a shell.
  #
  # That is a mitigation, not a fix. Narrow this the day you have a bastion or a
  # self-hosted runner.
  ingress {
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = var.ssh_source_cidrs
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}
