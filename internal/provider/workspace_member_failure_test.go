// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func memberCallCount(mock *memberMock, op string) int {
	count := 0
	for _, call := range mock.calls {
		if call == op {
			count++
		}
	}
	return count
}
func TestMemberPartialInvitationSend(t *testing.T) {
	for _, failure := range []string{"before send", "mail failure", "payload errors", "lossy payload", "unrelated result", "read failure"} {
		t.Run(failure, func(t *testing.T) {
			mock := newMemberMock()
			r := mockMemberResource(mock)
			m := memberModel()
			switch failure {
			case "before send":
				mock.fail["SendInvitations"] = errors.New("private-password inviteToken=private-token")
			case "mail failure":
				mock.lateFail = map[string]error{"SendInvitations": errors.New("mail failed private-token")}
			case "payload errors":
				mock.after = func(op string, d map[string]any) {
					if op == "SendInvitations" {
						p := d["sendInvitations"].(map[string]any)
						p["success"] = false
						p["errors"] = []string{"private-password"}
					}
				}
			case "lossy payload":
				mock.after = func(op string, d map[string]any) {
					if op == "SendInvitations" {
						d["sendInvitations"] = map[string]any{"success": true}
					}
				}
			case "unrelated result":
				mock.after = func(op string, d map[string]any) {
					if op == "SendInvitations" {
						d["sendInvitations"].(map[string]any)["result"] = []any{map[string]any{"id": memberNativeID, "email": "unrelated@example.test", "roleId": roleTestID, "expiresAt": time.Now().Add(time.Hour)}}
					}
				}
			case "read failure":
				mock.after = func(op string, _ map[string]any) {
					if op == "SendInvitations" {
						mock.fail["FindWorkspaceInvitations"] = errors.New("private-token")
					}
				}
			}
			resp := resource.CreateResponse{State: memberState(t, m)}
			r.Create(t.Context(), resource.CreateRequest{Plan: memberPlan(t, m)}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("ambiguous invitation send accepted")
			}
			got := getMemberState(t, resp.State)
			if got.ID.ValueString() != m.ID.ValueString() || memberCallCount(mock, "SendInvitations") != 1 || memberCallCount(mock, "DeleteWorkspaceInvitation") != 0 {
				t.Fatal("partial send lost identity, retried, or rolled back")
			}
			if failure != "before send" && failure != "read failure" && got.Status.ValueString() != "pending" {
				t.Fatal("recoverable pending invitation not recorded")
			}
			for _, d := range resp.Diagnostics {
				if strings.Contains(d.Detail(), "private-password") || strings.Contains(d.Detail(), "private-token") {
					t.Fatal("raw mutation details leaked")
				}
			}
		})
	}
}
func TestMemberCancelFailuresNeverResend(t *testing.T) {
	for _, failure := range []string{"before cancel", "ambiguous cancel", "error result", "lossy result", "read failed", "replacement send failed"} {
		t.Run(failure, func(t *testing.T) {
			mock := newMemberMock()
			mock.invite(memberEmail, roleTestID)
			r := mockMemberResource(mock)
			state := memberImport(t, r, memberEmail)
			m := getMemberState(t, state)
			m.RoleID = types.StringValue(roleTestOther)
			switch failure {
			case "before cancel":
				mock.fail["DeleteWorkspaceInvitation"] = errors.New("private-secret")
			case "ambiguous cancel":
				mock.lateFail = map[string]error{"DeleteWorkspaceInvitation": errors.New("private-secret")}
			case "error result":
				mock.after = func(op string, d map[string]any) {
					if op == "DeleteWorkspaceInvitation" {
						d["deleteWorkspaceInvitation"] = "error"
					}
				}
			case "lossy result":
				mock.after = func(op string, d map[string]any) {
					if op == "DeleteWorkspaceInvitation" {
						d["deleteWorkspaceInvitation"] = nil
					}
				}
			case "read failed":
				mock.after = func(op string, _ map[string]any) {
					if op == "DeleteWorkspaceInvitation" {
						mock.fail["FindWorkspaceInvitations"] = errors.New("private-secret")
					}
				}
			case "replacement send failed":
				mock.lateFail = map[string]error{"SendInvitations": errors.New("private-secret")}
			}
			resp := resource.UpdateResponse{State: state}
			r.Update(t.Context(), resource.UpdateRequest{State: state, Plan: memberPlan(t, m)}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("partial update accepted")
			}
			got := getMemberState(t, resp.State)
			if got.ID.ValueString() != m.ID.ValueString() || memberCallCount(mock, "DeleteWorkspaceInvitation") != 1 {
				t.Fatal("partial cancellation lost stable identity or retried")
			}
			sends := 0
			if failure == "replacement send failed" {
				sends = 1
				if got.Status.ValueString() != "pending" || got.RoleID.ValueString() != roleTestOther {
					t.Fatal("replacement invitation not recovered")
				}
			}
			if memberCallCount(mock, "SendInvitations") != sends {
				t.Fatal("unconfirmed cancellation caused resend")
			}
		})
	}
}
func TestMemberAcceptanceRaceDoesNotRemoveOrReassign(t *testing.T) {
	for _, action := range []string{"update during cancel", "delete during cancel", "update before read", "delete before read"} {
		t.Run(action, func(t *testing.T) {
			mock := newMemberMock()
			mock.invite(memberEmail, roleTestID)
			r := mockMemberResource(mock)
			state := memberImport(t, r, memberEmail)
			m := getMemberState(t, state)
			m.RoleID = types.StringValue(roleTestOther)
			if strings.Contains(action, "before read") {
				mock.accept()
			} else {
				mock.before = func(op string) {
					if op == "DeleteWorkspaceInvitation" && len(mock.invites) > 0 {
						mock.accept()
					}
				}
			}
			if strings.HasPrefix(action, "update") {
				resp := resource.UpdateResponse{State: state}
				r.Update(t.Context(), resource.UpdateRequest{State: state, Plan: memberPlan(t, m)}, &resp)
				if !resp.Diagnostics.HasError() {
					t.Fatal("acceptance race update silently succeeded")
				}
				if getMemberState(t, resp.State).Status.ValueString() != "accepted" {
					t.Fatal("newly accepted access not retained in state")
				}
			} else {
				resp := resource.DeleteResponse{State: state}
				r.Delete(t.Context(), resource.DeleteRequest{State: state}, &resp)
				if !resp.Diagnostics.HasError() {
					t.Fatal("acceptance race deletion silently succeeded")
				}
			}
			if memberCallCount(mock, "UpdateWorkspaceMemberRole") != 0 || memberCallCount(mock, "DeleteUserFromWorkspace") != 0 || memberCallCount(mock, "SendInvitations") != 0 || len(mock.members) != 3 {
				t.Fatal("pending operation changed accepted access")
			}
			read := resource.ReadResponse{State: state}
			r.Read(t.Context(), resource.ReadRequest{State: state}, &read)
			if read.Diagnostics.HasError() || getMemberState(t, read.State).ID.ValueString() != m.ID.ValueString() || getMemberState(t, read.State).RoleID.ValueString() != roleTestID {
				t.Fatal("race did not preserve accepted identity/role")
			}
		})
	}
}
func TestMemberAcceptedMutationFailuresAndReplacedIdentity(t *testing.T) {
	for _, op := range []string{"UpdateWorkspaceMemberRole", "DeleteUserFromWorkspace"} {
		for _, kind := range []string{"transport", "lossy", "unconfirmed read"} {
			t.Run(op+kind, func(t *testing.T) {
				mock := newMemberMock()
				mock.invite(memberEmail, roleTestID)
				mock.accept()
				r := mockMemberResource(mock)
				state := memberImport(t, r, memberEmail)
				switch kind {
				case "transport":
					mock.fail[op] = errors.New("FORBIDDEN private-secret")
				case "lossy":
					mock.after = func(operation string, d map[string]any) {
						if operation == op {
							key := "updateWorkspaceMemberRole"
							if op == "DeleteUserFromWorkspace" {
								key = "deleteUserFromWorkspace"
							}
							d[key] = map[string]any{"id": "wrong"}
						}
					}
				case "unconfirmed read":
					mock.after = func(operation string, _ map[string]any) {
						if operation == op {
							mock.fail["CurrentUser"] = errors.New("private-secret")
						}
					}
				}
				if op == "UpdateWorkspaceMemberRole" {
					m := getMemberState(t, state)
					m.RoleID = types.StringValue(roleTestOther)
					resp := resource.UpdateResponse{State: state}
					r.Update(t.Context(), resource.UpdateRequest{State: state, Plan: memberPlan(t, m)}, &resp)
					if !resp.Diagnostics.HasError() || getMemberState(t, resp.State).ID.ValueString() != m.ID.ValueString() {
						t.Fatal("partial accepted update lost identity")
					}
				} else {
					resp := resource.DeleteResponse{State: state}
					r.Delete(t.Context(), resource.DeleteRequest{State: state}, &resp)
					if !resp.Diagnostics.HasError() || !resp.State.Raw.Equal(state.Raw) {
						t.Fatal("unconfirmed deletion removed state")
					}
				}
				if memberCallCount(mock, op) != 1 {
					t.Fatal("mutation retried")
				}
			})
		}
	}
	mock := newMemberMock()
	mock.invite(memberEmail, roleTestID)
	mock.accept()
	r := mockMemberResource(mock)
	state := memberImport(t, r, memberEmail)
	mock.members[2]["id"] = roleTestFlag
	mock.rebind()
	deleted := resource.DeleteResponse{State: state}
	r.Delete(t.Context(), resource.DeleteRequest{State: state}, &deleted)
	if !deleted.Diagnostics.HasError() || memberCallCount(mock, "DeleteUserFromWorkspace") != 0 {
		t.Fatal("replaced member identity deleted")
	}
	mock = newMemberMock()
	mock.invite(memberEmail, roleTestID)
	r = mockMemberResource(mock)
	state = memberImport(t, r, memberEmail)
	mock.invites[0]["id"] = memberNativeID
	m := getMemberState(t, state)
	m.RoleID = types.StringValue(roleTestOther)
	updated := resource.UpdateResponse{State: state}
	r.Update(t.Context(), resource.UpdateRequest{State: state, Plan: memberPlan(t, m)}, &updated)
	if !updated.Diagnostics.HasError() || memberCallCount(mock, "DeleteWorkspaceInvitation") != 0 {
		t.Fatal("replaced invitation cancelled")
	}
}

type blockedMemberClient struct {
	*memberMock
	once    sync.Once
	entered chan struct{}
	release chan struct{}
	reads   chan struct{}
}

func (c *blockedMemberClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	if req.OpName == "CurrentUser" {
		c.reads <- struct{}{}
	}
	if req.OpName == "SendInvitations" {
		c.once.Do(func() { close(c.entered); <-c.release })
	}
	return c.memberMock.MakeRequest(ctx, req, resp)
}
func TestMemberSharesRoleMutationLock(t *testing.T) {
	api := &blockedMemberClient{memberMock: newMemberMock(), entered: make(chan struct{}), release: make(chan struct{}), reads: make(chan struct{}, 4)}
	member := mockMemberResource(api.memberMock)
	member.client = api
	role := mockRoleResource(api.roleMock)
	role.client = api
	role.mutationLock = member.mutationLock
	m := memberModel()
	state := memberState(t, m)
	plan := memberPlan(t, m)
	roleModel := roleResourceTestModel()
	roleModel.ID = types.StringUnknown()
	rolePlan := roleResourcePlan(t, roleModel)
	roleState := roleResourceState(t, roleModel)
	results := make(chan bool, 2)
	go func() {
		resp := resource.CreateResponse{State: state}
		member.Create(t.Context(), resource.CreateRequest{Plan: plan}, &resp)
		results <- resp.Diagnostics.HasError()
	}()
	select {
	case <-api.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("member send did not start")
	}
	<-api.reads
	go func() {
		resp := resource.CreateResponse{State: roleState}
		role.Create(t.Context(), resource.CreateRequest{Plan: rolePlan}, &resp)
		results <- resp.Diagnostics.HasError()
	}()
	select {
	case <-api.reads:
		close(api.release)
		t.Fatal("role safety reads escaped shared IAM lock")
	case <-time.After(25 * time.Millisecond):
	}
	close(api.release)
	for range 2 {
		select {
		case failed := <-results:
			if failed {
				t.Fatal("serialized IAM operation failed")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("IAM operation deadlocked")
		}
	}
}
