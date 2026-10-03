// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const memberEmail = "declared@example.test"
const memberNativeID = "88888888-8888-4888-8888-888888888888"
const memberUserID = "99999999-9999-4999-8999-999999999999"
const memberMembershipID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

type memberMock struct {
	*roleMock
	invites  []map[string]any
	before   func(string)
	after    func(string, map[string]any)
	lateFail map[string]error
}

func newMemberMock() *memberMock {
	m := &memberMock{roleMock: newRoleMock(), invites: []map[string]any{}}
	m.own["userEmail"] = "operator@example.test"
	m.members[1]["userEmail"] = "recovery@example.test"
	return m
}
func (m *memberMock) rebind() {
	for _, role := range m.roles {
		assigned := []any{}
		for _, member := range m.members {
			for _, assignedRole := range member["roles"].([]any) {
				if assignedRole.(map[string]any)["id"] == role["id"] {
					assigned = append(assigned, member)
				}
			}
		}
		role["workspaceMembers"] = assigned
	}
}
func (m *memberMock) invite(email, role string) {
	m.invites = append(m.invites, map[string]any{"id": roleTestFlag, "email": email, "roleId": role, "expiresAt": time.Now().Add(time.Hour).Format(time.RFC3339Nano)})
}
func (m *memberMock) accept() {
	role := m.invites[0]["roleId"]
	m.members = append(m.members, map[string]any{"id": memberNativeID, "userId": memberUserID, "userWorkspaceId": memberMembershipID, "userEmail": m.invites[0]["email"], "roles": []any{map[string]any{"id": role}}})
	m.invites = []map[string]any{}
	m.rebind()
}
func (m *memberMock) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	if m.before != nil {
		m.before(req.OpName)
	}
	data := map[string]any{}
	raw, _ := json.Marshal(req.Variables)
	vars := map[string]json.RawMessage{}
	_ = json.Unmarshal(raw, &vars)
	str := func(key string) string { var s string; _ = json.Unmarshal(vars[key], &s); return s }
	switch req.OpName {
	case "FindWorkspaceInvitations", "SendInvitations", "DeleteWorkspaceInvitation", "UpdateWorkspaceMemberRole", "DeleteUserFromWorkspace":
		m.calls = append(m.calls, req.OpName)
		m.inputs[req.OpName] = raw
		if err := m.fail[req.OpName]; err != nil {
			return err
		}
		switch req.OpName {
		case "FindWorkspaceInvitations":
			data["findWorkspaceInvitations"] = m.invites
		case "SendInvitations":
			var emails []string
			_ = json.Unmarshal(vars["emails"], &emails)
			m.invite(emails[0], str("roleId"))
			data["sendInvitations"] = map[string]any{"success": true, "errors": []string{}, "result": m.invites}
		case "DeleteWorkspaceInvitation":
			result := "error"
			for i, invitation := range m.invites {
				if invitation["id"] == str("appTokenId") {
					m.invites = append(m.invites[:i], m.invites[i+1:]...)
					result = "success"
					break
				}
			}
			data["deleteWorkspaceInvitation"] = result
		case "UpdateWorkspaceMemberRole":
			for _, member := range m.members {
				if member["id"] == str("workspaceMemberId") {
					member["roles"] = []any{map[string]any{"id": str("roleId")}}
					data["updateWorkspaceMemberRole"] = member
				}
			}
			m.rebind()
		case "DeleteUserFromWorkspace":
			for i, member := range m.members {
				if member["id"] == str("workspaceMemberIdToDelete") {
					data["deleteUserFromWorkspace"] = map[string]any{"id": member["userWorkspaceId"], "userId": member["userId"], "deletedAt": nil}
					m.members = append(m.members[:i], m.members[i+1:]...)
					break
				}
			}
			m.rebind()
		}
	default:
		var base json.RawMessage
		if err := m.roleMock.MakeRequest(ctx, req, &graphql.Response{Data: &base}); err != nil {
			return err
		}
		_ = json.Unmarshal(base, &data)
		if req.OpName == "CurrentUser" {
			user := data["currentUser"].(map[string]any)
			user["email"], user["hasPassword"] = "operator@example.test", true
			user["currentUserWorkspace"] = map[string]any{"id": safetyUser, "userId": safetyUser}
		}
	}
	if m.after != nil {
		m.after(req.OpName, data)
	}
	encoded, _ := json.Marshal(data)
	if err := m.lateFail[req.OpName]; err != nil {
		return err
	}
	return json.Unmarshal(encoded, resp.Data)
}
func mockMemberResource(m *memberMock) *workspaceMemberResource {
	return &workspaceMemberResource{client: m, mutationLock: &sync.Mutex{}, identity: client.Identity{WorkspaceID: safetyWorkspace, UserID: safetyUser, WorkspaceMemberID: safetyMember, UserWorkspaceID: safetyUser, Email: "operator@example.test"}}
}
func memberModel() workspaceMemberModel {
	return workspaceMemberModel{ID: types.StringValue(memberImportID(safetyWorkspace, memberEmail)), Email: types.StringValue(memberEmail), RoleID: types.StringValue(roleTestID), WorkspaceID: types.StringValue(safetyWorkspace), MemberID: types.StringNull(), InvitationID: types.StringNull(), Status: types.StringValue("unconfirmed"), ExpiresAt: types.StringNull(), OwnershipConfirmed: types.BoolValue(true)}
}
func memberState(t *testing.T, model workspaceMemberModel) tfsdk.State {
	t.Helper()
	var schema resource.SchemaResponse
	NewWorkspaceMemberResource().Schema(t.Context(), resource.SchemaRequest{}, &schema)
	state := tfsdk.State{Schema: schema.Schema}
	if d := state.Set(t.Context(), &model); d.HasError() {
		t.Fatal(d)
	}
	return state
}
func memberPlan(t *testing.T, model workspaceMemberModel) tfsdk.Plan {
	return tfsdk.Plan(memberState(t, model))
}
func getMemberState(t *testing.T, state tfsdk.State) workspaceMemberModel {
	t.Helper()
	var m workspaceMemberModel
	if d := state.Get(t.Context(), &m); d.HasError() {
		t.Fatal(d)
	}
	return m
}
func memberImport(t *testing.T, r *workspaceMemberResource, email string) tfsdk.State {
	t.Helper()
	resp := resource.ImportStateResponse{State: memberState(t, memberModel())}
	r.ImportState(t.Context(), resource.ImportStateRequest{ID: memberImportID(safetyWorkspace, email)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	return resp.State
}
func TestMemberSchemaConfigAndValidation(t *testing.T) {
	r := NewWorkspaceMemberResource().(*workspaceMemberResource)
	var md resource.MetadataResponse
	r.Metadata(t.Context(), resource.MetadataRequest{ProviderTypeName: "twenty"}, &md)
	var schema resource.SchemaResponse
	r.Schema(t.Context(), resource.SchemaRequest{}, &schema)
	if md.TypeName != "twenty_workspace_member" || len(schema.Schema.Attributes) != 9 || !schema.Schema.Attributes["email"].IsRequired() || !schema.Schema.Attributes["workspace_id"].IsComputed() {
		t.Fatal("unexpected membership schema")
	}
	data := &ClientData{Client: &client.Session{}}
	var configured resource.ConfigureResponse
	r.Configure(t.Context(), resource.ConfigureRequest{ProviderData: data}, &configured)
	if configured.Diagnostics.HasError() || r.mutationLock != &data.MutationLock {
		t.Fatal("must share IAM lock")
	}
	r.Configure(t.Context(), resource.ConfigureRequest{}, &configured)
	if r.client != nil || r.identity.UserID != "" {
		t.Fatal("schema configure must reset session")
	}
	for _, bad := range []any{"private-secret", (*ClientData)(nil), &ClientData{}} {
		resp := resource.ConfigureResponse{}
		r.Configure(t.Context(), resource.ConfigureRequest{ProviderData: bad}, &resp)
		if !resp.Diagnostics.HasError() || strings.Contains(resp.Diagnostics[0].Detail(), "private-secret") {
			t.Fatal("unsafe configure")
		}
	}
	for _, email := range []string{"", " Declared@example.test", "Declared@example.test", "name <declared@example.test>", "é@example.test", "a/b@example.test", "declared@example.test\n"} {
		m := memberModel()
		m.Email = types.StringValue(email)
		if !m.validate(true).HasError() {
			t.Fatal("invalid email accepted")
		}
	}
	for _, id := range []string{"", "role-name", "00000000-0000-0000-0000-000000000000", memberMembershipID + "/bad", strings.ToUpper(memberMembershipID)} {
		m := memberModel()
		m.RoleID = types.StringValue(id)
		if !m.validate(true).HasError() {
			t.Fatal("invalid role accepted")
		}
	}
	m := memberModel()
	m.Email, m.RoleID = types.StringUnknown(), types.StringUnknown()
	if m.validate(false).HasError() || !m.validate(true).HasError() {
		t.Fatal("unknown validation must defer until mutation")
	}
	var validated resource.ValidateConfigResponse
	r.ValidateConfig(t.Context(), resource.ValidateConfigRequest{Config: tfsdk.Config(memberState(t, memberModel()))}, &validated)
	if validated.Diagnostics.HasError() {
		t.Fatal(validated.Diagnostics)
	}
	state := memberState(t, memberModel())
	created := resource.CreateResponse{State: state}
	r.Create(t.Context(), resource.CreateRequest{Plan: memberPlan(t, memberModel())}, &created)
	read := resource.ReadResponse{State: state}
	r.Read(t.Context(), resource.ReadRequest{State: state}, &read)
	updated := resource.UpdateResponse{State: state}
	r.Update(t.Context(), resource.UpdateRequest{State: state, Plan: memberPlan(t, memberModel())}, &updated)
	deleted := resource.DeleteResponse{State: state}
	r.Delete(t.Context(), resource.DeleteRequest{State: state}, &deleted)
	if !created.Diagnostics.HasError() || !read.Diagnostics.HasError() || !updated.Diagnostics.HasError() || !deleted.Diagnostics.HasError() {
		t.Fatal("unconfigured lifecycle accepted")
	}
}
func TestMemberLifecycleAndDrift(t *testing.T) {
	mock := newMemberMock()
	r := mockMemberResource(mock)
	m := memberModel()
	created := resource.CreateResponse{State: memberState(t, m)}
	r.Create(t.Context(), resource.CreateRequest{Plan: memberPlan(t, m)}, &created)
	if created.Diagnostics.HasError() {
		t.Fatal(created.Diagnostics)
	}
	m = getMemberState(t, created.State)
	if m.Status.ValueString() != "pending" || m.InvitationID.ValueString() != roleTestFlag || !m.MemberID.IsNull() {
		t.Fatal("create should return pending")
	}
	if strings.Contains(string(mock.inputs["SendInvitations"]), "password") {
		t.Fatal("provider must not create credentials")
	}
	plan := m
	plan.RoleID = types.StringValue(roleTestOther)
	updated := resource.UpdateResponse{State: created.State}
	r.Update(t.Context(), resource.UpdateRequest{Plan: memberPlan(t, plan), State: created.State}, &updated)
	if updated.Diagnostics.HasError() {
		t.Fatal(updated.Diagnostics)
	}
	m = getMemberState(t, updated.State)
	if m.RoleID.ValueString() != roleTestOther || m.ID.ValueString() != memberImportID(safetyWorkspace, memberEmail) {
		t.Fatal("pending reissue lost identity or role")
	}
	mock.accept()
	read := resource.ReadResponse{State: updated.State}
	r.Read(t.Context(), resource.ReadRequest{State: updated.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatal(read.Diagnostics)
	}
	m = getMemberState(t, read.State)
	if m.Status.ValueString() != "accepted" || m.MemberID.ValueString() != memberNativeID || !m.InvitationID.IsNull() || !m.ExpiresAt.IsNull() || m.ID.ValueString() != plan.ID.ValueString() {
		t.Fatal("acceptance must preserve compound identity")
	}
	plan = m
	plan.RoleID = types.StringValue(roleTestID)
	updated = resource.UpdateResponse{State: read.State}
	r.Update(t.Context(), resource.UpdateRequest{Plan: memberPlan(t, plan), State: read.State}, &updated)
	if updated.Diagnostics.HasError() {
		t.Fatal(updated.Diagnostics)
	}
	mock.members[2]["roles"] = []any{map[string]any{"id": roleTestOther}}
	mock.rebind()
	read = resource.ReadResponse{State: updated.State}
	r.Read(t.Context(), resource.ReadRequest{State: updated.State}, &read)
	if read.Diagnostics.HasError() || getMemberState(t, read.State).RoleID.ValueString() != roleTestOther {
		t.Fatal("external role drift was hidden")
	}
	deleted := resource.DeleteResponse{State: read.State}
	r.Delete(t.Context(), resource.DeleteRequest{State: read.State}, &deleted)
	if deleted.Diagnostics.HasError() || len(mock.members) != 2 {
		t.Fatal(deleted.Diagnostics)
	}
	read = resource.ReadResponse{State: read.State}
	r.Read(t.Context(), resource.ReadRequest{State: read.State}, &read)
	if read.Diagnostics.HasError() || !read.State.Raw.IsNull() {
		t.Fatal("confirmed disappearance should remove state")
	}
}
func TestMemberExplicitImportsAndExpiredReplacement(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		mock := newMemberMock()
		mock.invite(memberEmail, roleTestID)
		if accepted {
			mock.accept()
		}
		r := mockMemberResource(mock)
		m := memberModel()
		created := resource.CreateResponse{State: memberState(t, m)}
		r.Create(t.Context(), resource.CreateRequest{Plan: memberPlan(t, m)}, &created)
		if !created.Diagnostics.HasError() || strings.Contains(strings.Join(mock.calls, ","), "SendInvitations") {
			t.Fatal("existing access silently adopted")
		}
		state := memberImport(t, r, memberEmail)
		imported := getMemberState(t, state)
		if imported.RoleID.ValueString() != roleTestID {
			t.Fatal("import must read external role without changing it")
		}
	}
	for _, id := range []string{"", "label", safetyWorkspace + "/Declared@example.test", safetyWorkspace + "/declared@example.test/extra", memberNativeID + "/" + memberEmail, "00000000-0000-0000-0000-000000000000/" + memberEmail, safetyWorkspace + "/operator@example.test"} {
		mock := newMemberMock()
		r := mockMemberResource(mock)
		resp := resource.ImportStateResponse{State: memberState(t, memberModel())}
		r.ImportState(t.Context(), resource.ImportStateRequest{ID: id}, &resp)
		if !resp.Diagnostics.HasError() || len(mock.calls) != 0 {
			t.Fatal("invalid/foreign/operator import reached server")
		}
	}
	mock := newMemberMock()
	mock.invite(memberEmail, roleTestID)
	mock.invites[0]["expiresAt"] = time.Now().Add(-time.Hour).Format(time.RFC3339Nano)
	r := mockMemberResource(mock)
	state := memberImport(t, r, memberEmail)
	m := getMemberState(t, state)
	if m.Status.ValueString() != "expired" {
		t.Fatal("expired access reported active")
	}
	var plan resource.ModifyPlanResponse
	r.ModifyPlan(t.Context(), resource.ModifyPlanRequest{State: state, Plan: memberPlan(t, m)}, &plan)
	if plan.Diagnostics.HasError() || len(plan.RequiresReplace) != 1 {
		t.Fatal("expiration must plan replacement")
	}
	deleted := resource.DeleteResponse{State: state}
	r.Delete(t.Context(), resource.DeleteRequest{State: state}, &deleted)
	if deleted.Diagnostics.HasError() || len(mock.invites) != 0 {
		t.Fatal("expired revocation failed")
	}
}
func TestMemberSafetyGuards(t *testing.T) {
	for name, change := range map[string]func(*memberMock){
		"operator email":    func(m *memberMock) { m.invite("operator@example.test", roleTestID) },
		"unassignable role": func(m *memberMock) { m.roles[0]["canBeAssignedToUsers"] = false },
		"no recovery":       func(m *memberMock) { m.members = m.members[:1]; m.rebind() },
		"missing role":      func(m *memberMock) { m.roles = m.roles[1:] },
	} {
		t.Run(name, func(t *testing.T) {
			mock := newMemberMock()
			change(mock)
			r := mockMemberResource(mock)
			m := memberModel()
			if name == "operator email" {
				m.Email = types.StringValue("operator@example.test")
			}
			resp := resource.CreateResponse{State: memberState(t, m)}
			r.Create(t.Context(), resource.CreateRequest{Plan: memberPlan(t, m)}, &resp)
			if !resp.Diagnostics.HasError() || strings.Contains(strings.Join(mock.calls, ","), "SendInvitations") {
				t.Fatal("unsafe invite accepted")
			}
		})
	}
	// Recovery is the only independent full-settings administrator. The operator
	// still has full settings, but this is not enough to permit recovery removal.
	mock := newMemberMock()
	r := mockMemberResource(mock)
	state := memberImport(t, r, "recovery@example.test")
	deleted := resource.DeleteResponse{State: state}
	r.Delete(t.Context(), resource.DeleteRequest{State: state}, &deleted)
	if !deleted.Diagnostics.HasError() {
		t.Fatal("independent recovery deletion allowed")
	}
	m := getMemberState(t, state)
	m.RoleID = types.StringValue(roleTestID)
	updated := resource.UpdateResponse{State: state}
	r.Update(t.Context(), resource.UpdateRequest{State: state, Plan: memberPlan(t, m)}, &updated)
	if !updated.Diagnostics.HasError() || strings.Contains(strings.Join(mock.calls, ","), "UpdateWorkspaceMemberRole") {
		t.Fatal("independent recovery downscope allowed")
	}
	// A renamed email cannot evade the operator user/member identity guard.
	mock.own["userEmail"] = memberEmail
	imported := resource.ImportStateResponse{State: memberState(t, memberModel())}
	r.ImportState(t.Context(), resource.ImportStateRequest{ID: memberImportID(safetyWorkspace, memberEmail)}, &imported)
	if !imported.Diagnostics.HasError() {
		t.Fatal("operator member-ID bypass accepted")
	}
	// Direct simulated final-member/final-admin snapshots test the defensive guard
	// independently of the operator refusal that blocks those cases in practice.
	snapshot, err := readMemberSnapshot(t.Context(), mock, r.identity)
	if err != nil {
		t.Fatal(err)
	}
	a, err := snapshot.access("recovery@example.test")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.user.WorkspaceMembers = []client.MemberIdentity{*a.member}
	if snapshot.guardMutation(a, "", true, r.identity) == nil || snapshot.guardMutation(a, roleTestID, false, r.identity) == nil {
		t.Fatal("final member/admin guard allowed removal or downscope")
	}
}
func TestMemberMalformedAndFailedReadsPreserveState(t *testing.T) {
	changes := map[string]func(string, map[string]any){
		"null invitations": func(op string, d map[string]any) {
			if op == "FindWorkspaceInvitations" {
				d["findWorkspaceInvitations"] = nil
			}
		},
		"lossy invitations": func(op string, d map[string]any) {
			if op == "FindWorkspaceInvitations" {
				d["findWorkspaceInvitations"] = []any{map[string]any{"id": roleTestFlag}}
			}
		},
		"duplicate invitations": func(op string, d map[string]any) {
			if op == "FindWorkspaceInvitations" {
				d["findWorkspaceInvitations"] = []any{map[string]any{"id": roleTestFlag, "email": memberEmail, "roleId": roleTestID, "expiresAt": time.Now().Add(time.Hour)}, map[string]any{"id": memberNativeID, "email": memberEmail, "roleId": roleTestID, "expiresAt": time.Now().Add(time.Hour)}}
			}
		},
		"null members": func(op string, d map[string]any) {
			if op == "CurrentUser" {
				d["currentUser"].(map[string]any)["workspaceMembers"] = nil
			}
		},
		"duplicate members": func(op string, d map[string]any) {
			if op == "CurrentUser" {
				u := d["currentUser"].(map[string]any)
				list := u["workspaceMembers"].([]any)
				u["workspaceMembers"] = append(list, list[0])
			}
		},
		"missing email": func(op string, d map[string]any) {
			if op == "CurrentUser" {
				delete(d["currentUser"].(map[string]any), "email")
			}
		},
		"foreign workspace": func(op string, d map[string]any) {
			if op == "CurrentWorkspace" {
				d["currentWorkspace"].(map[string]any)["id"] = memberNativeID
			}
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			mock := newMemberMock()
			mock.invite(memberEmail, roleTestID)
			r := mockMemberResource(mock)
			state := memberImport(t, r, memberEmail)
			mock.after = change
			read := resource.ReadResponse{State: state}
			r.Read(t.Context(), resource.ReadRequest{State: state}, &read)
			if !read.Diagnostics.HasError() || !read.State.Raw.Equal(state.Raw) {
				t.Fatal("malformed read lost state")
			}
			deleted := resource.DeleteResponse{State: state}
			r.Delete(t.Context(), resource.DeleteRequest{State: state}, &deleted)
			if !deleted.Diagnostics.HasError() || strings.Contains(strings.Join(mock.calls, ","), "DeleteWorkspaceInvitation") {
				t.Fatal("malformed result caused cancellation")
			}
		})
	}
	for _, op := range []string{"CurrentUser", "CurrentWorkspace", "GetRoles", "FindWorkspaceInvitations"} {
		for _, err := range []error{errors.New("FORBIDDEN private-secret"), &graphql.HTTPError{StatusCode: 503}} {
			mock := newMemberMock()
			mock.invite(memberEmail, roleTestID)
			r := mockMemberResource(mock)
			state := memberImport(t, r, memberEmail)
			mock.fail[op] = err
			read := resource.ReadResponse{State: state}
			r.Read(t.Context(), resource.ReadRequest{State: state}, &read)
			if !read.Diagnostics.HasError() || !read.State.Raw.Equal(state.Raw) || strings.Contains(read.Diagnostics[0].Detail(), "private-secret") {
				t.Fatal("failed read changed state or leaked data")
			}
		}
	}
}
