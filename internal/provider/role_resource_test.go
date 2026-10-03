// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/oapi-codegen/nullable"
)

const (
	safetyUser      = "44444444-4444-4444-8444-444444444444"
	safetyMember    = "55555555-5555-4555-8555-555555555555"
	safetyRecovery  = "66666666-6666-4666-8666-666666666666"
	safetyWorkspace = "77777777-7777-4777-8777-777777777777"
)

type roleMock struct {
	roles     []map[string]any
	members   []map[string]any
	apps      []map[string]any
	invites   []map[string]any
	own       map[string]any
	defaultID string
	calls     []string
	inputs    map[string]json.RawMessage
	fail      map[string]error
	change    func(string, map[string]any)
}

func newRoleMock() *roleMock {
	role := roleTestFixture(roleTestID, "Managed")
	role["isEditable"], role["canBeAssignedToUsers"], role["universalIdentifier"] = true, true, roleTestID
	admin := roleTestFixture(roleTestOther, "Admin")
	admin["canUpdateAllSettings"], admin["canBeAssignedToUsers"], admin["universalIdentifier"] = true, true, standardAdminRole
	own := map[string]any{"id": safetyMember, "userId": safetyUser, "userWorkspaceId": safetyUser, "roles": []any{map[string]any{"id": roleTestOther}}}
	recovery := map[string]any{"id": safetyRecovery, "userId": safetyRecovery, "userWorkspaceId": safetyRecovery, "roles": []any{map[string]any{"id": roleTestOther}}}
	admin["workspaceMembers"] = []any{own, recovery}
	return &roleMock{roles: []map[string]any{role, admin}, members: []map[string]any{own, recovery}, own: own, apps: []map[string]any{}, invites: []map[string]any{}, defaultID: roleTestOther, inputs: map[string]json.RawMessage{}, fail: map[string]error{}}
}
func (m *roleMock) MakeRequest(_ context.Context, req *graphql.Request, resp *graphql.Response) error {
	m.calls = append(m.calls, req.OpName)
	raw, _ := json.Marshal(req.Variables)
	m.inputs[req.OpName] = raw
	if err := m.fail[req.OpName]; err != nil {
		return err
	}
	var variables map[string]json.RawMessage
	_ = json.Unmarshal(raw, &variables)
	data := map[string]any{}
	switch req.OpName {
	case "GetRoles":
		data["getRoles"] = m.roles
	case "CurrentUser":
		data["currentUser"] = map[string]any{"id": safetyUser, "disabled": false, "isEmailVerified": true, "workspaceMember": m.own, "workspaceMembers": m.members, "currentWorkspace": map[string]any{"id": safetyWorkspace}}
	case "CurrentWorkspace":
		data["currentWorkspace"] = map[string]any{"id": safetyWorkspace, "defaultRole": map[string]any{"id": m.defaultID}}
	case "FindManyApplications":
		data["findManyApplications"] = m.apps
	case "FindWorkspaceInvitations":
		data["findWorkspaceInvitations"] = m.invites
	case "CreateOneRole":
		var input map[string]any
		_ = json.Unmarshal(variables["input"], &input)
		role := roleTestFixture(input["id"].(string), input["label"].(string))
		role["isEditable"], role["universalIdentifier"] = true, input["id"]
		for k, v := range input {
			role[k] = v
		}
		m.roles = append(m.roles, role)
		data["createOneRole"] = role
	case "UpdateOneRole":
		var input struct {
			Id     string
			Update map[string]any
		}
		_ = json.Unmarshal(variables["input"], &input)
		for _, role := range m.roles {
			if role["id"] == input.Id {
				for k, v := range input.Update {
					role[k] = v
				}
				data["updateOneRole"] = role
			}
		}
	case "UpsertPermissionFlags":
		var input client.UpsertPermissionFlagsInput
		_ = json.Unmarshal(variables["input"], &input)
		flags := []any{}
		for i, key := range input.PermissionFlagKeys {
			ids := []string{roleTestFlag, safetyWorkspace, safetyUser}
			flags = append(flags, roleTestPermissionFlag(ids[i%len(ids)], input.RoleId, key))
		}
		for _, role := range m.roles {
			if role["id"] == input.RoleId {
				role["permissionFlags"] = flags
			}
		}
		data["upsertPermissionFlags"] = flags
	case "DeleteOneRole":
		var id string
		_ = json.Unmarshal(variables["roleId"], &id)
		roles := []map[string]any{}
		for _, role := range m.roles {
			if role["id"] != id {
				roles = append(roles, role)
			}
		}
		m.roles = roles
		data["deleteOneRole"] = id
	default:
		return errors.New("unexpected mock operation")
	}
	if m.change != nil {
		m.change(req.OpName, data)
	}
	result, _ := json.Marshal(data)
	return json.Unmarshal(result, resp.Data)
}
func mockRoleResource(m *roleMock) *roleResource {
	return &roleResource{client: m, identity: client.Identity{UserID: safetyUser, WorkspaceMemberID: safetyMember, WorkspaceID: safetyWorkspace}, mutationLock: &sync.Mutex{}}
}
func roleResourceState(t *testing.T, model roleResourceModel) tfsdk.State {
	t.Helper()
	var schema resource.SchemaResponse
	NewRoleResource().Schema(t.Context(), resource.SchemaRequest{}, &schema)
	state := tfsdk.State{Schema: schema.Schema}
	if d := state.Set(t.Context(), &model); d.HasError() {
		t.Fatal(d)
	}
	return state
}
func roleResourcePlan(t *testing.T, model roleResourceModel) tfsdk.Plan {
	state := roleResourceState(t, model)
	return tfsdk.Plan(state)
}
func roleResourceConfig(t *testing.T, model roleResourceModel) tfsdk.Config {
	state := roleResourceState(t, model)
	return tfsdk.Config(state)
}
func roleResourceTestModel() roleResourceModel {
	return roleResourceModel{ID: types.StringValue(roleTestID), Label: types.StringValue("Managed"), Description: types.StringNull(), Icon: types.StringValue(""), IsEditable: types.BoolValue(true), CanBeAssignedToUsers: types.BoolValue(true), CanBeAssignedToAgents: types.BoolValue(false), CanBeAssignedToAPIKeys: types.BoolValue(false), CanUpdateAllSettings: types.BoolValue(false), CanAccessAllTools: types.BoolValue(false), CanReadAllObjectRecords: types.BoolValue(false), CanUpdateAllObjectRecords: types.BoolValue(false), CanSoftDeleteAllObjectRecords: types.BoolValue(false), CanDestroyAllObjectRecords: types.BoolValue(false), PermissionFlags: types.SetValueMust(types.StringType, nil)}
}
func TestRoleResourceSchemaConfigureImport(t *testing.T) {
	r := NewRoleResource().(*roleResource)
	var metadata resource.MetadataResponse
	r.Metadata(t.Context(), resource.MetadataRequest{ProviderTypeName: "twenty"}, &metadata)
	var schema resource.SchemaResponse
	r.Schema(t.Context(), resource.SchemaRequest{}, &schema)
	if metadata.TypeName != "twenty_role" || len(schema.Schema.Attributes) != 15 {
		t.Fatal("missing separate resource schema")
	}
	data := &ClientData{Client: &client.Session{}}
	var configured resource.ConfigureResponse
	r.Configure(t.Context(), resource.ConfigureRequest{ProviderData: data}, &configured)
	if configured.Diagnostics.HasError() || r.mutationLock != &data.MutationLock {
		t.Fatal("resource must share session and mutation lock")
	}
	r.Configure(t.Context(), resource.ConfigureRequest{}, &configured)
	if r.client != nil {
		t.Fatal("schema-only configure must reset client")
	}
	for _, value := range []any{"private-secret", (*ClientData)(nil), &ClientData{}} {
		configured = resource.ConfigureResponse{}
		r.Configure(t.Context(), resource.ConfigureRequest{ProviderData: value}, &configured)
		if !configured.Diagnostics.HasError() || strings.Contains(configured.Diagnostics[0].Detail(), "private-secret") {
			t.Fatal("unsafe configure")
		}
	}
	for _, id := range []string{roleTestID, strings.ToUpper(roleTestID), "name", "", roleTestID + "/" + roleTestOther, " " + roleTestID} {
		resp := resource.ImportStateResponse{State: roleResourceState(t, roleResourceTestModel())}
		r.ImportState(t.Context(), resource.ImportStateRequest{ID: id}, &resp)
		if resp.Diagnostics.HasError() == validRoleUUID(id) {
			t.Fatalf("unexpected import result for %q", id)
		}
	}
}
func TestRoleResourceValidation(t *testing.T) {
	tests := map[string]func(*roleResourceModel){
		"blank label":         func(m *roleResourceModel) { m.Label = types.StringValue(" ") },
		"reserved label":      func(m *roleResourceModel) { m.Label = types.StringValue("gUeSt") },
		"unknown label":       func(m *roleResourceModel) { m.Label = types.StringUnknown() },
		"unknown description": func(m *roleResourceModel) { m.Description = types.StringUnknown() },
		"null label":          func(m *roleResourceModel) { m.Label = types.StringNull() },
		"normalized strings":  func(m *roleResourceModel) { m.Description = types.StringValue("two  spaces") },
		"null flags":          func(m *roleResourceModel) { m.PermissionFlags = types.SetNull(types.StringType) },
		"unknown flags":       func(m *roleResourceModel) { m.PermissionFlags = types.SetUnknown(types.StringType) },
		"null flag": func(m *roleResourceModel) {
			m.PermissionFlags = types.SetValueMust(types.StringType, []attr.Value{types.StringNull()})
		},
		"unknown flag": func(m *roleResourceModel) {
			m.PermissionFlags = types.SetValueMust(types.StringType, []attr.Value{types.StringUnknown()})
		},
		"unsupported flag": func(m *roleResourceModel) {
			m.PermissionFlags = types.SetValueMust(types.StringType, []attr.Value{types.StringValue("roles")})
		},
		"false read true update": func(m *roleResourceModel) { m.CanUpdateAllObjectRecords = types.BoolValue(true) },
		"null boolean":           func(m *roleResourceModel) { m.CanReadAllObjectRecords = types.BoolNull() },
		"unknown boolean":        func(m *roleResourceModel) { m.CanReadAllObjectRecords = types.BoolUnknown() },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			model := roleResourceTestModel()
			change(&model)
			if !model.validate(true).HasError() {
				t.Fatal("invalid apply input accepted")
			}
		})
	}
	model := roleResourceTestModel()
	model.Label = types.StringUnknown()
	if model.validate(false).HasError() {
		t.Fatal("unknown config should defer until apply")
	}
	model = roleResourceTestModel()
	model.CanReadAllObjectRecords = types.BoolUnknown()
	model.CanUpdateAllObjectRecords = types.BoolValue(true)
	if model.validate(false).HasError() {
		t.Fatal("unknown read permission should defer validation")
	}
	model = roleResourceTestModel()
	var validated resource.ValidateConfigResponse
	NewRoleResource().(*roleResource).ValidateConfig(t.Context(), resource.ValidateConfigRequest{Config: roleResourceConfig(t, model)}, &validated)
	if validated.Diagnostics.HasError() {
		t.Fatal(validated.Diagnostics)
	}
	raw, _ := json.Marshal(model.payload())
	var wire map[string]any
	_ = json.Unmarshal(raw, &wire)
	if wire["description"] != nil || wire["icon"] != "" || wire["canReadAllObjectRecords"] != false || wire["canBeAssignedToAgents"] != false {
		t.Fatalf("false/null/empty omitted: %s", raw)
	}
	for _, flag := range client.AllPermissionFlagType {
		model.PermissionFlags = types.SetValueMust(types.StringType, []attr.Value{types.StringValue(string(flag))})
		if model.validate(true).HasError() {
			t.Fatalf("supported flag %s rejected", flag)
		}
	}
}
func TestRoleResourceCRUD(t *testing.T) {
	mock := newRoleMock()
	r := mockRoleResource(mock)
	plan := roleResourceTestModel()
	plan.ID = types.StringUnknown()
	plan.Label = types.StringValue("New role")
	state := roleResourceState(t, plan)
	created := resource.CreateResponse{State: state}
	r.Create(t.Context(), resource.CreateRequest{Plan: roleResourcePlan(t, plan)}, &created)
	if created.Diagnostics.HasError() {
		t.Fatal(created.Diagnostics)
	}
	var model roleResourceModel
	if d := created.State.Get(t.Context(), &model); d.HasError() {
		t.Fatal(d)
	}
	if !validRoleUUID(model.ID.ValueString()) || model.ID.ValueString() == roleTestID {
		t.Fatal("create did not choose a new native UUID")
	}
	var flagWire map[string]map[string]any
	_ = json.Unmarshal(mock.inputs["UpsertPermissionFlags"], &flagWire)
	if !reflect.DeepEqual(flagWire["input"]["permissionFlagKeys"], []any{}) {
		t.Fatal("clearing flags must send [], not null")
	}
	model.Label = types.StringValue("Renamed")
	model.Description = types.StringValue("Description")
	model.PermissionFlags = types.SetValueMust(types.StringType, []attr.Value{types.StringValue("ROLES")})
	updated := resource.UpdateResponse{State: created.State}
	r.Update(t.Context(), resource.UpdateRequest{Plan: roleResourcePlan(t, model), State: created.State}, &updated)
	if updated.Diagnostics.HasError() {
		t.Fatal(updated.Diagnostics)
	}
	read := resource.ReadResponse{State: updated.State}
	r.Read(t.Context(), resource.ReadRequest{State: updated.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatal(read.Diagnostics)
	}
	var got roleResourceModel
	_ = read.State.Get(t.Context(), &got)
	if !got.Label.Equal(model.Label) || !got.PermissionFlags.Equal(model.PermissionFlags) {
		t.Fatal("server re-read did not preserve update")
	}
	deleted := resource.DeleteResponse{State: read.State}
	r.Delete(t.Context(), resource.DeleteRequest{State: read.State}, &deleted)
	if deleted.Diagnostics.HasError() {
		t.Fatal(deleted.Diagnostics)
	}
	missing := resource.ReadResponse{State: read.State}
	r.Read(t.Context(), resource.ReadRequest{State: read.State}, &missing)
	if missing.Diagnostics.HasError() || !missing.State.Raw.IsNull() {
		t.Fatal("confirmed missing role must remove state")
	}
}
func TestRoleResourceReadFailuresRetainState(t *testing.T) {
	for _, operation := range []string{"auth", "server", "malformed", "duplicate", "null flags", "unknown flag"} {
		t.Run(operation, func(t *testing.T) {
			mock := newRoleMock()
			switch operation {
			case "auth", "server":
				mock.fail["GetRoles"] = errors.New("private-token")
			case "malformed":
				mock.roles[0]["canReadAllObjectRecords"] = nil
			case "duplicate":
				mock.roles = append(mock.roles, mock.roles[0])
			case "null flags":
				mock.roles[0]["permissionFlags"] = nil
			case "unknown flag":
				mock.roles[0]["permissionFlags"] = []any{roleTestPermissionFlag(roleTestFlag, roleTestID, "FUTURE_FLAG")}
			}
			state := roleResourceState(t, roleResourceTestModel())
			resp := resource.ReadResponse{State: state}
			mockRoleResource(mock).Read(t.Context(), resource.ReadRequest{State: state}, &resp)
			if !resp.Diagnostics.HasError() || !resp.State.Raw.Equal(state.Raw) || strings.Contains(resp.Diagnostics[0].Detail(), "private-token") {
				t.Fatal("failed read must retain state without leaking errors")
			}
		})
	}
}
func TestRoleResourcePartialFailures(t *testing.T) {
	for _, operation := range []string{"CreateOneRole", "UpsertPermissionFlags", "GetRoles"} {
		t.Run(operation, func(t *testing.T) {
			mock := newRoleMock()
			mock.change = func(op string, _ map[string]any) {
				if op == "CreateOneRole" {
					mock.fail[operation] = errors.New("private-token")
				}
			}
			if operation == "CreateOneRole" {
				mock.fail[operation] = errors.New("private-token")
			}
			model := roleResourceTestModel()
			model.ID = types.StringUnknown()
			resp := resource.CreateResponse{State: roleResourceState(t, model)}
			mockRoleResource(mock).Create(t.Context(), resource.CreateRequest{Plan: roleResourcePlan(t, model)}, &resp)
			var got roleResourceModel
			_ = resp.State.Get(t.Context(), &got)
			if !resp.Diagnostics.HasError() || !validRoleUUID(got.ID.ValueString()) {
				t.Fatal("partial or ambiguous create lost its recoverable ID")
			}
			creates := 0
			for _, op := range mock.calls {
				if op == "CreateOneRole" {
					creates++
				}
			}
			if creates != 1 {
				t.Fatal("mutations must never be blindly retried")
			}
		})
	}
	mock := newRoleMock()
	mock.fail["UpsertPermissionFlags"] = errors.New("private-token")
	model := roleResourceTestModel()
	state := roleResourceState(t, model)
	model.Label = types.StringValue("Partial rename")
	resp := resource.UpdateResponse{State: state}
	mockRoleResource(mock).Update(t.Context(), resource.UpdateRequest{State: state, Plan: roleResourcePlan(t, model)}, &resp)
	var got roleResourceModel
	_ = resp.State.Get(t.Context(), &got)
	if !resp.Diagnostics.HasError() || got.ID.ValueString() != roleTestID || got.Label.ValueString() != "Partial rename" {
		t.Fatal("partial update must retain ID and recovered server state")
	}
}
func TestRoleResourceMutationGuards(t *testing.T) {
	changes := map[string]func(*roleMock){
		"bootstrap current role": func(m *roleMock) {
			m.own["roles"] = []any{map[string]any{"id": roleTestID}}
			m.roles[0]["workspaceMembers"] = []any{m.own}
			m.roles[1]["workspaceMembers"] = []any{m.members[1]}
		},
		"default":                     func(m *roleMock) { m.defaultID = roleTestID },
		"noneditable":                 func(m *roleMock) { m.roles[0]["isEditable"] = false },
		"built-in":                    func(m *roleMock) { m.roles[0]["universalIdentifier"] = standardAdminRole },
		"seeded label":                func(m *roleMock) { m.roles[0]["label"] = "Guest" },
		"duplicate roles":             func(m *roleMock) { m.roles = append(m.roles, m.roles[0]) },
		"missing operator membership": func(m *roleMock) { m.members = m.members[1:] },
		"unexplained role assignment": func(m *roleMock) { m.roles[0]["workspaceMembers"] = []any{map[string]any{"id": safetyWorkspace}} },
		"malformed role assignments":  func(m *roleMock) { delete(m.roles[0], "agents") },
		"malformed member roles":      func(m *roleMock) { m.own["roles"] = nil },
		"foreign workspace": func(m *roleMock) {
			m.change = func(op string, data map[string]any) {
				if op == "CurrentWorkspace" {
					data["currentWorkspace"].(map[string]any)["id"] = roleTestID
				}
			}
		},
		"unknown member role": func(m *roleMock) { m.own["roles"] = []any{map[string]any{"id": safetyWorkspace}} },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			for _, deleting := range []bool{false, true} {
				mock := newRoleMock()
				change(mock)
				assertRoleMutationRejected(t, mock, deleting)
			}
		})
	}
	for name, change := range map[string]func(*roleMock){
		"members": func(m *roleMock) {
			m.roles[0]["workspaceMembers"] = []any{m.members[1]}
			m.members[1]["roles"] = []any{map[string]any{"id": roleTestOther}, map[string]any{"id": roleTestID}}
		},
		"agents":                        func(m *roleMock) { m.roles[0]["agents"] = []any{map[string]any{"id": safetyWorkspace}} },
		"api keys":                      func(m *roleMock) { m.roles[0]["apiKeys"] = []any{map[string]any{"id": safetyWorkspace}} },
		"application":                   func(m *roleMock) { m.apps = []map[string]any{{"id": safetyWorkspace, "defaultRoleId": roleTestID}} },
		"unknown application defaults":  func(m *roleMock) { m.apps = []map[string]any{{"id": safetyWorkspace}} },
		"application visibility denied": func(m *roleMock) { m.fail["FindManyApplications"] = errors.New("private-token") },
	} {
		t.Run("in use "+name, func(t *testing.T) { mock := newRoleMock(); change(mock); assertRoleMutationRejected(t, mock, true) })
	}
}
func assertRoleMutationRejected(t *testing.T, mock *roleMock, deleting bool) {
	t.Helper()
	model := roleResourceTestModel()
	state := roleResourceState(t, model)
	r := mockRoleResource(mock)
	if deleting {
		resp := resource.DeleteResponse{State: state}
		r.Delete(t.Context(), resource.DeleteRequest{State: state}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("unsafe delete accepted")
		}
	} else {
		resp := resource.UpdateResponse{State: state}
		r.Update(t.Context(), resource.UpdateRequest{State: state, Plan: roleResourcePlan(t, model)}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("unsafe update accepted")
		}
	}
	for _, op := range mock.calls {
		if op == "UpdateOneRole" || op == "DeleteOneRole" || op == "UpsertPermissionFlags" {
			t.Fatalf("guard ran after mutation %s", op)
		}
	}
}
func TestRoleAdministratorGuard(t *testing.T) {
	for _, attribute := range []string{"can_update_all_settings", "can_be_assigned_to_users"} {
		for _, alternative := range []bool{false, true} {
			t.Run(attribute+"/independent_alternative="+fmt.Sprint(alternative), func(t *testing.T) {
				mock := newRoleMock()
				mock.roles[0]["canUpdateAllSettings"] = true
				// Admin remains on the operator. Only the recovery user has the
				// editable custom role that Terraform is about to downscope.
				mock.roles[0]["workspaceMembers"] = []any{mock.members[1]}
				mock.roles[1]["workspaceMembers"] = []any{mock.own}
				mock.members[1]["roles"] = []any{map[string]any{"id": roleTestID}}
				if alternative {
					other := map[string]any{"id": safetyWorkspace, "userId": safetyWorkspace, "userWorkspaceId": safetyWorkspace, "roles": []any{map[string]any{"id": roleTestOther}}}
					mock.members = append(mock.members, other)
					mock.roles[1]["workspaceMembers"] = []any{mock.own, other}
				}
				r := mockRoleResource(mock)
				model := roleResourceTestModel()
				model.CanUpdateAllSettings = types.BoolValue(true)
				state := roleResourceState(t, model)
				if attribute == "can_update_all_settings" {
					model.CanUpdateAllSettings = types.BoolValue(false)
				} else {
					model.CanBeAssignedToUsers = types.BoolValue(false)
				}
				resp := resource.UpdateResponse{State: state}
				r.Update(t.Context(), resource.UpdateRequest{State: state, Plan: roleResourcePlan(t, model)}, &resp)
				if resp.Diagnostics.HasError() == alternative {
					t.Fatal("role downscope must preserve a non-operator administrator", resp.Diagnostics)
				}
				mutations := 0
				for _, op := range mock.calls {
					if op == "UpdateOneRole" || op == "UpsertPermissionFlags" {
						mutations++
					}
				}
				if (!alternative && (mutations != 0 || !resp.State.Raw.Equal(state.Raw))) || (alternative && mutations != 2) {
					t.Fatal("unsafe downscope mutated the role, or safe downscope was not applied")
				}
			})
		}
	}
}
func TestRoleSafetyWireValidation(t *testing.T) {
	for _, op := range []string{"CurrentUser", "CurrentWorkspace", "GetRoles", "FindManyApplications"} {
		for _, raw := range []string{"null", "{}", "[]"} {
			api := roleSafetyClient{Client: rawRoleClient{raw: raw}}
			var result any
			if err := api.MakeRequest(t.Context(), &graphql.Request{OpName: op}, &graphql.Response{Data: &result}); err == nil {
				t.Fatalf("accepted malformed %s", op)
			}
		}
	}
}

type rawRoleClient struct{ raw string }

func (c rawRoleClient) MakeRequest(_ context.Context, _ *graphql.Request, resp *graphql.Response) error {
	return json.Unmarshal([]byte(c.raw), resp.Data)
}
func TestRoleNullableInputs(t *testing.T) {
	m := roleResourceTestModel()
	payload := m.payload()
	if !payload.Description.IsNull() {
		t.Fatal("null string must be sent as null")
	}
	if value, err := payload.CanReadAllObjectRecords.Get(); err != nil || value {
		t.Fatal("explicit false must remain specified")
	}
	m.Description = types.StringValue("")
	if got := m.payload().Description; !reflect.DeepEqual(got, nullable.NewNullableWithValue("")) {
		t.Fatal("empty string must stay distinct from null")
	}
}
