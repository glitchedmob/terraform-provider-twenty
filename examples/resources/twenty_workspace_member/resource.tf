data "twenty_role" "member" {
  label = "Member"
}

# Keep the operator and recovery administrator out of this set.
resource "twenty_workspace_member" "declared" {
  for_each = toset(["alice@example.com", "bob@example.com"])
  email    = each.key
  role_id  = data.twenty_role.member.id
}
