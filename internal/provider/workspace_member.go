// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	errInvalidMemberResponse = errors.New("twenty returned malformed, ambiguous, or incomplete membership data; no unrelated invitation was changed")
	errMemberIdentity        = errors.New("membership identity does not match the configured workspace or is the protected operator identity")
	errMemberMissing         = errors.New("there is no member or invitation for this email to import")
	errMemberRole            = errors.New("the requested role does not exist in this workspace or cannot be assigned to users")
	errMemberExists          = errors.New("access already exists for this email; import it explicitly before managing its role")
	errUnsafeMember          = errors.New("membership mutation refused: preserve the final workspace member, a full-settings administrator, and an independent full-settings recovery administrator outside the operator")
	errMemberRace            = errors.New("membership changed during the operation; newly accepted access was not removed or reassigned. Refresh and review the next plan before retrying")
	errMemberOwnership       = errors.New("membership ownership was not confirmed. Inspect the remaining access and explicitly import this compound ID before updating or destroying it; no automatic adoption or resend is allowed")
	errMemberPartial         = errors.New("membership operation was not confirmed. The stable ID and any readable remaining access were retained. Inspect refreshed state and test mail delivery before retrying; no automatic resend or rollback was attempted")
)

func normalizedMemberEmail(email string) bool {
	return email != "" && email == strings.ToLower(strings.TrimSpace(email)) && client.ValidateEmail(email) == nil && !strings.Contains(email, "/")
}
func memberImportID(workspace, email string) string { return workspace + "/" + email }
func validMemberUUID(value string) bool {
	return validRoleUUID(value) && value != "00000000-0000-0000-0000-000000000000"
}
func parseMemberID(id string) (string, string, error) {
	parts := strings.Split(id, "/")
	if len(parts) != 2 || !validMemberUUID(parts[0]) || strings.ToLower(parts[0]) != parts[0] || !normalizedMemberEmail(parts[1]) {
		return "", "", errMemberIdentity
	}
	return parts[0], parts[1], nil
}

// Validate fields before the generated decoder can turn omitted/null values into
// zero values. No invitation token or link is selected by these operations.
type memberQueryClient struct{ graphql.Client }

func (c memberQueryClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	var raw json.RawMessage
	wire := &graphql.Response{Data: &raw}
	if err := c.Client.MakeRequest(ctx, req, wire); err != nil {
		return err
	}
	var data map[string]json.RawMessage
	if json.Unmarshal(raw, &data) != nil {
		return errInvalidMemberResponse
	}
	var err error
	switch req.OpName {
	case "CurrentUser":
		err = validateWorkspaceUser(data["currentUser"])
		if err == nil {
			err = validateSafetyMembers(data["currentUser"])
		}
		if err == nil {
			var user map[string]json.RawMessage
			_ = json.Unmarshal(data["currentUser"], &user)
			members, e := wireList(user["workspaceMembers"])
			if e != nil {
				err = e
			} else {
				for _, member := range members {
					var email string
					if json.Unmarshal(member["userEmail"], &email) != nil || client.ValidateEmail(email) != nil || strings.TrimSpace(email) != email || email == "" {
						err = errInvalidMemberResponse
						break
					}
				}
			}
		}
	case "GetRoles":
		err = validateRoleResponse(raw)
		if err == nil {
			err = validateRoleAssignments(data["getRoles"])
		}
	case "CurrentWorkspace":
		var workspace, role map[string]json.RawMessage
		if json.Unmarshal(data["currentWorkspace"], &workspace) != nil || !workspaceWireUUID(workspace["id"]) || json.Unmarshal(workspace["defaultRole"], &role) != nil || !workspaceWireUUID(role["id"]) {
			err = errInvalidMemberResponse
		}
	case "FindWorkspaceInvitations":
		err = validateInvitations(data["findWorkspaceInvitations"])
	case "SendInvitations":
		var payload map[string]json.RawMessage
		if json.Unmarshal(data["sendInvitations"], &payload) != nil {
			err = errInvalidMemberResponse
			break
		}
		var success *bool
		var messages []string
		if json.Unmarshal(payload["success"], &success) != nil || success == nil || json.Unmarshal(payload["errors"], &messages) != nil || messages == nil {
			err = errInvalidMemberResponse
			break
		}
		err = validateInvitations(payload["result"])
	case "DeleteWorkspaceInvitation":
		var result string
		if json.Unmarshal(data["deleteWorkspaceInvitation"], &result) != nil || (result != "success" && result != "error") {
			err = errInvalidMemberResponse
		}
	case "UpdateWorkspaceMemberRole", "DeleteUserFromWorkspace":
		key := "updateWorkspaceMemberRole"
		if req.OpName == "DeleteUserFromWorkspace" {
			key = "deleteUserFromWorkspace"
		}
		var result map[string]json.RawMessage
		if json.Unmarshal(data[key], &result) != nil || !workspaceWireUUID(result["id"]) || !workspaceWireUUID(result["userId"]) {
			err = errInvalidMemberResponse
		}
	default:
		err = errInvalidMemberResponse
	}
	if err != nil || json.Unmarshal(raw, resp.Data) != nil {
		return errInvalidMemberResponse
	}
	resp.Extensions, resp.Errors = wire.Extensions, wire.Errors
	return nil
}
func validateInvitations(raw json.RawMessage) error {
	invitations, err := wireList(raw)
	if err != nil {
		return err
	}
	for _, invite := range invitations {
		var email string
		var expires *time.Time
		if !workspaceWireUUID(invite["id"]) || json.Unmarshal(invite["email"], &email) != nil || client.ValidateEmail(email) != nil || email == "" || strings.TrimSpace(email) != email ||
			json.Unmarshal(invite["expiresAt"], &expires) != nil || expires == nil || expires.IsZero() || len(invite["roleId"]) == 0 || (string(invite["roleId"]) != "null" && !workspaceWireUUID(invite["roleId"])) {
			return errInvalidMemberResponse
		}
	}
	return nil
}

type memberSnapshot struct {
	*roleSafetySnapshot
	invitations []client.Invitation
}

func readMemberSnapshot(ctx context.Context, api graphql.Client, identity client.Identity) (*memberSnapshot, error) {
	guarded := memberQueryClient{api}
	safety, err := readRoleSafety(ctx, guarded, identity)
	if err != nil {
		return nil, err
	}
	own, _ := safety.user.WorkspaceMember.Get()
	membership, err := safety.user.CurrentUserWorkspace.Get()
	if err != nil || !safety.user.HasPassword || !strings.EqualFold(safety.user.Email, identity.Email) || own.UserWorkspaceId.GetOrEmpty() != identity.UserWorkspaceID || membership.Id != identity.UserWorkspaceID || membership.UserId != identity.UserID {
		return nil, errMemberIdentity
	}
	emails, users, memberships := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, member := range safety.user.WorkspaceMembers {
		email := strings.ToLower(member.UserEmail)
		userWorkspace := member.UserWorkspaceId.GetOrEmpty()
		if email == "" || emails[email] || users[member.UserId] || memberships[userWorkspace] || !validMemberUUID(member.Id) || !validMemberUUID(member.UserId) || !validMemberUUID(userWorkspace) {
			return nil, errInvalidMemberResponse
		}
		emails[email], users[member.UserId], memberships[userWorkspace] = true, true, true
	}
	for _, role := range safety.roles {
		if !validMemberUUID(role.Id) || !validMemberUUID(role.UniversalIdentifier.GetOrEmpty()) {
			return nil, errInvalidMemberResponse
		}
	}
	invitations, err := client.FindWorkspaceInvitations(ctx, guarded)
	if err != nil {
		return nil, err
	}
	return &memberSnapshot{safety, invitations.FindWorkspaceInvitations}, nil
}

type memberAccess struct {
	member     *client.MemberIdentity
	invitation *client.Invitation
	roleID     string
	status     string
}

func (s *memberSnapshot) access(email string) (*memberAccess, error) {
	a := &memberAccess{status: "absent"}
	for i := range s.user.WorkspaceMembers {
		member := &s.user.WorkspaceMembers[i]
		if strings.EqualFold(member.UserEmail, email) {
			if a.member != nil || len(member.Roles) != 1 {
				return nil, errInvalidMemberResponse
			}
			a.member, a.roleID, a.status = member, member.Roles[0].Id, "accepted"
		}
	}
	for i := range s.invitations {
		invite := &s.invitations[i]
		if !strings.EqualFold(invite.Email, email) {
			continue
		}
		if a.invitation != nil {
			return nil, errInvalidMemberResponse
		}
		a.invitation = invite
	}
	if a.member != nil {
		// Acceptance normally consumes the invitation. A remaining row is ambiguous,
		// even when expired; do not cancel it as part of accepted-member management.
		if a.invitation != nil {
			return nil, errInvalidMemberResponse
		}
	} else if a.invitation != nil {
		a.roleID = a.invitation.RoleId.GetOrEmpty()
		if a.roleID == "" {
			a.roleID = s.defaultRoleID
		}
		if s.role(a.roleID) == nil {
			return nil, errInvalidMemberResponse
		}
		a.status = "pending"
		if !a.invitation.ExpiresAt.After(time.Now()) {
			a.status = "expired"
		}
	}
	return a, nil
}
func (s *memberSnapshot) operatorGuard(email string, a *memberAccess, identity client.Identity) error {
	if strings.EqualFold(email, identity.Email) || strings.EqualFold(email, s.user.Email) {
		return errMemberIdentity
	}
	if a.member != nil && (a.member.Id == identity.WorkspaceMemberID || a.member.UserId == identity.UserID || a.member.UserWorkspaceId.GetOrEmpty() == identity.UserWorkspaceID) {
		return errMemberIdentity
	}
	return nil
}
func (s *memberSnapshot) guardMutation(a *memberAccess, roleID string, deleting bool, identity client.Identity) error {
	if !deleting {
		role := s.role(roleID)
		if role == nil || !role.CanBeAssignedToUsers {
			return errMemberRole
		}
	}
	remaining, admins, independent := 0, 0, 0
	for _, member := range s.user.WorkspaceMembers {
		target := a.member != nil && a.member.Id == member.Id
		if target && deleting {
			continue
		}
		remaining++
		full := false
		if target {
			role := s.role(roleID)
			full = role != nil && role.CanBeAssignedToUsers && role.CanUpdateAllSettings
		} else {
			for _, assigned := range member.Roles {
				role := s.role(assigned.Id)
				if role != nil && role.CanBeAssignedToUsers && role.CanUpdateAllSettings {
					full = true
				}
			}
		}
		if full {
			admins++
			if member.UserId != identity.UserID && member.Id != identity.WorkspaceMemberID {
				independent++
			}
		}
	}
	if remaining == 0 || admins == 0 || independent == 0 {
		return errUnsafeMember
	}
	return nil
}

const memberReplacementSummary = "Twenty Membership Ownership Changed"
const memberReplacementDetail = "The native invitation or member ID changed for this email. Ownership confirmation was cleared; the replacement access was not adopted. Inspect the current access and match its role in configuration, remove only the local state binding, then explicitly import the compound ID before any update or destroy."

func (m workspaceMemberModel) replacedBy(a *memberAccess) bool {
	memberID, invitationID := m.MemberID.ValueString(), m.InvitationID.ValueString()
	return (memberID != "" && (a.invitation != nil || (a.member != nil && a.member.Id != memberID))) ||
		(invitationID != "" && a.invitation != nil && a.invitation.Id != invitationID)
}

// Acceptance consumes the original invitation and creates a member, so that
// transition keeps ownership. Replacement of an already known native ID does
// not. An unconfirmed binding is never confirmed by reconciliation.
func (m *workspaceMemberModel) reconcileAccess(a *memberAccess) bool {
	replaced := m.replacedBy(a)
	m.fromAccess(a)
	if replaced || a.status == "absent" {
		m.OwnershipConfirmed = types.BoolValue(false)
	}
	return replaced
}

func (m *workspaceMemberModel) fromAccess(a *memberAccess) {
	m.MemberID, m.InvitationID, m.ExpiresAt = types.StringNull(), types.StringNull(), types.StringNull()
	m.Status = types.StringValue(a.status)
	if a.member != nil {
		m.MemberID = types.StringValue(a.member.Id)
	}
	if a.invitation != nil {
		m.InvitationID = types.StringValue(a.invitation.Id)
		m.ExpiresAt = types.StringValue(a.invitation.ExpiresAt.Format(time.RFC3339Nano))
	}
	if a.roleID != "" {
		m.RoleID = types.StringValue(a.roleID)
	}
}
