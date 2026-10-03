// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/oapi-codegen/nullable"
)

var (
	roleUUIDPattern        = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	errInvalidRoleResponse = errors.New("twenty returned malformed or incomplete role data")
)

type roleModel struct {
	ID                            types.String `tfsdk:"id"`
	RoleID                        types.String `tfsdk:"role_id"`
	Label                         types.String `tfsdk:"label"`
	Description                   types.String `tfsdk:"description"`
	Icon                          types.String `tfsdk:"icon"`
	IsEditable                    types.Bool   `tfsdk:"is_editable"`
	CanBeAssignedToUsers          types.Bool   `tfsdk:"can_be_assigned_to_users"`
	CanBeAssignedToAgents         types.Bool   `tfsdk:"can_be_assigned_to_agents"`
	CanBeAssignedToAPIKeys        types.Bool   `tfsdk:"can_be_assigned_to_api_keys"`
	CanUpdateAllSettings          types.Bool   `tfsdk:"can_update_all_settings"`
	CanAccessAllTools             types.Bool   `tfsdk:"can_access_all_tools"`
	CanReadAllObjectRecords       types.Bool   `tfsdk:"can_read_all_object_records"`
	CanUpdateAllObjectRecords     types.Bool   `tfsdk:"can_update_all_object_records"`
	CanSoftDeleteAllObjectRecords types.Bool   `tfsdk:"can_soft_delete_all_object_records"`
	CanDestroyAllObjectRecords    types.Bool   `tfsdk:"can_destroy_all_object_records"`
	PermissionFlags               types.Set    `tfsdk:"permission_flags"`
}

func validRoleUUID(value string) bool {
	return roleUUIDPattern.MatchString(value)
}

func blankRoleLabel(value string) bool {
	return strings.TrimSpace(value) == ""
}

func selectRole(roles []client.GetRolesGetRolesRole, config roleModel) (*client.GetRolesGetRolesRole, int) {
	var selected *client.GetRolesGetRolesRole
	count := 0
	for index := range roles {
		role := &roles[index]
		match := role.Label == config.Label.ValueString()
		if !config.RoleID.IsNull() {
			match = strings.EqualFold(role.Id, config.RoleID.ValueString())
		}
		if match {
			selected = role
			count++
		}
	}
	return selected, count
}

func roleModelFromAPI(ctx context.Context, role client.GetRolesGetRolesRole, selector types.String) roleModel {
	flags := types.SetNull(types.StringType)
	if role.PermissionFlags != nil {
		values := make([]string, 0, len(role.PermissionFlags))
		for _, flag := range role.PermissionFlags {
			values = append(values, flag.Flag)
		}
		// All elements are strings. Validation rejects duplicates before mapping.
		flags, _ = types.SetValueFrom(ctx, types.StringType, values)
	}
	return roleModel{
		ID:                            types.StringValue(role.Id),
		RoleID:                        selector,
		Label:                         types.StringValue(role.Label),
		Description:                   roleNullableString(role.Description),
		Icon:                          roleNullableString(role.Icon),
		IsEditable:                    types.BoolValue(role.IsEditable),
		CanBeAssignedToUsers:          types.BoolValue(role.CanBeAssignedToUsers),
		CanBeAssignedToAgents:         types.BoolValue(role.CanBeAssignedToAgents),
		CanBeAssignedToAPIKeys:        types.BoolValue(role.CanBeAssignedToApiKeys),
		CanUpdateAllSettings:          types.BoolValue(role.CanUpdateAllSettings),
		CanAccessAllTools:             types.BoolValue(role.CanAccessAllTools),
		CanReadAllObjectRecords:       types.BoolValue(role.CanReadAllObjectRecords),
		CanUpdateAllObjectRecords:     types.BoolValue(role.CanUpdateAllObjectRecords),
		CanSoftDeleteAllObjectRecords: types.BoolValue(role.CanSoftDeleteAllObjectRecords),
		CanDestroyAllObjectRecords:    types.BoolValue(role.CanDestroyAllObjectRecords),
		PermissionFlags:               flags,
	}
}

func roleNullableString(value nullable.Nullable[string]) types.String {
	if result, err := value.Get(); err == nil {
		return types.StringValue(result)
	}
	return types.StringNull()
}

// Generated non-null strings and booleans decode JSON null into zero values.
// Validate their presence before decoding into the unchanged generated types.
// This wrapper uses the session's transport, including renewal and redaction.
type roleQueryClient struct {
	graphql.Client
}

func (c roleQueryClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	var raw json.RawMessage
	wireResponse := &graphql.Response{Data: &raw}
	if err := c.Client.MakeRequest(ctx, req, wireResponse); err != nil {
		return err
	}
	if err := validateRoleResponse(raw); err != nil {
		return errInvalidRoleResponse
	}
	resp.Extensions = wireResponse.Extensions
	resp.Errors = wireResponse.Errors
	if err := json.Unmarshal(raw, resp.Data); err != nil {
		return errInvalidRoleResponse
	}
	return nil
}

func validateRoleResponse(raw json.RawMessage) error {
	var data map[string]json.RawMessage
	if err := json.Unmarshal(raw, &data); err != nil {
		return errors.New("invalid role response object")
	}
	var roles []map[string]json.RawMessage
	if err := json.Unmarshal(data["getRoles"], &roles); err != nil || roles == nil {
		return errors.New("missing role list")
	}
	for _, role := range roles {
		var id, label string
		if err := json.Unmarshal(role["id"], &id); err != nil || !validRoleUUID(id) {
			return errors.New("invalid role UUID")
		}
		if err := json.Unmarshal(role["label"], &label); err != nil || blankRoleLabel(label) {
			return errors.New("invalid role label")
		}
		for _, name := range []string{"description", "icon"} {
			var value *string
			if len(role[name]) == 0 || json.Unmarshal(role[name], &value) != nil {
				return errors.New("missing or invalid nullable role string")
			}
		}
		for _, name := range []string{
			"isEditable", "canBeAssignedToUsers", "canBeAssignedToAgents", "canBeAssignedToApiKeys",
			"canUpdateAllSettings", "canAccessAllTools", "canReadAllObjectRecords", "canUpdateAllObjectRecords",
			"canSoftDeleteAllObjectRecords", "canDestroyAllObjectRecords",
		} {
			var value *bool
			if err := json.Unmarshal(role[name], &value); err != nil || value == nil {
				return errors.New("missing or invalid role boolean")
			}
		}
		if err := validateRoleFlags(role["permissionFlags"], id); err != nil {
			return err
		}
	}
	return nil
}

func validateRoleFlags(raw json.RawMessage, roleID string) error {
	if len(raw) == 0 {
		return errors.New("missing permission flag selection")
	}
	var flags []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &flags); err != nil {
		return errors.New("invalid permission flag list")
	}
	keys := make(map[string]bool, len(flags))
	ids := make(map[string]bool, len(flags))
	for _, flag := range flags {
		var id, owner, key string
		if err := json.Unmarshal(flag["id"], &id); err != nil || !validRoleUUID(id) {
			return errors.New("invalid permission flag UUID")
		}
		if err := json.Unmarshal(flag["roleId"], &owner); err != nil || !strings.EqualFold(owner, roleID) {
			return errors.New("permission flag belongs to another role")
		}
		if err := json.Unmarshal(flag["flag"], &key); err != nil || strings.TrimSpace(key) == "" {
			return errors.New("invalid permission flag key")
		}
		if keys[key] || ids[strings.ToLower(id)] {
			return errors.New("duplicate permission flag")
		}
		keys[key] = true
		ids[strings.ToLower(id)] = true
	}
	return nil
}
