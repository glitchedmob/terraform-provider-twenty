terraform {
  required_providers {
    twenty = {
      source = "glitchedmob/twenty"
    }
  }
}

# Unreleased. Use a locally built provider with Terraform CLI dev_overrides.
# Inject TWENTY_EMAIL and TWENTY_PASSWORD through your secret-injection process.
provider "twenty" {
  endpoint = "https://twenty.example.com"
}
