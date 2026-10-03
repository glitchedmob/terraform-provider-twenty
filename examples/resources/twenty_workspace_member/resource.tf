# Provider authentication is configured separately. Never manage the operator.
data "twenty_workspace" "current" {}
data "twenty_role" "member" {
  label = "Member"
}

resource "twenty_workspace_member" "declared" {
  for_each = toset(["alice@example.com", "bob@example.com"])
  email    = each.key
  role_id  = data.twenty_role.member.id
}

# workspace_id is computed and must match data.twenty_workspace.current.id.
# Existing access requires import before apply.
