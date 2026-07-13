terraform {
  # Pinned so a module cannot be planned by a Terraform old enough to
  # mis-handle it. Same value in every module, on every cloud.
  required_version = ">= 1.10"

  required_providers {
    aws = { source = "hashicorp/aws", version = "~> 5.0" }
  }
}

locals {
  sizes = {
    small  = "t3.small"
    medium = "t3.medium"
    large  = "m6i.xlarge"
  }

  public_ip = aws_instance.this.public_ip
}

data "aws_ami" "ubuntu" {
  most_recent = true
  owners      = ["099720109477"] # Canonical

  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-amd64-server-*"]
  }
}

resource "aws_key_pair" "deploy" {
  key_name   = var.name
  public_key = var.ssh_public_key
  tags       = var.tags
}

# The network module comma-joins its private subnet ids into the contract's single
# string. The VM belongs on a public subnet, which is looked up from the VPC the
# private subnets belong to — the price of a contract that does not bend to AWS.
data "aws_subnet" "first_private" {
  id = split(",", var.network_id)[0]
}

data "aws_subnets" "public" {
  filter {
    name   = "vpc-id"
    values = [data.aws_subnet.first_private.vpc_id]
  }

  filter {
    name   = "tag:Name"
    values = ["*-public"]
  }
}

# trivy:ignore:AVD-AWS-0164 The VM has a public IP on purpose: Caddy terminates TLS on the
# box and there is no load balancer. At one node an LB is a second thing to pay for,
# configure, and migrate, and it would terminate the TLS that Caddy already terminates
# for free. Revisit at two nodes — see deploy/ARCHITECTURE.md.
resource "aws_instance" "this" {
  ami           = data.aws_ami.ubuntu.id
  instance_type = local.sizes[var.vm_size]

  subnet_id                   = data.aws_subnets.public.ids[0]
  vpc_security_group_ids      = [var.firewall_id]
  associate_public_ip_address = true

  key_name = aws_key_pair.deploy.key_name

  # IMDSv2, required — not optional, which is the AWS default and the reason this is a
  # finding rather than a preference.
  #
  # With IMDSv1 still accepted, ANY server-side request forgery in the application is a
  # credential-theft primitive: a single unvalidated outbound URL fetch reaches
  # 169.254.169.254 and returns the instance's IAM credentials. Requiring a session
  # token closes that, because SSRF can rarely issue the PUT that mints one.
  #
  # This application makes plenty of outbound requests it does not fully control
  # (connector APIs, webhooks, Gotenberg rendering caller-influenced HTML), so the
  # premise of that attack is not hypothetical here.
  metadata_options {
    http_endpoint               = "enabled"
    http_tokens                 = "required"
    http_put_response_hop_limit = 1 # a container cannot reach IMDS through the host
  }

  root_block_device {
    volume_size = var.disk_gb
    volume_type = "gp3"
    encrypted   = true
  }

  tags = merge(var.tags, { Name = var.name })

  lifecycle {
    # The disk holds Docker images and logs. Replacing the VM is a normal, cheap
    # operation — never something to protect against.
    ignore_changes = [ami]
  }
}
