terraform {
  required_providers {
    twenty = {
      source = "glitchedmob/twenty"
    }
  }
}

# Inject credentials through the environment and use a local provider build.
provider "twenty" {
  endpoint = "https://twenty.example.com"
}

# Replace this UUID and match every existing role value before applying.
import {
  to = twenty_role.triage
  id = "11111111-1111-4111-8111-111111111111"
}

resource "twenty_role" "triage" {
  label       = "Support triage"
  description = null
  icon        = null

  can_be_assigned_to_users    = true
  can_be_assigned_to_agents   = false
  can_be_assigned_to_api_keys = false

  can_update_all_settings            = false
  can_access_all_tools               = false
  can_read_all_object_records        = true
  can_update_all_object_records      = false
  can_soft_delete_all_object_records = false
  can_destroy_all_object_records     = false

  permission_flags = ["WORKSPACE_MEMBERS"]
}
