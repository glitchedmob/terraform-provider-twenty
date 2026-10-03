// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/oapi-codegen/nullable"

	"github.com/glitchedmob/terraform-provider-twenty/internal/acceptance"
	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
)

// One disposable stack is reused across session, data source, and IAM resource tests.
// No managed resources alter the operator or its independent recovery admin.
func TestAccSessionAndRole(t *testing.T) {
	fixture := acceptance.Bootstrap(t, acceptance.Start(t))
	// Do not let ambient Terraform debug settings persist secrets, state, or plans.
	for _, name := range []string{"TF_LOG", "TF_LOG_CORE", "TF_LOG_PROVIDER", "TF_LOG_SDK", "TF_LOG_SDK_HELPER_RESOURCE", "TF_LOG_PATH", "TF_LOG_PATH_MASK", "TF_ACC_LOG", "TF_ACC_LOG_PATH", "TF_ACC_PERSIST_WORKING_DIR"} {
		t.Setenv(name, "")
	}
	t.Setenv("TWENTY_ENDPOINT", fixture.Stack.Endpoint)
	t.Setenv("TWENTY_EMAIL", fixture.Operator.Email)
	t.Setenv("TWENTY_PASSWORD", fixture.Operator.Password)

	t.Run("login_identity_and_forced_renewal", func(t *testing.T) {
		session, err := client.NewSession(t.Context(), fixture.Stack.Endpoint, fixture.Operator.Email, fixture.Operator.Password, true)
		if err != nil {
			t.Fatalf("authenticate disposable operator: %s", err)
		}
		before := session.Identity()
		if before.UserID != fixture.Operator.UserID || before.WorkspaceMemberID != fixture.Operator.MemberID || before.WorkspaceID != fixture.WorkspaceID || before.Email != fixture.Operator.Email {
			t.Fatal("password session identified a different workspace or member")
		}
		if err := session.Refresh(t.Context()); err != nil {
			t.Fatalf("force legitimate server token renewal: %s", err)
		}
		if err := session.Refresh(t.Context()); err != nil {
			t.Fatalf("renew again using the rotated refresh token: %s", err)
		}
		roles, err := session.GetRoles(t.Context())
		if err != nil || len(roles.GetRoles) == 0 {
			t.Fatal("role query failed after two server-issued renewals")
		}
		identity, err := client.CurrentUser(t.Context(), session.Client())
		if err != nil || identity.CurrentUser.Id != before.UserID {
			t.Fatal("renewed session did not preserve authenticated identity")
		}
	})

	t.Run("role_id_and_label_environment_only", func(t *testing.T) {
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: acceptanceFactories(),
			Steps: []resource.TestStep{
				{
					Config: roleAcceptanceConfig("role_id", fixture.AdminRole.Id),
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckResourceAttr("data.twenty_role.test", "id", fixture.AdminRole.Id),
						resource.TestCheckResourceAttr("data.twenty_role.test", "label", fixture.AdminRole.Label),
						resource.TestCheckResourceAttr("data.twenty_role.test", "can_update_all_settings", "true"),
						resource.TestCheckResourceAttr("data.twenty_role.test", "can_be_assigned_to_users", "true"),
					),
				},
				{
					Config: roleAcceptanceConfig("label", fixture.AdminRole.Label),
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckResourceAttr("data.twenty_role.test", "id", fixture.AdminRole.Id),
						resource.TestCheckResourceAttr("data.twenty_role.test", "label", fixture.AdminRole.Label),
					),
				},
			},
		})
	})

	t.Run("workspace_current_and_count_refresh", func(t *testing.T) { testAccWorkspaceDataSource(t, fixture) })

	t.Run("role_resource", func(t *testing.T) { testAccRoleResource(t, fixture) })

	t.Run("workspace_member_resource", func(t *testing.T) { testAccWorkspaceMemberResource(t, fixture) })

	t.Run("missing_role", func(t *testing.T) {
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: acceptanceFactories(),
			Steps: []resource.TestStep{{
				Config:      roleAcceptanceConfig("label", "does-not-exist-in-disposable-workspace"),
				ExpectError: regexp.MustCompile("Twenty Role Not Found"),
			}},
		})
	})

	t.Run("wrong_password", func(t *testing.T) {
		t.Setenv("TWENTY_PASSWORD", fixture.Operator.Password+"incorrect")
		if _, err := client.NewSession(t.Context(), fixture.Stack.Endpoint, fixture.Operator.Email, fixture.Operator.Password+"incorrect", true); err == nil {
			t.Fatal("server accepted an incorrect disposable password")
		}
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: acceptanceFactories(),
			Steps: []resource.TestStep{{
				Config:      roleAcceptanceConfig("role_id", fixture.AdminRole.Id),
				ExpectError: regexp.MustCompile("Unable to Configure Twenty Provider"),
			}},
		})
	})

	t.Run("denied_roles_permission", func(t *testing.T) {
		created, err := client.CreateOneRole(t.Context(), fixture.Operator.API, client.CreateRoleInput{
			Label:                "Acceptance without settings permissions",
			CanUpdateAllSettings: nullable.NewNullableWithValue(false),
			CanBeAssignedToUsers: nullable.NewNullableWithValue(true),
		})
		if err != nil {
			t.Fatalf("create disposable restricted role: %s", err)
		}
		roleID := created.CreateOneRole.Id
		if roleID == "" || roleID == fixture.AdminRole.Id {
			t.Fatal("restricted role has invalid identity")
		}
		if _, err := client.UpsertPermissionFlags(t.Context(), fixture.Operator.API, client.UpsertPermissionFlagsInput{
			RoleId: roleID, PermissionFlagKeys: []string{},
		}); err != nil {
			t.Fatalf("clear restricted role settings flags: %s", err)
		}
		// A third account, not the operator or recovery administrator.
		restricted := fixture.InviteAccount(t, "restricted", fixture.AdminRole.Id)
		if restricted.MemberID == fixture.Operator.MemberID || restricted.MemberID == fixture.Recovery.MemberID {
			t.Fatal("refuse to reassign a protected bootstrap member")
		}
		if _, err := client.UpdateWorkspaceMemberRole(t.Context(), fixture.Operator.API, restricted.MemberID, roleID); err != nil {
			t.Fatalf("assign disposable member's restricted role: %s", err)
		}
		session, err := client.NewSession(t.Context(), fixture.Stack.Endpoint, restricted.Email, restricted.Password, true)
		if err != nil {
			t.Fatalf("authenticate verified restricted member: %s", err)
		}
		if _, err := session.GetRoles(t.Context()); err == nil {
			t.Fatal("member without ROLES permission unexpectedly read roles")
		}
		t.Run("terraform", func(t *testing.T) {
			t.Setenv("TWENTY_EMAIL", restricted.Email)
			t.Setenv("TWENTY_PASSWORD", restricted.Password)
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: acceptanceFactories(),
				Steps: []resource.TestStep{{
					Config:      roleAcceptanceConfig("role_id", fixture.AdminRole.Id),
					ExpectError: regexp.MustCompile("Unable to Read Twenty Roles"),
				}, {
					Config: `provider "twenty" { allow_insecure_http = true }
resource "twenty_role" "denied" {
 label = "Denied create"
 permission_flags = []
}`,
					ExpectError: regexp.MustCompile("Unable to Create Twenty Role"),
				}, {
					Config: fmt.Sprintf(`provider "twenty" { allow_insecure_http = true }
import {
 to = twenty_role.denied
 id = %q
}
resource "twenty_role" "denied" {
 label = "Denied import"
 permission_flags = []
}`, fixture.AdminRole.Id),
					PlanOnly:    true,
					ExpectError: regexp.MustCompile("Unable to Read Twenty Role"),
				}},
			})
		})
		t.Run("workspace_without_settings_permissions", func(t *testing.T) {
			t.Setenv("TWENTY_EMAIL", restricted.Email)
			t.Setenv("TWENTY_PASSWORD", restricted.Password)
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: acceptanceFactories(),
				Steps: []resource.TestStep{{Config: workspaceAcceptanceConfig, Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.twenty_workspace.current", "id", fixture.WorkspaceID),
					resource.TestCheckResourceAttr("data.twenty_workspace.current", "activation_status", "ACTIVE"),
					resource.TestCheckResourceAttrSet("data.twenty_workspace.current", "default_role_id"),
					resource.TestCheckResourceAttrSet("data.twenty_workspace.current", "workspace_members_count"),
				)}},
			})
		})
		t.Run("settings_flags_are_distinct", func(t *testing.T) {
			if _, err := client.UpsertPermissionFlags(t.Context(), fixture.Operator.API, client.UpsertPermissionFlagsInput{RoleId: roleID, PermissionFlagKeys: []string{"ROLES"}}); err != nil {
				t.Fatal("grant ROLES to disposable restricted member")
			}
			rolesOnly, err := client.NewSession(t.Context(), fixture.Stack.Endpoint, restricted.Email, restricted.Password, true)
			if err != nil {
				t.Fatal("authenticate ROLES-only member")
			}
			if _, err := rolesOnly.GetRoles(t.Context()); err != nil {
				t.Fatal("ROLES must permit role reads")
			}
			t.Run("roles_only_membership_denied", func(t *testing.T) {
				t.Setenv("TWENTY_EMAIL", restricted.Email)
				t.Setenv("TWENTY_PASSWORD", restricted.Password)
				testAccMemberPermissionDenied(t, fixture)
			})
			t.Run("roles_only_create_update_and_application_guard", func(t *testing.T) {
				t.Setenv("TWENTY_EMAIL", restricted.Email)
				t.Setenv("TWENTY_PASSWORD", restricted.Password)
				resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: acceptanceFactories(), Steps: []resource.TestStep{
					{Config: `provider "twenty" { allow_insecure_http = true }
resource "twenty_role" "scoped" {
 label = "ROLES-only created role"
 permission_flags = ["ROLES"]
}`},
					{Config: `provider "twenty" { allow_insecure_http = true }
resource "twenty_role" "scoped" {
 label = "ROLES-only updated role"
 permission_flags = []
}`},
					{Config: `provider "twenty" { allow_insecure_http = true }
resource "twenty_role" "scoped" {
 label = "ROLES-only updated role"
 permission_flags = []
}`, Destroy: true, ExpectError: regexp.MustCompile("Unable to Delete Twenty Role")},
					{PreConfig: func() {
						if _, err := client.UpsertPermissionFlags(t.Context(), fixture.Operator.API, client.UpsertPermissionFlagsInput{RoleId: roleID, PermissionFlagKeys: []string{"ROLES", "APPLICATIONS"}}); err != nil {
							t.Fatal("grant application assignment visibility for deletion")
						}
					}, Config: `provider "twenty" { allow_insecure_http = true }
resource "twenty_role" "scoped" {
 label = "ROLES-only updated role"
 permission_flags = []
}`, Destroy: true},
				}})
			})
			if _, err := client.SendInvitations(t.Context(), rolesOnly.Client(), []string{"not-invited@example.test"}, nullable.NewNullableWithValue(roleID)); err == nil {
				t.Fatal("ROLES unexpectedly granted WORKSPACE_MEMBERS")
			}
			if _, err := client.UpsertPermissionFlags(t.Context(), fixture.Operator.API, client.UpsertPermissionFlagsInput{RoleId: roleID, PermissionFlagKeys: []string{"WORKSPACE_MEMBERS"}}); err != nil {
				t.Fatal("replace settings flags")
			}
			membersOnly, err := client.NewSession(t.Context(), fixture.Stack.Endpoint, restricted.Email, restricted.Password, true)
			if err != nil {
				t.Fatal("authenticate WORKSPACE_MEMBERS-only member")
			}
			if _, err := membersOnly.GetRoles(t.Context()); err == nil {
				t.Fatal("WORKSPACE_MEMBERS unexpectedly granted ROLES")
			}
			t.Run("members_only_membership_denied", func(t *testing.T) {
				t.Setenv("TWENTY_EMAIL", restricted.Email)
				t.Setenv("TWENTY_PASSWORD", restricted.Password)
				testAccMemberPermissionDenied(t, fixture)
			})
		})
	})

	t.Run("recovery_administrator_preserved", func(t *testing.T) {
		for _, account := range []*acceptance.Account{fixture.Operator, fixture.Recovery} {
			session, err := client.NewSession(t.Context(), fixture.Stack.Endpoint, account.Email, account.Password, true)
			if err != nil {
				t.Fatalf("authenticate protected disposable administrator: %s", err)
			}
			if session.Identity().WorkspaceMemberID != account.MemberID {
				t.Fatal("protected administrator identity changed")
			}
			if _, err := session.GetRoles(t.Context()); err != nil {
				t.Fatal("protected administrator lost settings permissions")
			}
			user, err := client.CurrentUser(t.Context(), session.Client())
			if err != nil {
				t.Fatal("read protected administrator assignment")
			}
			own, err := user.CurrentUser.WorkspaceMember.Get()
			if err != nil || len(own.Roles) != 1 || own.Roles[0].Id != fixture.AdminRole.Id || own.UserId != account.UserID {
				t.Fatal("unmanaged administrator assignment changed")
			}
		}
	})
}

func acceptanceFactories() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"twenty": providerserver.NewProtocol6WithError(New("acceptance")()),
	}
}

// Credentials never appear in Terraform configuration, state, or diagnostics.
func roleAcceptanceConfig(selector, value string) string {
	return fmt.Sprintf(`
provider "twenty" {
  allow_insecure_http = true
}
data "twenty_role" "test" {
  %s = %q
}
`, selector, value)
}
