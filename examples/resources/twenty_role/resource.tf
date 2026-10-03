# The caller needs ROLES; deletion also needs APPLICATIONS and WORKSPACE_MEMBERS.
# Stored invitation references, including expired rows, block role deletion.
resource "twenty_role" "triage" {
  label       = "Support triage"
  description = "Read records and manage workspace invitations"
  icon        = "IconUser"

  can_be_assigned_to_users    = true
  can_be_assigned_to_agents   = false
  can_be_assigned_to_api_keys = false

  can_update_all_settings            = false
  can_access_all_tools               = false
  can_read_all_object_records        = true
  can_update_all_object_records      = false
  can_soft_delete_all_object_records = false
  can_destroy_all_object_records     = false

  # Terraform owns the entire explicit set. [] clears all explicit flags.
  permission_flags = ["WORKSPACE_MEMBERS"]
}
