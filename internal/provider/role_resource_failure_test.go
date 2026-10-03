// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"errors"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestRoleMalformedMutationResponses(t *testing.T) {
	for _, operation := range []string{"CreateOneRole", "UpdateOneRole", "UpsertPermissionFlags", "DeleteOneRole"} {
		t.Run(operation, func(t *testing.T) {
			mock := newRoleMock()
			mock.change = func(op string, data map[string]any) {
				if op != operation {
					return
				}
				switch op {
				case "CreateOneRole":
					data["createOneRole"] = map[string]any{"id": roleTestOther}
				case "UpdateOneRole":
					data["updateOneRole"] = map[string]any{"id": roleTestOther}
				case "UpsertPermissionFlags":
					data["upsertPermissionFlags"] = nil
				case "DeleteOneRole":
					data["deleteOneRole"] = "success"
				}
			}
			r := mockRoleResource(mock)
			model := roleResourceTestModel()
			state := roleResourceState(t, model)
			switch operation {
			case "CreateOneRole":
				model.ID = types.StringUnknown()
				resp := resource.CreateResponse{State: state}
				r.Create(t.Context(), resource.CreateRequest{Plan: roleResourcePlan(t, model)}, &resp)
				if !resp.Diagnostics.HasError() {
					t.Fatal("unconfirmed create accepted")
				}
				var got roleResourceModel
				_ = resp.State.Get(t.Context(), &got)
				if !validRoleUUID(got.ID.ValueString()) || got.ID.ValueString() == roleTestOther {
					t.Fatal("unconfirmed response must retain the requested UUID, not adopt another ID")
				}
			case "UpdateOneRole", "UpsertPermissionFlags":
				model.Label = types.StringValue("Changed")
				resp := resource.UpdateResponse{State: state}
				r.Update(t.Context(), resource.UpdateRequest{State: state, Plan: roleResourcePlan(t, model)}, &resp)
				if !resp.Diagnostics.HasError() {
					t.Fatal("unconfirmed update accepted")
				}
				var got roleResourceModel
				_ = resp.State.Get(t.Context(), &got)
				if got.ID.ValueString() != roleTestID || got.Label.ValueString() != "Changed" {
					t.Fatal("partial update must recover actual values with stable identity")
				}
			case "DeleteOneRole":
				resp := resource.DeleteResponse{State: state}
				r.Delete(t.Context(), resource.DeleteRequest{State: state}, &resp)
				if !resp.Diagnostics.HasError() || !resp.State.Raw.Equal(state.Raw) {
					t.Fatal("unconfirmed delete must return an error and retain state")
				}
				refreshed := resource.ReadResponse{State: state}
				r.Read(t.Context(), resource.ReadRequest{State: state}, &refreshed)
				if refreshed.Diagnostics.HasError() || !refreshed.State.Raw.IsNull() {
					t.Fatal("a subsequent validated read may confirm deletion")
				}
			}
			count := 0
			for _, call := range mock.calls {
				if call == operation {
					count++
				}
			}
			if count != 1 {
				t.Fatal("malformed responses must not trigger mutation retries")
			}
		})
	}
}
func TestRoleFlagResponseValidation(t *testing.T) {
	for name, flags := range map[string]any{
		"unavailable": nil,
		"wrong set":   []any{roleTestPermissionFlag(roleTestFlag, roleTestID, "WORKSPACE_MEMBERS")},
		"wrong owner": []any{roleTestPermissionFlag(roleTestFlag, roleTestOther, "ROLES")},
		"missing ID":  []any{roleTestPermissionFlag("", roleTestID, "ROLES")},
		"duplicate":   []any{roleTestPermissionFlag(roleTestFlag, roleTestID, "ROLES"), roleTestPermissionFlag(safetyUser, roleTestID, "ROLES")},
	} {
		t.Run(name, func(t *testing.T) {
			mock := newRoleMock()
			mock.change = func(op string, data map[string]any) {
				if op == "UpsertPermissionFlags" {
					data["upsertPermissionFlags"] = flags
				}
			}
			model := roleResourceTestModel()
			model.PermissionFlags, _ = types.SetValueFrom(t.Context(), types.StringType, []string{"ROLES"})
			if err := mockRoleResource(mock).writeFlags(t.Context(), model); err == nil {
				t.Fatal("malformed or inconsistent flag response accepted")
			}
		})
	}
}
func TestRoleMissingMutationTargets(t *testing.T) {
	mock := newRoleMock()
	mock.roles = mock.roles[1:]
	r := mockRoleResource(mock)
	model := roleResourceTestModel()
	state := roleResourceState(t, model)
	deleted := resource.DeleteResponse{State: state}
	r.Delete(t.Context(), resource.DeleteRequest{State: state}, &deleted)
	if deleted.Diagnostics.HasError() {
		t.Fatal("deleting a confirmed absent role should be a no-op")
	}
	updated := resource.UpdateResponse{State: state}
	r.Update(t.Context(), resource.UpdateRequest{State: state, Plan: roleResourcePlan(t, model)}, &updated)
	if !updated.Diagnostics.HasError() {
		t.Fatal("updating an absent role must not recreate it")
	}
	for _, op := range mock.calls {
		if op == "DeleteOneRole" || op == "UpdateOneRole" {
			t.Fatal("mutation sent for absent role")
		}
	}
}
func TestRoleUnknownApplyAndUnconfiguredClient(t *testing.T) {
	mock := newRoleMock()
	r := mockRoleResource(mock)
	model := roleResourceTestModel()
	model.Icon = types.StringUnknown()
	created := resource.CreateResponse{State: roleResourceState(t, model)}
	r.Create(t.Context(), resource.CreateRequest{Plan: roleResourcePlan(t, model)}, &created)
	if !created.Diagnostics.HasError() || len(mock.calls) != 0 {
		t.Fatal("unknown apply input reached server")
	}
	model = roleResourceTestModel()
	state := roleResourceState(t, model)
	r = &roleResource{}
	read := resource.ReadResponse{State: state}
	r.Read(t.Context(), resource.ReadRequest{State: state}, &read)
	if !read.Diagnostics.HasError() || !read.State.Raw.Equal(state.Raw) {
		t.Fatal("missing client must not remove state")
	}
}
func TestRoleCreateDoesNotAdoptExistingLabel(t *testing.T) {
	mock := newRoleMock()
	mock.fail["CreateOneRole"] = errors.New("label already exists private-token")
	model := roleResourceTestModel()
	model.ID = types.StringUnknown()
	resp := resource.CreateResponse{State: roleResourceState(t, model)}
	mockRoleResource(mock).Create(t.Context(), resource.CreateRequest{Plan: roleResourcePlan(t, model)}, &resp)
	var got roleResourceModel
	_ = resp.State.Get(t.Context(), &got)
	if !resp.Diagnostics.HasError() || got.ID.ValueString() == roleTestID || !validRoleUUID(got.ID.ValueString()) {
		t.Fatal("create adopted an existing label instead of requiring import")
	}
}
func TestRolePartialUpdateReadFailure(t *testing.T) {
	mock := newRoleMock()
	mock.change = func(op string, _ map[string]any) {
		if op == "UpdateOneRole" {
			mock.fail["GetRoles"] = errors.New("private-token")
		}
	}
	model := roleResourceTestModel()
	state := roleResourceState(t, model)
	model.Description = types.StringValue("Changed")
	resp := resource.UpdateResponse{State: state}
	mockRoleResource(mock).Update(t.Context(), resource.UpdateRequest{State: state, Plan: roleResourcePlan(t, model)}, &resp)
	if !resp.Diagnostics.HasError() || !resp.State.Raw.Equal(state.Raw) {
		t.Fatal("failed final read must retain the existing ID and state")
	}
}
