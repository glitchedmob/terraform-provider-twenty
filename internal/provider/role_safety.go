// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
)

const standardAdminRole = "20202020-02c2-43f2-b94d-cab1f2b532eb"

// Admin has a pinned universal identifier. Seeded Member and Guest rows can
// have generated IDs, so reserve their labels too, including custom lookalikes.
func reservedRoleLabel(label string) bool {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "admin", "member", "guest":
		return true
	}
	return false
}

var errUnsafeRole = errors.New("role mutation refused: the role is protected, in use, or would remove the final full-settings administrator")

// Validate wire values before genqlient turns missing non-null fields into zeros.
// Only selected Metadata operations are allowed through this guard client.
type roleSafetyClient struct{ graphql.Client }

func (c roleSafetyClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	var raw json.RawMessage
	wire := &graphql.Response{Data: &raw}
	if err := c.Client.MakeRequest(ctx, req, wire); err != nil {
		return err
	}
	var data map[string]json.RawMessage
	if json.Unmarshal(raw, &data) != nil {
		return errInvalidRoleResponse
	}
	var err error
	switch req.OpName {
	case "GetRoles":
		err = validateRoleResponse(raw)
		if err == nil {
			err = validateRoleAssignments(data["getRoles"])
		}
	case "CurrentUser":
		err = validateSafetyMembers(data["currentUser"])
	case "CurrentWorkspace":
		var workspace map[string]json.RawMessage
		if json.Unmarshal(data["currentWorkspace"], &workspace) != nil || !wireUUID(workspace["id"]) {
			err = errInvalidRoleResponse
		} else {
			var role map[string]json.RawMessage
			if json.Unmarshal(workspace["defaultRole"], &role) != nil || !wireUUID(role["id"]) {
				err = errInvalidRoleResponse
			}
		}
	case "FindManyApplications":
		var apps []map[string]json.RawMessage
		if json.Unmarshal(data["findManyApplications"], &apps) != nil || apps == nil {
			err = errInvalidRoleResponse
		} else {
			ids := map[string]bool{}
			for _, app := range apps {
				var id string
				if json.Unmarshal(app["id"], &id) != nil || !validRoleUUID(id) || ids[strings.ToLower(id)] {
					err = errInvalidRoleResponse
					break
				}
				ids[strings.ToLower(id)] = true
				if len(app["defaultRoleId"]) == 0 || (string(app["defaultRoleId"]) != "null" && !wireUUID(app["defaultRoleId"])) {
					err = errInvalidRoleResponse
					break
				}
			}
		}
	default:
		err = errInvalidRoleResponse
	}
	if err != nil || json.Unmarshal(raw, resp.Data) != nil {
		return errInvalidRoleResponse
	}
	resp.Extensions, resp.Errors = wire.Extensions, wire.Errors
	return nil
}
func wireUUID(raw json.RawMessage) bool {
	var id string
	return json.Unmarshal(raw, &id) == nil && validRoleUUID(id)
}
func wireList(raw json.RawMessage) ([]map[string]json.RawMessage, error) {
	var list []map[string]json.RawMessage
	if json.Unmarshal(raw, &list) != nil || list == nil {
		return nil, errInvalidRoleResponse
	}
	ids := map[string]bool{}
	for _, item := range list {
		var id string
		if json.Unmarshal(item["id"], &id) != nil || !validRoleUUID(id) || ids[strings.ToLower(id)] {
			return nil, errInvalidRoleResponse
		}
		ids[strings.ToLower(id)] = true
	}
	return list, nil
}
func validateRoleAssignments(raw json.RawMessage) error {
	roles, err := wireList(raw)
	if err != nil {
		return err
	}
	for _, role := range roles {
		if !wireUUID(role["universalIdentifier"]) || string(role["permissionFlags"]) == "null" {
			return errInvalidRoleResponse
		}
		for _, key := range []string{"workspaceMembers", "agents", "apiKeys"} {
			if _, err := wireList(role[key]); err != nil {
				return err
			}
		}
	}
	return nil
}
func validateSafetyMembers(raw json.RawMessage) error {
	var user map[string]json.RawMessage
	if json.Unmarshal(raw, &user) != nil || !wireUUID(user["id"]) {
		return errInvalidRoleResponse
	}
	if string(user["disabled"]) != "false" || string(user["isEmailVerified"]) != "true" {
		return errInvalidRoleResponse
	}
	members, err := wireList(user["workspaceMembers"])
	if err != nil || len(members) == 0 {
		return errInvalidRoleResponse
	}
	var own map[string]json.RawMessage
	if json.Unmarshal(user["workspaceMember"], &own) != nil || !wireUUID(own["id"]) {
		return errInvalidRoleResponse
	}
	members = append(members, own)
	for _, member := range members {
		if !wireUUID(member["userId"]) || !wireUUID(member["userWorkspaceId"]) {
			return errInvalidRoleResponse
		}
		roles, err := wireList(member["roles"])
		if err != nil || len(roles) == 0 {
			return errInvalidRoleResponse
		}
	}
	return nil
}

type roleSafetySnapshot struct {
	roles         []client.GetRolesGetRolesRole
	user          client.CurrentUserCurrentUser
	defaultRoleID string
	applications  []client.FindManyApplicationsFindManyApplicationsApplication
}

func readRoleSafety(ctx context.Context, api graphql.Client, identity client.Identity) (*roleSafetySnapshot, error) {
	guarded := roleSafetyClient{api}
	user, err := client.CurrentUser(ctx, guarded)
	if err != nil {
		return nil, err
	}
	workspace, err := client.CurrentWorkspace(ctx, guarded)
	if err != nil {
		return nil, err
	}
	roles, err := client.GetRoles(ctx, guarded)
	if err != nil {
		return nil, err
	}
	own, err := user.CurrentUser.WorkspaceMember.Get()
	if err != nil || user.CurrentUser.Id != identity.UserID || own.Id != identity.WorkspaceMemberID || own.UserId != identity.UserID || workspace.CurrentWorkspace.Id != identity.WorkspaceID {
		return nil, errInvalidRoleResponse
	}
	currentWorkspace, err := user.CurrentUser.CurrentWorkspace.Get()
	if err != nil || currentWorkspace.Id != identity.WorkspaceID {
		return nil, errInvalidRoleResponse
	}
	defaultRole, err := workspace.CurrentWorkspace.DefaultRole.Get()
	if err != nil {
		return nil, errInvalidRoleResponse
	}
	s := &roleSafetySnapshot{roles: roles.GetRoles, user: user.CurrentUser, defaultRoleID: defaultRole.Id}
	if s.role(defaultRole.Id) == nil {
		return nil, errInvalidRoleResponse
	}
	ownFound := false
	for _, member := range s.user.WorkspaceMembers {
		if member.Id == own.Id {
			ownFound = true
			if member.UserId != own.UserId || member.UserWorkspaceId.GetOrEmpty() != own.UserWorkspaceId.GetOrEmpty() || !sameRoleIDs(member.Roles, own.Roles) {
				return nil, errInvalidRoleResponse
			}
		}
		for _, role := range member.Roles {
			found := s.role(role.Id)
			if found == nil || !roleHasMember(*found, member.Id) {
				return nil, errInvalidRoleResponse
			}
		}
	}
	if !ownFound {
		return nil, errInvalidRoleResponse
	}
	for _, role := range s.roles {
		for _, assigned := range role.WorkspaceMembers {
			found := false
			for _, member := range s.user.WorkspaceMembers {
				if member.Id == assigned.Id && memberHasRole(member, role.Id) {
					found = true
				}
			}
			if !found {
				return nil, errInvalidRoleResponse
			}
		}
	}
	return s, nil
}
func sameRoleIDs(a, b []client.RoleDetails) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		found := false
		for _, y := range b {
			if x.Id == y.Id {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func (s *roleSafetySnapshot) role(id string) *client.GetRolesGetRolesRole {
	for i := range s.roles {
		if strings.EqualFold(s.roles[i].Id, id) {
			return &s.roles[i]
		}
	}
	return nil
}
func memberHasRole(member client.MemberIdentity, id string) bool {
	for _, role := range member.Roles {
		if role.Id == id {
			return true
		}
	}
	return false
}
func roleHasMember(role client.GetRolesGetRolesRole, id string) bool {
	for _, member := range role.WorkspaceMembers {
		if member.Id == id {
			return true
		}
	}
	return false
}
func (s *roleSafetySnapshot) guard(id string, deleting, canUpdateAllSettings, canBeAssignedToUsers bool) error {
	role := s.role(id)
	if role == nil {
		return nil
	}
	own, err := s.user.WorkspaceMember.Get()
	if err != nil {
		return errInvalidRoleResponse
	}
	if !role.IsEditable || reservedRoleLabel(role.Label) || strings.EqualFold(role.UniversalIdentifier.GetOrEmpty(), standardAdminRole) || strings.EqualFold(id, s.defaultRoleID) || memberHasRole(own, role.Id) {
		return errUnsafeRole
	}
	if deleting {
		// The pinned server lists active API keys, including expired ones, but
		// excludes revoked keys from both this relation and deletion rebinding.
		if len(role.WorkspaceMembers) != 0 || len(role.Agents) != 0 || len(role.ApiKeys) != 0 {
			return errUnsafeRole
		}
		if s.applications == nil {
			return errInvalidRoleResponse
		}
		for _, app := range s.applications {
			if value, err := app.DefaultRoleId.Get(); err == nil && strings.EqualFold(value, id) {
				return errUnsafeRole
			}
		}
	}
	if role.CanUpdateAllSettings && (deleting || !canUpdateAllSettings || !canBeAssignedToUsers) {
		for _, member := range s.user.WorkspaceMembers {
			for _, assigned := range member.Roles {
				other := s.role(assigned.Id)
				if other != nil && !strings.EqualFold(other.Id, id) && other.CanUpdateAllSettings && other.CanBeAssignedToUsers {
					return nil
				}
			}
		}
		return errUnsafeRole
	}
	return nil
}
