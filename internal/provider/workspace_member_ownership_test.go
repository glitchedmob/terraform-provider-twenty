// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"errors"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestMemberAmbiguousSendRequiresExplicitImportBeforeFurtherWrites(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		mock := newMemberMock()
		mock.lateFail = map[string]error{"SendInvitations": errors.New("mail delivery uncertain")}
		r := mockMemberResource(mock)
		m := memberModel()
		created := resource.CreateResponse{State: memberState(t, m)}
		r.Create(t.Context(), resource.CreateRequest{Plan: memberPlan(t, m)}, &created)
		if !created.Diagnostics.HasError() || getMemberState(t, created.State).OwnershipConfirmed.ValueBool() {
			t.Fatal("ambiguous invitation was automatically adopted")
		}
		mock.lateFail = nil
		if accepted {
			mock.accept()
		}
		refreshed := resource.ReadResponse{State: created.State}
		r.Read(t.Context(), resource.ReadRequest{State: created.State}, &refreshed)
		if refreshed.Diagnostics.HasError() || getMemberState(t, refreshed.State).OwnershipConfirmed.ValueBool() {
			t.Fatal("refresh must not confer ownership of an ambiguous send")
		}
		state := refreshed.State
		plan := getMemberState(t, state)
		plan.RoleID = types.StringValue(roleTestOther)
		updated := resource.UpdateResponse{State: state}
		r.Update(t.Context(), resource.UpdateRequest{State: state, Plan: memberPlan(t, plan)}, &updated)
		deleted := resource.DeleteResponse{State: state}
		r.Delete(t.Context(), resource.DeleteRequest{State: state}, &deleted)
		if !updated.Diagnostics.HasError() || !deleted.Diagnostics.HasError() || memberCallCount(mock, "DeleteWorkspaceInvitation") != 0 || memberCallCount(mock, "DeleteUserFromWorkspace") != 0 || memberCallCount(mock, "UpdateWorkspaceMemberRole") != 0 {
			t.Fatal("unconfirmed ownership allowed another write")
		}
		state = memberImport(t, r, memberEmail)
		if !getMemberState(t, state).OwnershipConfirmed.ValueBool() {
			t.Fatal("explicit import must confirm takeover")
		}
		deleted = resource.DeleteResponse{State: state}
		r.Delete(t.Context(), resource.DeleteRequest{State: state}, &deleted)
		if deleted.Diagnostics.HasError() {
			t.Fatal(deleted.Diagnostics)
		}
	}
}
func TestMemberConcurrentDeclaredEmailSendsOnlyOnce(t *testing.T) {
	mock := newMemberMock()
	first := mockMemberResource(mock)
	second := mockMemberResource(mock)
	second.mutationLock = first.mutationLock
	m := memberModel()
	plan := memberPlan(t, m)
	state := memberState(t, m)
	results := make(chan bool, 2)
	var start sync.WaitGroup
	start.Add(2)
	for _, r := range []*workspaceMemberResource{first, second} {
		go func() {
			start.Done()
			start.Wait()
			resp := resource.CreateResponse{State: state}
			r.Create(t.Context(), resource.CreateRequest{Plan: plan}, &resp)
			results <- resp.Diagnostics.HasError()
		}()
	}
	failed := 0
	for range 2 {
		if <-results {
			failed++
		}
	}
	if failed != 1 || memberCallCount(mock, "SendInvitations") != 1 {
		t.Fatal("concurrent create adopted access or sent twice")
	}
}
func TestMemberDuplicateEmailAndNilIdentityFailClosed(t *testing.T) {
	for _, kind := range []string{"email", "user", "membership", "nil user", "nil membership", "nil role"} {
		t.Run(kind, func(t *testing.T) {
			mock := newMemberMock()
			mock.invite(memberEmail, roleTestID)
			mock.accept()
			r := mockMemberResource(mock)
			switch kind {
			case "email":
				mock.members[2]["userEmail"] = "recovery@example.test"
			case "user":
				mock.members[2]["userId"] = safetyRecovery
			case "membership":
				mock.members[2]["userWorkspaceId"] = safetyRecovery
			case "nil user":
				mock.members[2]["userId"] = "00000000-0000-0000-0000-000000000000"
			case "nil membership":
				mock.members[2]["userWorkspaceId"] = "00000000-0000-0000-0000-000000000000"
			case "nil role":
				mock.roles[0]["universalIdentifier"] = "00000000-0000-0000-0000-000000000000"
			}
			mock.rebind()
			imported := resource.ImportStateResponse{State: memberState(t, memberModel())}
			r.ImportState(t.Context(), resource.ImportStateRequest{ID: memberImportID(safetyWorkspace, memberEmail)}, &imported)
			if !imported.Diagnostics.HasError() {
				t.Fatal("ambiguous membership snapshot allowed import")
			}
		})
	}
}
