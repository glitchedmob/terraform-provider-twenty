terraform {
  required_providers {
    twenty = {
      source = "glitchedmob/twenty"
    }
  }
}

# Set TWENTY_EMAIL and TWENTY_PASSWORD in the environment.
provider "twenty" {
  endpoint = "https://twenty.example.com"
}

data "twenty_role" "member" {
  label = "Member"
}

resource "twenty_role" "triage" {
  label                       = "Support triage"
  can_be_assigned_to_users    = true
  can_read_all_object_records = true
  permission_flags            = []
}

# Exclude the operator and independent recovery administrator.
resource "twenty_workspace_member" "declared" {
  for_each = {
    "alice@example.com" = twenty_role.triage.id
    "bob@example.com"   = data.twenty_role.member.id
  }
  email   = each.key
  role_id = each.value
}
