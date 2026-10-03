terraform {
  required_providers {
    twenty = {
      source = "glitchedmob/twenty"
    }
  }
}

# Unreleased. Build locally and use Terraform CLI dev_overrides.
# Inject TWENTY_EMAIL and TWENTY_PASSWORD outside checked-in configuration.
provider "twenty" {
  endpoint = "https://twenty.example.com"
}
