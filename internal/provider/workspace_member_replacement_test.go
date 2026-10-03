// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func assertMemberReplacementWarning(t *testing.T, diagnostics diag.Diagnostics) {
	t.Helper()
	for _, d := range diagnostics {
		if d.Severity() == diag.SeverityWarning && d.Summary() == memberReplacementSummary && d.Detail() == memberReplacementDetail {
			return
		}
	}
	t.Fatal("native-ID replacement must report the fixed inspection/import warning")
}

func assertNoMemberWrites(t *testing.T, mock *memberMock) {
	t.Helper()
	for _, op := range []string{"SendInvitations", "DeleteWorkspaceInvitation", "UpdateWorkspaceMemberRole", "DeleteUserFromWorkspace"} {
		if memberCallCount(mock, op) != 0 {
			t.Fatal("replacement or unconfirmed ownership allowed a write")
		}
	}
}

func TestMemberReplacementRequiresImportAcrossRefreshAndMutation(t *testing.T) {
	for _, kind := range []string{"invitation", "accepted member", "accepted to invitation"} {
		for _, action := range []string{"read then update and delete", "update then read and delete", "delete then read and update"} {
			t.Run(kind+"/"+action, func(t *testing.T) {
				mock := newMemberMock()
				mock.invite(memberEmail, roleTestID)
				if kind != "invitation" {
					mock.accept()
				}
				r := mockMemberResource(mock)
				state := memberImport(t, r, memberEmail)
				original := getMemberState(t, state)
				switch kind {
				case "invitation":
					mock.invites[0]["id"] = memberNativeID
				case "accepted member":
					mock.members[2]["id"] = roleTestFlag
					mock.members[2]["userWorkspaceId"] = roleTestFlag
					mock.rebind()
				case "accepted to invitation":
					mock.members = mock.members[:2]
					mock.rebind()
					mock.invite(memberEmail, roleTestID)
				}
				if strings.HasPrefix(action, "read") {
					resp := resource.ReadResponse{State: state}
					r.Read(t.Context(), resource.ReadRequest{State: state}, &resp)
					if resp.Diagnostics.HasError() {
						t.Fatal(resp.Diagnostics)
					}
					assertMemberReplacementWarning(t, resp.Diagnostics)
					state = resp.State
				} else if strings.HasPrefix(action, "update") {
					plan := original
					plan.RoleID = types.StringValue(roleTestOther)
					resp := resource.UpdateResponse{State: state}
					r.Update(t.Context(), resource.UpdateRequest{State: state, Plan: memberPlan(t, plan)}, &resp)
					if !resp.Diagnostics.HasError() {
						t.Fatal("update accepted replacement ownership")
					}
					assertMemberReplacementWarning(t, resp.Diagnostics)
					state = resp.State
				} else {
					resp := resource.DeleteResponse{State: state}
					r.Delete(t.Context(), resource.DeleteRequest{State: state}, &resp)
					if !resp.Diagnostics.HasError() {
						t.Fatal("delete accepted replacement ownership")
					}
					assertMemberReplacementWarning(t, resp.Diagnostics)
					state = resp.State
				}
				got := getMemberState(t, state)
				if got.OwnershipConfirmed.ValueBool() || !got.ID.Equal(original.ID) || !got.RoleID.Equal(original.RoleID) || (got.MemberID.Equal(original.MemberID) && got.InvitationID.Equal(original.InvitationID)) {
					t.Fatal("replacement did not retain stable identity and drop ownership")
				}
				// A later refresh must not restore confirmation, nor may a later
				// apply/destroy mutate the replacement now recorded in state.
				read := resource.ReadResponse{State: state}
				r.Read(t.Context(), resource.ReadRequest{State: state}, &read)
				if read.Diagnostics.HasError() || getMemberState(t, read.State).OwnershipConfirmed.ValueBool() {
					t.Fatal("refresh reconfirmed external ownership")
				}
				state = read.State
				plan := getMemberState(t, state)
				plan.RoleID = types.StringValue(roleTestOther)
				updated := resource.UpdateResponse{State: state}
				r.Update(t.Context(), resource.UpdateRequest{State: state, Plan: memberPlan(t, plan)}, &updated)
				deleted := resource.DeleteResponse{State: state}
				r.Delete(t.Context(), resource.DeleteRequest{State: state}, &deleted)
				if !updated.Diagnostics.HasError() || !deleted.Diagnostics.HasError() {
					t.Fatal("unconfirmed replacement allowed update/destroy")
				}
				assertNoMemberWrites(t, mock)
				state = memberImport(t, r, memberEmail)
				if !getMemberState(t, state).OwnershipConfirmed.ValueBool() {
					t.Fatal("explicit import did not confirm inspected replacement")
				}
				deleted = resource.DeleteResponse{State: state}
				r.Delete(t.Context(), resource.DeleteRequest{State: state}, &deleted)
				if deleted.Diagnostics.HasError() {
					t.Fatal(deleted.Diagnostics)
				}
			})
		}
	}
}

func TestMemberOriginalAcceptanceKeepsConfirmedOwnership(t *testing.T) {
	mock := newMemberMock()
	mock.invite(memberEmail, roleTestID)
	r := mockMemberResource(mock)
	state := memberImport(t, r, memberEmail)
	id := getMemberState(t, state).ID
	mock.accept()
	read := resource.ReadResponse{State: state}
	r.Read(t.Context(), resource.ReadRequest{State: state}, &read)
	got := getMemberState(t, read.State)
	if len(read.Diagnostics) != 0 || !got.OwnershipConfirmed.ValueBool() || !got.ID.Equal(id) || got.Status.ValueString() != "accepted" || !got.InvitationID.IsNull() {
		t.Fatal("original pending-to-accepted transition lost ownership or stable identity")
	}
	got.RoleID = types.StringValue(roleTestOther)
	updated := resource.UpdateResponse{State: read.State}
	r.Update(t.Context(), resource.UpdateRequest{State: read.State, Plan: memberPlan(t, got)}, &updated)
	deleted := resource.DeleteResponse{State: updated.State}
	r.Delete(t.Context(), resource.DeleteRequest{State: updated.State}, &deleted)
	if updated.Diagnostics.HasError() || deleted.Diagnostics.HasError() || memberCallCount(mock, "UpdateWorkspaceMemberRole") != 1 || memberCallCount(mock, "DeleteUserFromWorkspace") != 1 {
		t.Fatal("original acceptance must allow subsequent declared update/removal")
	}
}

func TestMemberReplacementDuringUpdateReconciliationDropsOwnership(t *testing.T) {
	mock := newMemberMock()
	mock.invite(memberEmail, roleTestID)
	mock.accept()
	r := mockMemberResource(mock)
	state := memberImport(t, r, memberEmail)
	mock.after = func(op string, _ map[string]any) {
		if op == "UpdateWorkspaceMemberRole" {
			mock.members[2]["id"] = roleTestFlag
			mock.rebind()
		}
	}
	plan := getMemberState(t, state)
	plan.RoleID = types.StringValue(roleTestOther)
	updated := resource.UpdateResponse{State: state}
	r.Update(t.Context(), resource.UpdateRequest{State: state, Plan: memberPlan(t, plan)}, &updated)
	assertMemberReplacementWarning(t, updated.Diagnostics)
	if !updated.Diagnostics.HasError() || getMemberState(t, updated.State).OwnershipConfirmed.ValueBool() || memberCallCount(mock, "UpdateWorkspaceMemberRole") != 1 {
		t.Fatal("post-mutation replacement was adopted or retried")
	}
	deleted := resource.DeleteResponse{State: updated.State}
	r.Delete(t.Context(), resource.DeleteRequest{State: updated.State}, &deleted)
	if !deleted.Diagnostics.HasError() || memberCallCount(mock, "DeleteUserFromWorkspace") != 0 {
		t.Fatal("post-mutation replacement was deleted")
	}
}
