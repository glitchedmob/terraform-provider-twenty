// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"strings"
	"testing"
	"time"

	"github.com/glitchedmob/terraform-provider-twenty/internal/acceptance"
	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"
)

// The invitation is external to Terraform, so no dependency can order its
// removal. A refused destroy must leave valid, readable invitation metadata.
func testAccExternalInvitationRoleGuard(t *testing.T, fixture *acceptance.Fixture) {
	cli := newDisposableTerraform(t, fixture, fixture.Stack.Endpoint)
	cli.writeConfig(t, `terraform {
 required_providers { twenty = { source = "glitchedmob/twenty" } }
}
provider "twenty" { allow_insecure_http = true }
resource "twenty_role" "custom" {
 label = "External invitation deletion guard"
 permission_flags = []
}
`)
	cli.requireSuccess(t, "apply", "-auto-approve")
	before := cli.resources(t)
	roleID, ok := before["twenty_role.custom"]["id"].(string)
	if !ok || len(before) != 1 || !validRoleUUID(roleID) {
		t.Fatal("Terraform did not own exactly one custom role")
	}
	email := "external-invitation-" + uuid.NewString() + "@acceptance.example"
	sent, err := client.SendInvitations(t.Context(), fixture.Operator.API, []string{email}, nullable.NewNullableWithValue(roleID))
	if err != nil || !sent.SendInvitations.Success || len(sent.SendInvitations.Errors) != 0 || len(sent.SendInvitations.Result) != 1 {
		t.Fatal("create external test invitation through supported API")
	}
	var invitationID string
	verifyPending := func() {
		t.Helper()
		session, err := client.NewSession(t.Context(), fixture.Stack.Endpoint, fixture.Operator.Email, fixture.Operator.Password, true)
		if err != nil {
			t.Fatal("authenticate fresh invitation guard operator")
		}
		snapshot, err := readMemberSnapshot(t.Context(), session.Client(), session.Identity())
		if err != nil {
			t.Fatal("external invitation must not strand membership safety reads")
		}
		access, err := snapshot.access(email)
		if err != nil || access.status != "pending" || access.invitation == nil || access.member != nil || !strings.EqualFold(access.roleID, roleID) || snapshot.role(roleID) == nil || !access.invitation.ExpiresAt.After(time.Now()) {
			t.Fatal("external pending invitation must reference an existing, readable role")
		}
		if invitationID == "" {
			invitationID = access.invitation.Id
		} else if invitationID != access.invitation.Id {
			t.Fatal("refused destroy replaced the external invitation")
		}
	}
	verifyPending()
	output, code := cli.run(t, "destroy", "-auto-approve")
	if code != 1 || !strings.Contains(output, "Unable to Delete Twenty Role") || !strings.Contains(strings.Join(strings.Fields(output), " "), errRoleInvitationReference.Error()) {
		t.Fatal("Terraform destroy must refuse an external invitation reference")
	}
	_, diagnostic, found := strings.Cut(output, "Error: Unable to Delete Twenty Role")
	if !found || strings.Contains(diagnostic, email) || strings.Contains(diagnostic, invitationID) || strings.Contains(diagnostic, fixture.Operator.Password) {
		t.Fatal("invitation guard diagnostic exposed private data")
	}
	after := cli.resources(t)
	if len(after) != 1 || after["twenty_role.custom"]["id"] != roleID {
		t.Fatal("refused destroy lost Terraform role ownership")
	}
	verifyPending()
	cli.requireSuccess(t, "plan", "-detailed-exitcode")
	// Only this test-created invitation is revoked. There is no rebinding,
	// accepted-member teardown, cache maintenance, or blind destroy retry.
	revoked, err := client.DeleteWorkspaceInvitation(t.Context(), fixture.Operator.API, invitationID)
	if err != nil || revoked.DeleteWorkspaceInvitation != "success" {
		t.Fatal("revoke only the external test invitation through supported API")
	}
	cli.requireSuccess(t, "destroy", "-auto-approve")
	if len(cli.resources(t)) != 0 {
		t.Fatal("safe role cleanup retained Terraform state")
	}
	session, err := client.NewSession(t.Context(), fixture.Stack.Endpoint, fixture.Operator.Email, fixture.Operator.Password, true)
	if err != nil {
		t.Fatal("authenticate after safe role cleanup")
	}
	snapshot, err := readMemberSnapshot(t.Context(), session.Client(), session.Identity())
	if err != nil {
		t.Fatal("membership reads failed after deliberate invitation and role cleanup")
	}
	access, err := snapshot.access(email)
	if err != nil || access.status != "absent" || snapshot.role(roleID) != nil {
		t.Fatal("test-created invitation and role survived cleanup")
	}
	for _, admin := range []*acceptance.Account{fixture.Operator, fixture.Recovery} {
		fresh, err := client.NewSession(t.Context(), fixture.Stack.Endpoint, admin.Email, admin.Password, true)
		if err != nil || fresh.Identity().WorkspaceMemberID != admin.MemberID || fresh.Identity().UserID != admin.UserID {
			t.Fatal("invitation guard changed a protected administrator identity/login")
		}
		identity, err := client.CurrentUser(t.Context(), fresh.Client())
		if err != nil {
			t.Fatal("read protected administrator after invitation guard cleanup")
		}
		member, err := identity.CurrentUser.WorkspaceMember.Get()
		if err != nil || len(member.Roles) != 1 || member.Roles[0].Id != fixture.AdminRole.Id {
			t.Fatal("invitation guard changed a protected administrator role")
		}
	}
	t.Log("external pending invitation blocked Terraform role destroy; deliberate test-invitation revocation permitted cleanup; membership reads and both administrators preserved")
}
