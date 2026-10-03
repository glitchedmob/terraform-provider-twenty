// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/glitchedmob/terraform-provider-twenty/internal/acceptance"
	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
	"github.com/google/uuid"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/oapi-codegen/nullable"
)

func memberAcceptanceConfig(email, roleID string) string {
	return fmt.Sprintf(`provider "twenty" { allow_insecure_http = true }
resource "twenty_workspace_member" "test" {
 email = %q
 role_id = %q
}
`, email, roleID)
}
func testAccWorkspaceMemberResource(t *testing.T, fixture *acceptance.Fixture) {
	const address = "twenty_workspace_member.test"
	roles := make([]string, 2)
	for i := range roles {
		created, err := client.CreateOneRole(t.Context(), fixture.Operator.API, client.CreateRoleInput{Label: fmt.Sprintf("Member lifecycle role %d", i), CanBeAssignedToUsers: nullable.NewNullableWithValue(true), CanUpdateAllSettings: nullable.NewNullableWithValue(false)})
		if err != nil {
			t.Fatal("create disposable membership role")
		}
		roles[i] = created.CreateOneRole.Id
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for _, id := range roles {
			if _, err := client.DeleteOneRole(ctx, fixture.Operator.API, id); err != nil {
				// v2.44.0 deletes role targets but can retain removed membership
				// IDs in its role-assignment cache. Never rebind live users or
				// edit the database to make disposable role cleanup succeed.
				if strings.Contains(err.Error(), "User workspaces not found") {
					t.Log("pinned role deletion rejected a stale removed-membership cache entry; the disposable stack owns final cleanup")
				} else {
					t.Errorf("cleanup disposable membership role: %s", err)
				}
			}
		}
	})
	empty := resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}
	update := resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)}}
	freshEmail := func() string { return "terraform-member-" + uuid.NewString() + "@acceptance.example" }
	assertAbsent := func(email string) error {
		user, err := client.CurrentUser(t.Context(), fixture.Operator.API)
		if err != nil {
			return fmt.Errorf("read members after destroy")
		}
		for _, m := range user.CurrentUser.WorkspaceMembers {
			if m.UserEmail == email {
				return fmt.Errorf("declared member survived destroy")
			}
		}
		invites, err := client.FindWorkspaceInvitations(t.Context(), fixture.Operator.API)
		if err != nil {
			return fmt.Errorf("read invitations after destroy")
		}
		for _, i := range invites.FindWorkspaceInvitations {
			if i.Email == email {
				return fmt.Errorf("declared invitation survived destroy")
			}
		}
		return nil
	}
	t.Run("pending_invite_update_import_revoke_and_disappearance", func(t *testing.T) {
		email := freshEmail()
		id := memberImportID(fixture.WorkspaceID, email)
		var invitationID string
		capture := func(s *terraform.State) error {
			attrs := s.RootModule().Resources[address].Primary.Attributes
			invitationID = attrs["invitation_id"]
			if !validRoleUUID(invitationID) {
				return fmt.Errorf("missing pending invitation UUID")
			}
			return nil
		}
		resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: acceptanceFactories(), CheckDestroy: func(*terraform.State) error { return assertAbsent(email) }, Steps: []resource.TestStep{
			{Config: memberAcceptanceConfig(email, roles[0]), Check: resource.ComposeAggregateTestCheckFunc(capture, resource.TestCheckResourceAttr(address, "id", id), resource.TestCheckResourceAttr(address, "workspace_id", fixture.WorkspaceID), resource.TestCheckResourceAttr(address, "status", "pending"), resource.TestCheckResourceAttrSet(address, "expires_at"), resource.TestCheckNoResourceAttr(address, "member_id"))},
			{Config: memberAcceptanceConfig(email, roles[1]), ConfigPlanChecks: update, Check: resource.ComposeAggregateTestCheckFunc(capture, resource.TestCheckResourceAttr(address, "id", id), resource.TestCheckResourceAttr(address, "role_id", roles[1]))},
			{ResourceName: address, ImportState: true, ImportStateVerify: true},
			{Config: memberAcceptanceConfig(email, roles[1]), ConfigPlanChecks: empty},
			{PreConfig: func() {
				revoked, err := client.DeleteWorkspaceInvitation(t.Context(), fixture.Operator.API, invitationID)
				if err != nil || revoked.DeleteWorkspaceInvitation != "success" {
					t.Fatal("out-of-band invitation revocation")
				}
			}, Config: memberAcceptanceConfig(email, roles[1]), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionCreate)}}, Check: resource.ComposeAggregateTestCheckFunc(capture, resource.TestCheckResourceAttr(address, "id", id))},
		}})
	})
	t.Run("acceptance_preserves_id_then_accepted_role_drift_and_removal", func(t *testing.T) {
		email := freshEmail()
		id := memberImportID(fixture.WorkspaceID, email)
		var account *acceptance.Account
		resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: acceptanceFactories(), CheckDestroy: func(*terraform.State) error { return assertAbsent(email) }, Steps: []resource.TestStep{
			{Config: memberAcceptanceConfig(email, roles[0]), Check: resource.TestCheckResourceAttr(address, "status", "pending")},
			{PreConfig: func() { account = fixture.AcceptInvitation(t, email, roles[0]) }, Config: memberAcceptanceConfig(email, roles[0]), ConfigPlanChecks: empty, Check: resource.ComposeAggregateTestCheckFunc(resource.TestCheckResourceAttr(address, "id", id), resource.TestCheckResourceAttr(address, "status", "accepted"), resource.TestCheckResourceAttrSet(address, "member_id"), resource.TestCheckNoResourceAttr(address, "invitation_id"), resource.TestCheckNoResourceAttr(address, "expires_at"))},
			{ResourceName: address, ImportState: true, ImportStateVerify: true},
			{Config: memberAcceptanceConfig(email, roles[1]), ConfigPlanChecks: update, Check: resource.TestCheckResourceAttr(address, "role_id", roles[1])},
			{PreConfig: func() {
				if _, err := client.UpdateWorkspaceMemberRole(t.Context(), fixture.Operator.API, account.MemberID, roles[0]); err != nil {
					t.Fatal("out-of-band member role update")
				}
			}, Config: memberAcceptanceConfig(email, roles[1]), ConfigPlanChecks: update, Check: func(*terraform.State) error {
				user, err := client.CurrentUser(t.Context(), account.API)
				if err != nil {
					return fmt.Errorf("read restored member role")
				}
				m, e := user.CurrentUser.WorkspaceMember.Get()
				if e != nil || len(m.Roles) != 1 || m.Roles[0].Id != roles[1] {
					return fmt.Errorf("desired member role was not restored")
				}
				return nil
			}},
			{Config: memberAcceptanceConfig(email, roles[1]), ConfigPlanChecks: empty},
		}})
	})
	t.Run("existing_accepted_requires_import_and_out_of_band_removal", func(t *testing.T) {
		existing := fixture.InviteAccount(t, "existing-import", roles[0])
		config := memberAcceptanceConfig(existing.Email, roles[0])
		resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: acceptanceFactories(), CheckDestroy: func(*terraform.State) error { return assertAbsent(existing.Email) }, Steps: []resource.TestStep{
			{Config: config, ExpectError: regexp.MustCompile("import it explicitly")},
			{Config: config + fmt.Sprintf("import {\n to = %s\n id = %q\n}\n", address, memberImportID(fixture.WorkspaceID, existing.Email)), ConfigPlanChecks: empty, Check: resource.TestCheckResourceAttr(address, "member_id", existing.MemberID)},
			{PreConfig: func() {
				if _, err := client.DeleteUserFromWorkspace(t.Context(), fixture.Operator.API, existing.MemberID); err != nil {
					t.Fatal("out-of-band accepted removal")
				}
			}, Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionCreate)}}, Check: resource.TestCheckResourceAttr(address, "status", "pending")},
		}})
	})
	t.Run("existing_pending_requires_import_and_builtin_role_assignment", func(t *testing.T) {
		email := freshEmail()
		sent, err := client.SendInvitations(t.Context(), fixture.Operator.API, []string{email}, nullable.NewNullableWithValue(fixture.AdminRole.Id))
		if err != nil || !sent.SendInvitations.Success {
			t.Fatal("prepare existing pending invitation")
		}
		config := memberAcceptanceConfig(email, fixture.AdminRole.Id)
		resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: acceptanceFactories(), CheckDestroy: func(*terraform.State) error { return assertAbsent(email) }, Steps: []resource.TestStep{
			{Config: config, ExpectError: regexp.MustCompile("import it explicitly")},
			{Config: config + fmt.Sprintf("import {\n to = %s\n id = %q\n}\n", address, memberImportID(fixture.WorkspaceID, email)), ConfigPlanChecks: empty, Check: resource.TestCheckResourceAttr(address, "status", "pending")},
		}})
	})
	t.Run("operator_and_foreign_import_refused", func(t *testing.T) {
		for _, id := range []string{memberImportID(fixture.WorkspaceID, fixture.Operator.Email), memberImportID(uuid.NewString(), freshEmail())} {
			resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: acceptanceFactories(), Steps: []resource.TestStep{{Config: memberAcceptanceConfig(freshEmail(), roles[0]) + fmt.Sprintf("import {\n to = %s\n id = %q\n}\n", address, id), PlanOnly: true, ExpectError: regexp.MustCompile("Unable to Import Twenty Workspace Member")}}})
		}
	})
	t.Run("operator_update_and_delete_refused", func(t *testing.T) {
		session, err := client.NewSession(t.Context(), fixture.Stack.Endpoint, fixture.Operator.Email, fixture.Operator.Password, true)
		if err != nil {
			t.Fatal("authenticate disposable member operator")
		}
		r := &workspaceMemberResource{}
		var configured frameworkresource.ConfigureResponse
		r.Configure(t.Context(), frameworkresource.ConfigureRequest{ProviderData: &ClientData{Client: session}}, &configured)
		m := memberModel()
		m.ID = types.StringValue(memberImportID(fixture.WorkspaceID, fixture.Operator.Email))
		m.WorkspaceID = types.StringValue(fixture.WorkspaceID)
		m.Email = types.StringValue(fixture.Operator.Email)
		m.Status = types.StringValue("accepted")
		m.MemberID = types.StringValue(fixture.Operator.MemberID)
		m.RoleID = types.StringValue(roles[0])
		state := memberState(t, m)
		updated := frameworkresource.UpdateResponse{State: state}
		r.Update(t.Context(), frameworkresource.UpdateRequest{State: state, Plan: memberPlan(t, m)}, &updated)
		deleted := frameworkresource.DeleteResponse{State: state}
		r.Delete(t.Context(), frameworkresource.DeleteRequest{State: state}, &deleted)
		if !updated.Diagnostics.HasError() || !deleted.Diagnostics.HasError() {
			t.Fatal("operator membership mutation accepted")
		}
	})
}

// Both settings grants are required, even when an individual server mutation
// checks only one. Missing list visibility must fail before taking ownership.
func testAccMemberPermissionDenied(t *testing.T, fixture *acceptance.Fixture) {
	email := "denied-member-" + uuid.NewString() + "@acceptance.example"
	sent, err := client.SendInvitations(t.Context(), fixture.Operator.API, []string{email}, nullable.NewNullableWithValue(fixture.AdminRole.Id))
	if err != nil || !sent.SendInvitations.Success || len(sent.SendInvitations.Result) != 1 {
		t.Fatal("prepare disposable denied-permission invitation")
	}
	invitationID := sent.SendInvitations.Result[0].Id
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := client.DeleteWorkspaceInvitation(ctx, fixture.Operator.API, invitationID); err != nil {
			t.Error("cleanup denied-permission invitation")
		}
	})
	config := memberAcceptanceConfig(email, fixture.AdminRole.Id)
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: acceptanceFactories(), Steps: []resource.TestStep{
		{Config: config, ExpectError: regexp.MustCompile("Unable to Create Twenty Workspace Member")},
		{Config: config + fmt.Sprintf("import {\n to = twenty_workspace_member.test\n id = %q\n}\n", memberImportID(fixture.WorkspaceID, email)), PlanOnly: true, ExpectError: regexp.MustCompile("Unable to Import Twenty Workspace Member")},
	}})
}
