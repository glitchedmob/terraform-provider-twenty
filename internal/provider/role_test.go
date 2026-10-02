// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestRoleResponseRequiredBooleans(t *testing.T) {
	t.Parallel()
	for _, field := range []string{
		"isEditable", "canBeAssignedToUsers", "canBeAssignedToAgents", "canBeAssignedToApiKeys",
		"canUpdateAllSettings", "canAccessAllTools", "canReadAllObjectRecords", "canUpdateAllObjectRecords",
		"canSoftDeleteAllObjectRecords", "canDestroyAllObjectRecords",
	} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			for _, invalid := range []string{"missing", "null", "wrong type"} {
				role := roleTestFixture(roleTestID, "Admin")
				switch invalid {
				case "missing":
					delete(role, field)
				case "null":
					role[field] = nil
				case "wrong type":
					role[field] = "false"
				}
				body := map[string]any{"data": map[string]any{"getRoles": []any{role}}}
				d := roleTestDataSource(t, body)
				response := roleTestRead(t, d, types.StringValue(roleTestID), types.StringNull())
				if !response.Diagnostics.HasError() || !response.State.Raw.IsNull() {
					t.Errorf("%s %s must not become false in state", invalid, field)
				}
			}
		})
	}
}

func TestRoleResponseMalformedValues(t *testing.T) {
	t.Parallel()
	for name, change := range map[string]func(map[string]any){
		"null ID":             func(role map[string]any) { role["id"] = nil },
		"missing ID":          func(role map[string]any) { delete(role, "id") },
		"invalid ID":          func(role map[string]any) { role["id"] = "private-invalid-id" },
		"null label":          func(role map[string]any) { role["label"] = nil },
		"missing label":       func(role map[string]any) { delete(role, "label") },
		"blank label":         func(role map[string]any) { role["label"] = " \n" },
		"invalid description": func(role map[string]any) { role["description"] = false },
		"invalid icon":        func(role map[string]any) { role["icon"] = 12 },
		"missing flags":       func(role map[string]any) { delete(role, "permissionFlags") },
		"null flag":           func(role map[string]any) { role["permissionFlags"] = []any{nil} },
		"null flag ID": func(role map[string]any) {
			role["permissionFlags"] = []any{roleTestPermissionFlag("", roleTestID, "ROLES")}
		},
		"wrong flag owner": func(role map[string]any) {
			role["permissionFlags"] = []any{roleTestPermissionFlag(roleTestFlag, roleTestOther, "ROLES")}
		},
		"blank flag key": func(role map[string]any) {
			role["permissionFlags"] = []any{roleTestPermissionFlag(roleTestFlag, roleTestID, " ")}
		},
		"duplicate flag key": func(role map[string]any) {
			role["permissionFlags"] = []any{roleTestPermissionFlag(roleTestFlag, roleTestID, "ROLES"), roleTestPermissionFlag(roleTestOther, roleTestID, "ROLES")}
		},
		"contradictory flag ID": func(role map[string]any) {
			role["permissionFlags"] = []any{roleTestPermissionFlag(roleTestFlag, roleTestID, "ROLES"), roleTestPermissionFlag(roleTestFlag, roleTestID, "WORKSPACE_MEMBERS")}
		},
		"wrong flag list type": func(role map[string]any) { role["permissionFlags"] = "private-token" },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			role := roleTestFixture(roleTestID, "Admin")
			change(role)
			d := roleTestDataSource(t, map[string]any{"data": map[string]any{"getRoles": []any{role}}})
			response := roleTestRead(t, d, types.StringValue(roleTestID), types.StringNull())
			if !response.Diagnostics.HasError() || !response.State.Raw.IsNull() {
				t.Fatal("invalid server value must not enter state")
			}
			if strings.Contains(response.Diagnostics[0].Detail(), "private-") {
				t.Fatal("diagnostic leaked malformed server values")
			}
		})
	}
}

func TestRoleResponseMalformedJSON(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"{", `null`, `[]`, `{}`, `{"getRoles":{}}`} {
		if err := validateRoleResponse(json.RawMessage(raw)); err == nil {
			t.Errorf("invalid response %s was accepted", raw)
		}
	}
}

func TestRoleUnknownFlagCompatibility(t *testing.T) {
	t.Parallel()
	role := roleTestFixture(roleTestID, "Admin")
	// The selected SDL field is String, not PermissionFlagType. Do not silently
	// discard a future server flag or claim that globals forbid explicit flags.
	role["canUpdateAllSettings"] = true
	role["permissionFlags"] = []any{roleTestPermissionFlag(roleTestFlag, roleTestID, "FUTURE_SETTINGS_FLAG")}
	d := roleTestDataSource(t, map[string]any{"data": map[string]any{"getRoles": []any{role}}})
	response := roleTestRead(t, d, types.StringValue(roleTestID), types.StringNull())
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	var state roleModel
	if diagnostics := response.State.Get(t.Context(), &state); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	var flags []string
	if diagnostics := state.PermissionFlags.ElementsAs(t.Context(), &flags, false); diagnostics.HasError() || len(flags) != 1 || flags[0] != "FUTURE_SETTINGS_FLAG" {
		t.Fatal("unknown server flag was discarded")
	}
}
