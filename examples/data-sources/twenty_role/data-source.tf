data "twenty_role" "existing" {
  label = "Admin"
}

# For UUID lookup, replace label with role_id.
# role_id = "11111111-1111-4111-8111-111111111111"
output "role_id" {
  value = data.twenty_role.existing.id
}
