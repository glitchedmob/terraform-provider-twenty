// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

const invitationGuardRoleID = "abcdefab-1234-4234-8234-abcdefabcdef"

func roleGuardInvitation(roleID any) map[string]any {
	return map[string]any{
		"id": "aaaaaaaa-1234-4234-8234-aaaaaaaaaaaa", "email": "external-private@example.test",
		"expiresAt": "2099-01-01T00:00:00Z", "roleId": roleID,
	}
}

func TestRoleDeleteInvitationReferences(t *testing.T) {
	for name, test := range map[string]struct {
		invites []map[string]any
		blocked bool
	}{
		"target":           {[]map[string]any{roleGuardInvitation(invitationGuardRoleID)}, true},
		"uppercase target": {[]map[string]any{roleGuardInvitation(strings.ToUpper(invitationGuardRoleID))}, true},
		"expired target": {[]map[string]any{func() map[string]any {
			invite := roleGuardInvitation(invitationGuardRoleID)
			invite["expiresAt"] = "2000-01-01T00:00:00Z"
			return invite
		}()}, true},
		"other role":           {[]map[string]any{roleGuardInvitation(roleTestOther)}, false},
		"null follows default": {[]map[string]any{roleGuardInvitation(nil)}, false},
		"empty":                {[]map[string]any{}, false},
	} {
		t.Run(name, func(t *testing.T) {
			mock := newRoleMock()
			mock.roles[0]["id"], mock.roles[0]["universalIdentifier"] = invitationGuardRoleID, invitationGuardRoleID
			model := roleResourceTestModel()
			model.ID = types.StringValue(invitationGuardRoleID)
			state := roleResourceState(t, model)
			r := mockRoleResource(mock)
			// A preceding refresh sees no external reference. Delete must inspect
			// fresh invitations rather than trust a cached role or Terraform graph.
			read := resource.ReadResponse{State: state}
			r.Read(t.Context(), resource.ReadRequest{State: state}, &read)
			if read.Diagnostics.HasError() {
				t.Fatal("refresh role before external invitation")
			}
			mock.invites, mock.calls = test.invites, nil
			resp := resource.DeleteResponse{State: state}
			r.Delete(t.Context(), resource.DeleteRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() != test.blocked {
				t.Fatal("wrong explicit invitation deletion decision", resp.Diagnostics)
			}
			if test.blocked && (!resp.State.Raw.Equal(state.Raw) || resp.Diagnostics[0].Detail() != errRoleInvitationReference.Error()) {
				t.Fatal("refused deletion must retain state with the fixed diagnostic")
			}
			assertRoleInvitationCalls(t, mock, !test.blocked)
			if test.blocked && len(mock.roles) != 2 || !test.blocked && len(mock.roles) != 1 {
				t.Fatal("role changed despite guard decision")
			}
		})
	}
}

func assertRoleInvitationCalls(t *testing.T, mock *roleMock, deleted bool) {
	t.Helper()
	counts := map[string]int{}
	for _, op := range mock.calls {
		counts[op]++
		switch op {
		case "CurrentUser", "CurrentWorkspace", "GetRoles", "FindManyApplications", "FindWorkspaceInvitations":
		case "DeleteOneRole":
			if deleted {
				continue
			}
			fallthrough
		default:
			t.Fatal("unexpected mutation, invitation cancellation, or retry", op)
		}
	}
	if counts["FindWorkspaceInvitations"] != 1 || counts["FindManyApplications"] != 1 || counts["DeleteOneRole"] != map[bool]int{true: 1, false: 0}[deleted] {
		t.Fatal("deletion did not use one fresh safety read and at most one mutation")
	}
}

func TestRoleDeleteInvitationReadFailuresRetainState(t *testing.T) {
	changes := map[string]func(*roleMock){
		"null list": func(m *roleMock) { m.invites = nil },
		"missing list": func(m *roleMock) {
			m.change = func(op string, data map[string]any) {
				if op == "FindWorkspaceInvitations" {
					delete(data, "findWorkspaceInvitations")
				}
			}
		},
		"duplicate ID": func(m *roleMock) {
			m.invites = []map[string]any{roleGuardInvitation(nil), roleGuardInvitation(roleTestOther)}
		},
		"duplicate ID case": func(m *roleMock) {
			a, b := roleGuardInvitation(nil), roleGuardInvitation(roleTestOther)
			b["id"] = strings.ToUpper(a["id"].(string))
			m.invites = []map[string]any{a, b}
		},
	}
	for _, key := range []string{"id", "email", "expiresAt", "roleId"} {
		changes["missing "+key] = func(m *roleMock) {
			invite := roleGuardInvitation(nil)
			delete(invite, key)
			m.invites = []map[string]any{invite}
		}
	}
	for _, key := range []string{"id", "email", "expiresAt", "roleId"} {
		changes["malformed "+key] = func(m *roleMock) {
			invite := roleGuardInvitation(nil)
			invite[key] = "private-invalid-token"
			m.invites = []map[string]any{invite}
		}
	}
	for name, err := range map[string]error{
		"WORKSPACE_MEMBERS unavailable": errors.New("permission denied private-invalid-token"),
		"transport":                     errors.New("transport private-invalid-token"),
		"canceled":                      fmt.Errorf("private-invalid-token: %w", context.Canceled),
		"deadline":                      fmt.Errorf("private-invalid-token: %w", context.DeadlineExceeded),
	} {
		changes[name] = func(m *roleMock) { m.fail["FindWorkspaceInvitations"] = err }
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			mock := newRoleMock()
			change(mock)
			state := roleResourceState(t, roleResourceTestModel())
			resp := resource.DeleteResponse{State: state}
			mockRoleResource(mock).Delete(t.Context(), resource.DeleteRequest{State: state}, &resp)
			if !resp.Diagnostics.HasError() || !resp.State.Raw.Equal(state.Raw) || len(mock.roles) != 2 {
				t.Fatal("unverified invitation read must retain role and state")
			}
			for _, private := range []string{"private-invalid-token", "external-private", "aaaaaaaa-1234"} {
				if strings.Contains(resp.Diagnostics[0].Detail(), private) {
					t.Fatal("invitation failure leaked server data")
				}
			}
			assertRoleInvitationCalls(t, mock, false)
		})
	}
}

type partialInvitationGuardClient struct{ *roleMock }

func (c partialInvitationGuardClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	if err := c.roleMock.MakeRequest(ctx, req, resp); err != nil {
		return err
	}
	if req.OpName == "FindWorkspaceInvitations" {
		resp.Errors = append(resp.Errors, &gqlerror.Error{Message: "private-invalid-token"})
	}
	return nil
}

func TestRoleDeletePartialInvitationDataAndCancellation(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprint("partial=", partial), func(t *testing.T) {
			mock := newRoleMock()
			r := mockRoleResource(mock)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if partial {
				r.client = partialInvitationGuardClient{mock}
			} else {
				mock.change = func(op string, _ map[string]any) {
					if op == "FindWorkspaceInvitations" {
						cancel()
					}
				}
			}
			state := roleResourceState(t, roleResourceTestModel())
			resp := resource.DeleteResponse{State: state}
			r.Delete(ctx, resource.DeleteRequest{State: state}, &resp)
			if !resp.Diagnostics.HasError() || !resp.State.Raw.Equal(state.Raw) || strings.Contains(resp.Diagnostics[0].Detail(), "private-invalid-token") {
				t.Fatal("partial/canceled snapshot must retain role state with a safe diagnostic")
			}
			assertRoleInvitationCalls(t, mock, false)
		})
	}
}

func TestRoleCreateUpdateDoNotReadInvitationsOrApplications(t *testing.T) {
	for _, create := range []bool{false, true} {
		mock := newRoleMock()
		mock.fail["FindWorkspaceInvitations"], mock.fail["FindManyApplications"] = errors.New("unavailable"), errors.New("unavailable")
		r := mockRoleResource(mock)
		model := roleResourceTestModel()
		state := roleResourceState(t, model)
		if create {
			model.ID = types.StringUnknown()
			model.Label = types.StringValue("New role")
			resp := resource.CreateResponse{State: state}
			r.Create(t.Context(), resource.CreateRequest{Plan: roleResourcePlan(t, model)}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatal("create acquired unrelated deletion visibility", resp.Diagnostics)
			}
		} else {
			model.Label = types.StringValue("Updated role")
			resp := resource.UpdateResponse{State: state}
			r.Update(t.Context(), resource.UpdateRequest{State: state, Plan: roleResourcePlan(t, model)}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatal("update acquired unrelated deletion visibility", resp.Diagnostics)
			}
		}
		for _, op := range mock.calls {
			if op == "FindWorkspaceInvitations" || op == "FindManyApplications" {
				t.Fatal("create/update read deletion-only safety lists")
			}
		}
	}
}

func TestRoleDeleteMissingSkipsUnrelatedInvitationRead(t *testing.T) {
	for _, complete := range []bool{false, true} {
		mock := newRoleMock()
		mock.roles = mock.roles[1:]
		mock.fail["FindWorkspaceInvitations"], mock.fail["FindManyApplications"] = errors.New("unavailable"), errors.New("unavailable")
		if !complete {
			mock.fail["GetRoles"] = errors.New("unavailable")
		}
		state := roleResourceState(t, roleResourceTestModel())
		resp := resource.DeleteResponse{State: state}
		mockRoleResource(mock).Delete(t.Context(), resource.DeleteRequest{State: state}, &resp)
		if resp.Diagnostics.HasError() == complete || !resp.State.Raw.Equal(state.Raw) {
			t.Fatal("only a complete role read can authorize absent-role destroy")
		}
		for _, op := range mock.calls {
			if op == "FindWorkspaceInvitations" || op == "FindManyApplications" || op == "DeleteOneRole" {
				t.Fatal("absent role required unrelated edits or reads")
			}
		}
	}
}

func TestRoleInvitationErrorMapping(t *testing.T) {
	for _, err := range []error{errInvalidInvitationResponse, errRoleInvitationReference} {
		var diagnostics diag.Diagnostics
		roleError(&diagnostics, "Delete", fmt.Errorf("private-invalid-token: %w", err))
		if diagnostics[0].Detail() != err.Error() {
			t.Fatal("wrapped invitation errors must use canonical diagnostics")
		}
	}
}
