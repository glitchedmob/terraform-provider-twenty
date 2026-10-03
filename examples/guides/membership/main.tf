terraform {
  required_version = ">= 1.10"
  required_providers {
    twenty = {
      source = "glitchedmob/twenty"
    }
  }
}

# Unreleased provider. Use a locally built binary and CLI dev_overrides.
variable "twenty_endpoint" { type = string }
variable "twenty_email" {
  type      = string
  sensitive = true
  ephemeral = true
}
variable "twenty_password" {
  type      = string
  sensitive = true
  ephemeral = true
}
provider "twenty" {
  endpoint = var.twenty_endpoint
  email    = var.twenty_email
  password = var.twenty_password
}

# Role deletion needs ROLES, APPLICATIONS, and WORKSPACE_MEMBERS on the caller.
# External invitation references, including expired rows, block role deletion.
# Provision the verified operator and independent recovery admin outside Terraform.
# Omit both emails here. New emails receive invitations, not passwords.
variable "declared_members" {
  type = map(string)
  default = {
    "alice@example.com" = "triage"
    "bob@example.com"   = "member"
  }
  validation {
    condition = alltrue([
      for email, role in var.declared_members :
      email == lower(trimspace(email)) && contains(["triage", "member"], role)
    ])
    error_message = "Declare lowercase emails and select triage or member."
  }
}

data "twenty_workspace" "current" {}
data "twenty_role" "member" {
  label = "Member"
}
resource "twenty_role" "triage" {
  label                       = "Support triage"
  can_be_assigned_to_users    = true
  can_read_all_object_records = true
  permission_flags            = []
}
locals {
  member_role_ids = {
    triage = twenty_role.triage.id
    member = data.twenty_role.member.id
  }
}
resource "twenty_workspace_member" "declared" {
  for_each = var.declared_members
  email    = each.key
  role_id  = local.member_role_ids[each.value]
}
output "workspace_id" {
  value = data.twenty_workspace.current.id
}
