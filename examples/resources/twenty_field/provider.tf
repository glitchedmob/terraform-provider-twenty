terraform {
  required_providers {
    twenty = {
      source = "glitchedmob/twenty"
    }
  }
}

# Unreleased. Use a local build with Terraform CLI dev_overrides.
# Inject TWENTY_EMAIL and TWENTY_PASSWORD outside configuration.
provider "twenty" {
  endpoint = "https://twenty.example.com"
}
