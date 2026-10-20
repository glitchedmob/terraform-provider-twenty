resource "twenty_role" "triage" {
  label                       = "Support triage"
  description                 = "Read records and manage workspace invitations"
  can_read_all_object_records = true

  # Terraform owns the complete explicit set; [] clears only explicit flags.
  permission_flags = ["WORKSPACE_MEMBERS"]
}
