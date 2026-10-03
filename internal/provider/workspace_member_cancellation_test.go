// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"errors"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func assertMemberCancellationWarning(t *testing.T, diagnostics diag.Diagnostics) {
	t.Helper()
	for _, d := range diagnostics {
		if d.Severity() == diag.SeverityWarning && d.Summary() == memberCancellationSummary && d.Detail() == memberCancellationDetail {
			return
		}
	}
	t.Fatal("unverified post-cancellation access must report the fixed inspection/import warning")
}

func TestMemberPostCancellationAcceptanceRequiresImport(t *testing.T) {
	for _, action := range []string{"update", "delete"} {
		for _, outcome := range []string{"success", "error result", "malformed result", "ambiguous transport", "failed followup read", "second update snapshot"} {
			if action == "delete" && outcome == "second update snapshot" {
				continue
			}
			t.Run(action+"/"+outcome, func(t *testing.T) {
				mock := newMemberMock()
				mock.invite(memberEmail, roleTestID)
				r := mockMemberResource(mock)
				state := memberImport(t, r, memberEmail)
				original := getMemberState(t, state)
				replaced, waiting := false, false
				acceptReplacement := func() {
					mock.invite(memberEmail, roleTestID)
					mock.invites[0]["id"] = memberMembershipID
					if mock.invites[0]["id"] == original.InvitationID.ValueString() {
						t.Fatal("external invitation B must have a different native ID")
					}
					mock.accept()
					replaced = true
				}
				mock.after = func(op string, data map[string]any) {
					if op == "FindWorkspaceInvitations" && waiting {
						// The cancellation snapshot has already read absence. Acceptance
						// is first visible in the separate pre-reissue safety snapshot.
						acceptReplacement()
						waiting = false
						return
					}
					if op != "DeleteWorkspaceInvitation" {
						return
					}
					// The mock cancels A successfully before this hook. A competing
					// administrator creates and accepts B before the follow-up snapshot.
					if data["deleteWorkspaceInvitation"] != "success" || len(mock.invites) != 0 {
						t.Fatal("invitation A was not cancelled before external acceptance")
					}
					if outcome == "second update snapshot" {
						waiting = true
						return
					}
					acceptReplacement()
					switch outcome {
					case "error result":
						data["deleteWorkspaceInvitation"] = "error"
					case "malformed result":
						data["deleteWorkspaceInvitation"] = nil
					case "ambiguous transport":
						mock.lateFail = map[string]error{op: errors.New("private-password invitationToken=private-token")}
					case "failed followup read":
						mock.fail["CurrentUser"] = errors.New("private-password invitationToken=private-token")
					}
				}
				var diagnostics diag.Diagnostics
				if action == "update" {
					plan := original
					plan.RoleID = types.StringValue(roleTestOther)
					resp := resource.UpdateResponse{State: state}
					r.Update(t.Context(), resource.UpdateRequest{State: state, Plan: memberPlan(t, plan)}, &resp)
					state, diagnostics = resp.State, resp.Diagnostics
				} else {
					resp := resource.DeleteResponse{State: state}
					r.Delete(t.Context(), resource.DeleteRequest{State: state}, &resp)
					state, diagnostics = resp.State, resp.Diagnostics
				}
				got := getMemberState(t, state)
				if !replaced || !diagnostics.HasError() || got.OwnershipConfirmed.ValueBool() || !got.ID.Equal(original.ID) {
					t.Fatal("post-cancellation acceptance retained ownership or lost the stable ID")
				}
				if outcome != "failed followup read" && (got.Status.ValueString() != "accepted" || got.MemberID.ValueString() != memberNativeID || !got.InvitationID.IsNull() || !got.RoleID.Equal(original.RoleID)) {
					t.Fatal("readable external accepted access was not retained")
				}
				assertMemberCancellationWarning(t, diagnostics)
				for _, d := range diagnostics {
					for _, private := range []string{"private-password", "private-token", memberEmail, memberNativeID, memberMembershipID, original.InvitationID.ValueString()} {
						if strings.Contains(d.Detail(), private) {
							t.Fatal("post-cancellation diagnostic exposed private details")
						}
					}
				}
				if memberCallCount(mock, "DeleteWorkspaceInvitation") != 1 {
					t.Fatal("cancellation was retried")
				}
				for _, op := range []string{"SendInvitations", "UpdateWorkspaceMemberRole", "DeleteUserFromWorkspace"} {
					if memberCallCount(mock, op) != 0 {
						t.Fatal("cancellation fell through to another mutation")
					}
				}
				mock.after, mock.lateFail = nil, nil
				delete(mock.fail, "CurrentUser")
				mock.calls = nil
				read := resource.ReadResponse{State: state}
				r.Read(t.Context(), resource.ReadRequest{State: state}, &read)
				got = getMemberState(t, read.State)
				if read.Diagnostics.HasError() || got.OwnershipConfirmed.ValueBool() || !got.ID.Equal(original.ID) || got.MemberID.ValueString() != memberNativeID || got.Status.ValueString() != "accepted" {
					t.Fatal("refresh adopted or lost external accepted access")
				}
				state = read.State
				plan := got
				plan.RoleID = types.StringValue(roleTestOther)
				updated := resource.UpdateResponse{State: state}
				r.Update(t.Context(), resource.UpdateRequest{State: state, Plan: memberPlan(t, plan)}, &updated)
				deleted := resource.DeleteResponse{State: state}
				r.Delete(t.Context(), resource.DeleteRequest{State: state}, &deleted)
				if !updated.Diagnostics.HasError() || !deleted.Diagnostics.HasError() || !updated.State.Raw.Equal(state.Raw) || !deleted.State.Raw.Equal(state.Raw) {
					t.Fatal("unconfirmed accepted access allowed update/delete or changed state")
				}
				for _, ds := range []diag.Diagnostics{updated.Diagnostics, deleted.Diagnostics} {
					if len(ds) != 1 || ds[0].Detail() != errMemberOwnership.Error() {
						t.Fatal("blocked writes must require explicit inspection/import")
					}
				}
				assertNoMemberWrites(t, mock)
				state = memberImport(t, r, memberEmail)
				got = getMemberState(t, state)
				if !got.OwnershipConfirmed.ValueBool() || !got.ID.Equal(original.ID) || got.MemberID.ValueString() != memberNativeID {
					t.Fatal("explicit import did not confirm inspected accepted access")
				}
				assertNoMemberWrites(t, mock)
				got.RoleID = types.StringValue(roleTestOther)
				updated = resource.UpdateResponse{State: state}
				r.Update(t.Context(), resource.UpdateRequest{State: state, Plan: memberPlan(t, got)}, &updated)
				deleted = resource.DeleteResponse{State: updated.State}
				r.Delete(t.Context(), resource.DeleteRequest{State: updated.State}, &deleted)
				if updated.Diagnostics.HasError() || deleted.Diagnostics.HasError() || memberCallCount(mock, "UpdateWorkspaceMemberRole") != 1 || memberCallCount(mock, "DeleteUserFromWorkspace") != 1 {
					t.Fatal("explicit import did not authorize the declared update/removal")
				}
			})
		}
	}
}
