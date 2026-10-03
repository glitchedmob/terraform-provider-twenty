// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	errInvalidWorkspaceResponse  = errors.New("twenty returned malformed or incomplete workspace data")
	errWorkspaceIdentityMismatch = errors.New("the current workspace or member no longer matches the configured session identity; reconfigure the provider")
)

// Validate consumed wire fields before genqlient can turn missing/null values
// into zeros. CurrentUser's cached workspace is used only for its identity;
// computed workspace properties always come from CurrentWorkspace.
type workspaceQueryClient struct{ graphql.Client }

func (c workspaceQueryClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	if req.OpName != "CurrentUser" && req.OpName != "CurrentWorkspace" {
		return errInvalidWorkspaceResponse
	}
	var raw json.RawMessage
	wire := &graphql.Response{Data: &raw}
	if err := c.Client.MakeRequest(ctx, req, wire); err != nil {
		return err
	}
	var data map[string]json.RawMessage
	if json.Unmarshal(raw, &data) != nil {
		return errInvalidWorkspaceResponse
	}
	var err error
	switch req.OpName {
	case "CurrentUser":
		err = validateWorkspaceUser(data["currentUser"])
	case "CurrentWorkspace":
		err = validateWorkspaceResponse(data["currentWorkspace"])
	}
	if err != nil || json.Unmarshal(raw, resp.Data) != nil {
		return errInvalidWorkspaceResponse
	}
	resp.Extensions, resp.Errors = wire.Extensions, wire.Errors
	return nil
}

func validateWorkspaceUser(raw json.RawMessage) error {
	var user map[string]json.RawMessage
	if json.Unmarshal(raw, &user) != nil || !workspaceWireUUID(user["id"]) {
		return errInvalidWorkspaceResponse
	}
	for _, field := range []string{"isEmailVerified", "hasPassword", "disabled"} {
		var value *bool
		if json.Unmarshal(user[field], &value) != nil || value == nil {
			return errInvalidWorkspaceResponse
		}
	}
	var email string
	if json.Unmarshal(user["email"], &email) != nil || client.ValidateEmail(email) != nil || strings.TrimSpace(email) == "" {
		return errInvalidWorkspaceResponse
	}
	for field, ids := range map[string][]string{
		"currentWorkspace":     {"id"},
		"workspaceMember":      {"id", "userId", "userWorkspaceId"},
		"currentUserWorkspace": {"id", "userId"},
	} {
		var object map[string]json.RawMessage
		if json.Unmarshal(user[field], &object) != nil {
			return errInvalidWorkspaceResponse
		}
		for _, id := range ids {
			if !workspaceWireUUID(object[id]) {
				return errInvalidWorkspaceResponse
			}
		}
	}
	return nil
}

func validateWorkspaceResponse(raw json.RawMessage) error {
	var workspace map[string]json.RawMessage
	if json.Unmarshal(raw, &workspace) != nil || !workspaceWireUUID(workspace["id"]) {
		return errInvalidWorkspaceResponse
	}
	var status client.WorkspaceActivationStatus
	if json.Unmarshal(workspace["activationStatus"], &status) != nil || !slices.Contains(client.AllWorkspaceActivationStatus, status) {
		return errInvalidWorkspaceResponse
	}
	var subdomain string
	if json.Unmarshal(workspace["subdomain"], &subdomain) != nil || strings.TrimSpace(subdomain) == "" {
		return errInvalidWorkspaceResponse
	}
	for _, field := range []string{"displayName", "customDomain"} {
		var value *string
		if len(workspace[field]) == 0 || json.Unmarshal(workspace[field], &value) != nil {
			return errInvalidWorkspaceResponse
		}
	}
	var urls map[string]json.RawMessage
	if json.Unmarshal(workspace["workspaceUrls"], &urls) != nil {
		return errInvalidWorkspaceResponse
	}
	var subdomainURL string
	var customURL *string
	if json.Unmarshal(urls["subdomainUrl"], &subdomainURL) != nil || !validWorkspaceURL(subdomainURL) ||
		len(urls["customUrl"]) == 0 || json.Unmarshal(urls["customUrl"], &customURL) != nil ||
		(customURL != nil && !validWorkspaceURL(*customURL)) {
		return errInvalidWorkspaceResponse
	}
	var defaultRole map[string]json.RawMessage
	if len(workspace["defaultRole"]) == 0 || json.Unmarshal(workspace["defaultRole"], &defaultRole) != nil ||
		(defaultRole != nil && !workspaceWireUUID(defaultRole["id"])) {
		return errInvalidWorkspaceResponse
	}
	var count *float64
	if len(workspace["workspaceMembersCount"]) == 0 || json.Unmarshal(workspace["workspaceMembersCount"], &count) != nil ||
		(count != nil && (*count < 0 || *count > 9007199254740991 || math.Trunc(*count) != *count)) {
		return errInvalidWorkspaceResponse
	}
	for _, field := range []string{"createdAt", "updatedAt"} {
		var timestamp *time.Time
		if json.Unmarshal(workspace[field], &timestamp) != nil || timestamp == nil || timestamp.IsZero() {
			return errInvalidWorkspaceResponse
		}
	}
	return nil
}

func workspaceWireUUID(raw json.RawMessage) bool {
	var id string
	return json.Unmarshal(raw, &id) == nil && validRoleUUID(id) && id != "00000000-0000-0000-0000-000000000000"
}

func validWorkspaceURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && strings.TrimSpace(value) == value
}

func readCurrentWorkspace(ctx context.Context, api graphql.Client, identity client.Identity) (*client.CurrentWorkspaceCurrentWorkspace, error) {
	// These are configuration-time identity pins, never reconciliation data.
	for _, id := range []string{identity.WorkspaceID, identity.UserID, identity.WorkspaceMemberID, identity.UserWorkspaceID} {
		if !validRoleUUID(id) || id == "00000000-0000-0000-0000-000000000000" {
			return nil, errWorkspaceIdentityMismatch
		}
	}
	guarded := workspaceQueryClient{api}
	user, err := client.CurrentUser(ctx, guarded)
	if err != nil {
		return nil, err
	}
	own, _ := user.CurrentUser.WorkspaceMember.Get() // Required wire fields were validated.
	current, _ := user.CurrentUser.CurrentWorkspace.Get()
	membership, _ := user.CurrentUser.CurrentUserWorkspace.Get()
	ownMembershipID, _ := own.UserWorkspaceId.Get()
	disabled, _ := user.CurrentUser.Disabled.Get()
	if user.CurrentUser.Id != identity.UserID || !strings.EqualFold(user.CurrentUser.Email, identity.Email) ||
		!user.CurrentUser.IsEmailVerified || disabled || !user.CurrentUser.HasPassword ||
		current.Id != identity.WorkspaceID || own.Id != identity.WorkspaceMemberID || own.UserId != identity.UserID ||
		membership.Id != identity.UserWorkspaceID || membership.UserId != identity.UserID ||
		ownMembershipID != identity.UserWorkspaceID {
		return nil, errWorkspaceIdentityMismatch
	}
	workspace, err := client.CurrentWorkspace(ctx, guarded)
	if err != nil {
		return nil, err
	}
	if workspace.CurrentWorkspace.Id != identity.WorkspaceID {
		return nil, errWorkspaceIdentityMismatch
	}
	return &workspace.CurrentWorkspace, nil
}

func workspaceModelFromAPI(workspace client.CurrentWorkspaceCurrentWorkspace) workspaceModel {
	roleID := types.StringNull()
	if role, err := workspace.DefaultRole.Get(); err == nil {
		roleID = types.StringValue(role.Id)
	}
	count := types.Int64Null()
	if value, err := workspace.WorkspaceMembersCount.Get(); err == nil {
		// Wire validation checked integral values and the exact float64 range.
		count = types.Int64Value(int64(value))
	}
	return workspaceModel{
		ID:                    types.StringValue(workspace.Id),
		DisplayName:           roleNullableString(workspace.DisplayName),
		DefaultRoleID:         roleID,
		ActivationStatus:      types.StringValue(string(workspace.ActivationStatus)),
		Subdomain:             types.StringValue(workspace.Subdomain),
		CustomDomain:          roleNullableString(workspace.CustomDomain),
		SubdomainURL:          types.StringValue(workspace.WorkspaceUrls.SubdomainUrl),
		CustomURL:             roleNullableString(workspace.WorkspaceUrls.CustomUrl),
		WorkspaceMembersCount: count,
		CreatedAt:             types.StringValue(workspace.CreatedAt.Format(time.RFC3339Nano)),
		UpdatedAt:             types.StringValue(workspace.UpdatedAt.Format(time.RFC3339Nano)),
	}
}
