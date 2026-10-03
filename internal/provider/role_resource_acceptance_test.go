// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/glitchedmob/terraform-provider-twenty/internal/acceptance"
	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/oapi-codegen/nullable"
)

func testAccRoleResource(t *testing.T, fixture *acceptance.Fixture) {
	const address = "twenty_role.test"
	var id string
	config := func(label, description, icon, flags string, grants bool) string {
		return fmt.Sprintf(`
provider "twenty" { allow_insecure_http = true }
resource "twenty_role" "test" {
 label = %q
 description = %s
 icon = %s
 permission_flags = %s
 can_be_assigned_to_users = true
 can_be_assigned_to_agents = %t
 can_be_assigned_to_api_keys = %t
 can_update_all_settings = %t
 can_access_all_tools = %t
 can_read_all_object_records = %t
 can_update_all_object_records = %t
 can_soft_delete_all_object_records = %t
 can_destroy_all_object_records = %t
}
`, label, description, icon, flags, grants, grants, grants, grants, grants, grants, grants, grants)
	}
	capture := func(s *terraform.State) error {
		id = s.RootModule().Resources[address].Primary.ID
		if !validRoleUUID(id) {
			return fmt.Errorf("resource did not persist a native UUID")
		}
		return nil
	}
	update := resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)}}
	empty := resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}
	initial := config("Terraform acceptance role", `"Test description"`, `"IconUser"`, `["ROLES", "WORKSPACE_MEMBERS"]`, false)
	granted := config("Terraform renamed role", `""`, `""`, `["ROLES"]`, true)
	cleared := config("Terraform renamed role", "null", "null", "[]", false)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acceptanceFactories(),
		CheckDestroy: func(_ *terraform.State) error {
			role, err := readManagedRole(t.Context(), fixture.Operator.API, id)
			if err != nil {
				return err
			}
			if role != nil {
				return fmt.Errorf("managed role survived Terraform destroy")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: initial, Check: resource.ComposeAggregateTestCheckFunc(capture, resource.TestCheckResourceAttr(address, "permission_flags.#", "2"), resource.TestCheckResourceAttr(address, "can_read_all_object_records", "false"), resource.TestCheckResourceAttr(address, "description", "Test description"))},
			{ResourceName: address, ImportState: true, ImportStateVerify: true},
			{Config: granted, ConfigPlanChecks: update, Check: resource.ComposeAggregateTestCheckFunc(resource.TestCheckResourceAttr(address, "permission_flags.#", "1"), resource.TestCheckResourceAttr(address, "can_update_all_settings", "true"), resource.TestCheckResourceAttr(address, "can_access_all_tools", "true"), resource.TestCheckResourceAttr(address, "can_read_all_object_records", "true"), resource.TestCheckResourceAttr(address, "can_update_all_object_records", "true"), resource.TestCheckResourceAttr(address, "can_soft_delete_all_object_records", "true"), resource.TestCheckResourceAttr(address, "can_destroy_all_object_records", "true"), resource.TestCheckResourceAttr(address, "description", ""), resource.TestCheckResourceAttr(address, "icon", ""))},
			{ResourceName: address, ImportState: true, ImportStateVerify: true},
			{Config: cleared, ConfigPlanChecks: update, Check: resource.ComposeAggregateTestCheckFunc(resource.TestCheckResourceAttr(address, "permission_flags.#", "0"), resource.TestCheckNoResourceAttr(address, "description"), resource.TestCheckNoResourceAttr(address, "icon"), resource.TestCheckResourceAttr(address, "can_read_all_object_records", "false"), resource.TestCheckResourceAttr(address, "can_update_all_settings", "false"))},
			{Config: cleared, ConfigPlanChecks: empty},
			{PreConfig: func() {
				if _, err := client.UpdateOneRole(t.Context(), fixture.Operator.API, client.UpdateRoleInput{Id: id, Update: client.UpdateRolePayload{Description: nullable.NewNullableWithValue("Out of band"), CanReadAllObjectRecords: nullable.NewNullableWithValue(true)}}); err != nil {
					t.Fatal("out-of-band role update failed")
				}
				if _, err := client.UpsertPermissionFlags(t.Context(), fixture.Operator.API, client.UpsertPermissionFlagsInput{RoleId: id, PermissionFlagKeys: []string{"ROLES"}}); err != nil {
					t.Fatal("out-of-band flag update failed")
				}
			}, Config: cleared, ConfigPlanChecks: update},
			{Config: cleared, ConfigPlanChecks: empty},
			{PreConfig: func() {
				if _, err := client.DeleteOneRole(t.Context(), fixture.Operator.API, id); err != nil {
					t.Fatal("out-of-band deletion failed")
				}
			}, Config: cleared, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionCreate)}}, Check: capture},
		},
	})
	t.Run("safe_defaults", func(t *testing.T) {
		resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: acceptanceFactories(), Steps: []resource.TestStep{{
			Config: `provider "twenty" { allow_insecure_http = true }
resource "twenty_role" "test" {
 label = "Terraform default role settings"
 permission_flags = []
}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(address, "can_be_assigned_to_users", "true"),
				resource.TestCheckResourceAttr(address, "can_be_assigned_to_agents", "false"),
				resource.TestCheckResourceAttr(address, "can_be_assigned_to_api_keys", "false"),
				resource.TestCheckResourceAttr(address, "can_update_all_settings", "false"),
				resource.TestCheckResourceAttr(address, "can_access_all_tools", "false"),
				resource.TestCheckResourceAttr(address, "can_read_all_object_records", "false"),
				resource.TestCheckResourceAttr(address, "can_update_all_object_records", "false"),
				resource.TestCheckResourceAttr(address, "can_soft_delete_all_object_records", "false"),
				resource.TestCheckResourceAttr(address, "can_destroy_all_object_records", "false"),
				resource.TestCheckResourceAttr(address, "permission_flags.#", "0"),
			),
		}, {ResourceName: address, ImportState: true, ImportStateVerify: true}}})
	})
	t.Run("missing_import", func(t *testing.T) {
		resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: acceptanceFactories(), Steps: []resource.TestStep{{Config: cleared}, {ResourceName: address, ImportState: true, ImportStateId: roleTestID, ExpectError: regexp.MustCompile("Cannot import non-existent remote object")}}})
	})
	t.Run("assigned_role_deletion_fails_closed", func(t *testing.T) {
		var assigned *acceptance.Account
		resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: acceptanceFactories(), Steps: []resource.TestStep{
			{Config: initial, Check: capture},
			{PreConfig: func() { assigned = fixture.InviteAccount(t, "assigned-role", id) }, Config: initial, Destroy: true, ExpectError: regexp.MustCompile("Unable to Delete Twenty Role")},
			{PreConfig: func() {
				user, err := client.CurrentUser(t.Context(), assigned.API)
				if err != nil {
					t.Fatal("read assigned member after refused deletion")
				}
				own, err := user.CurrentUser.WorkspaceMember.Get()
				if err != nil || len(own.Roles) != 1 || own.Roles[0].Id != id {
					t.Fatal("refused deletion rebound the assigned member")
				}
				if _, err := client.UpdateWorkspaceMemberRole(t.Context(), fixture.Operator.API, assigned.MemberID, fixture.AdminRole.Id); err != nil {
					t.Fatal("release test-only role assignment")
				}
			}, Config: initial, Destroy: true},
		}})
	})
	t.Run("bootstrap_and_default_roles_are_protected", func(t *testing.T) {
		session, err := client.NewSession(t.Context(), fixture.Stack.Endpoint, fixture.Operator.Email, fixture.Operator.Password, true)
		if err != nil {
			t.Fatal("authenticate disposable role operator")
		}
		snapshot, err := readRoleSafety(t.Context(), session.Client(), session.Identity())
		if err != nil {
			t.Fatal("read fresh role safety data")
		}
		r := &roleResource{}
		var configured frameworkresource.ConfigureResponse
		r.Configure(t.Context(), frameworkresource.ConfigureRequest{ProviderData: &ClientData{Client: session}}, &configured)
		for _, roleID := range []string{fixture.AdminRole.Id, snapshot.defaultRoleID} {
			role := snapshot.role(roleID)
			var model roleResourceModel
			if role == nil || model.fromAPI(t.Context(), *role) != nil {
				t.Fatal("read protected role")
			}
			state := roleResourceState(t, model)
			resp := frameworkresource.DeleteResponse{State: state}
			r.Delete(t.Context(), frameworkresource.DeleteRequest{State: state}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("provider allowed protected role deletion")
			}
		}
	})
	t.Run("server_rejects_write_without_read", func(t *testing.T) {
		_, err := client.CreateOneRole(t.Context(), fixture.Operator.API, client.CreateRoleInput{Label: "Invalid writing role", CanReadAllObjectRecords: nullable.NewNullableWithValue(false), CanUpdateAllObjectRecords: nullable.NewNullableWithValue(true)})
		if err == nil {
			t.Fatal("server accepted a global write grant without read")
		}
	})
}
