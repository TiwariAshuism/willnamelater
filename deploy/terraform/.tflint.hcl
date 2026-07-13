# Provider-aware linting. `terraform validate` only checks that the HCL parses and
# that references resolve; it does NOT know that google_compute_instance has no
# attribute called `machine_size`, or that an argument was deprecated two majors ago.
# tflint loads each provider's real schema and does.
#
# All three providers are enabled, because all three env stacks must stay plannable —
# a migration target you only lint on migration day is not a target.

plugin "terraform" {
  enabled = true
  preset  = "recommended"
}

plugin "google" {
  enabled = true
  version = "0.31.0"
  source  = "github.com/terraform-linters/tflint-ruleset-google"
}

plugin "azurerm" {
  enabled = true
  version = "0.27.0"
  source  = "github.com/terraform-linters/tflint-ruleset-azurerm"
}

plugin "aws" {
  enabled = true
  version = "0.37.0"
  source  = "github.com/terraform-linters/tflint-ruleset-aws"
}
