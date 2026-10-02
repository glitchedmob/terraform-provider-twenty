terraform {
  required_providers {
    twenty = {
      source = "glitchedmob/twenty"
    }
  }
}

# Illustrative only. The scaffold has no release, authentication, or resources.
# Build locally and use a Terraform CLI dev_overrides entry to load the binary.
# Future authentication will use TWENTY_EMAIL and TWENTY_PASSWORD.
provider "twenty" {
  endpoint = "http://localhost:3000"
}
