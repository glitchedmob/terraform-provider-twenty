data "twenty_workspace" "current" {}

output "workspace_id" {
  value = data.twenty_workspace.current.id
}

output "default_role_id" {
  value = data.twenty_workspace.current.default_role_id
}
