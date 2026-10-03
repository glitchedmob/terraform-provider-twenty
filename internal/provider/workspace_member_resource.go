// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/Khan/genqlient/graphql"
	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/oapi-codegen/nullable"
)

var (
	_ resource.Resource                   = &workspaceMemberResource{}
	_ resource.ResourceWithConfigure      = &workspaceMemberResource{}
	_ resource.ResourceWithValidateConfig = &workspaceMemberResource{}
	_ resource.ResourceWithImportState    = &workspaceMemberResource{}
	_ resource.ResourceWithModifyPlan     = &workspaceMemberResource{}
)

type workspaceMemberResource struct {
	client       graphql.Client
	identity     client.Identity
	mutationLock *sync.Mutex
}
type workspaceMemberModel struct {
	ID                 types.String `tfsdk:"id"`
	Email              types.String `tfsdk:"email"`
	RoleID             types.String `tfsdk:"role_id"`
	WorkspaceID        types.String `tfsdk:"workspace_id"`
	MemberID           types.String `tfsdk:"member_id"`
	InvitationID       types.String `tfsdk:"invitation_id"`
	Status             types.String `tfsdk:"status"`
	ExpiresAt          types.String `tfsdk:"expires_at"`
	OwnershipConfirmed types.Bool   `tfsdk:"ownership_confirmed"`
}

func NewWorkspaceMemberResource() resource.Resource { return &workspaceMemberResource{} }
func (r *workspaceMemberResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workspace_member"
}
func (r *workspaceMemberResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages access for one declared email in the provider's current workspace through Metadata GraphQL. Create sends one invitation without waiting for login. Existing access requires import. Requires ROLES and WORKSPACE_MEMBERS. Never manages the operator or global credentials. Preserve an independent recovery administrator outside Terraform and pause competing IAM writers during apply.",
		Attributes: map[string]schema.Attribute{
			"id":                  schema.StringAttribute{Computed: true, MarkdownDescription: "Stable identity and import format: lowercase workspace UUID/email. Remains unchanged when an invitation is accepted.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"email":               schema.StringAttribute{Required: true, MarkdownDescription: "One bare ASCII mailbox, explicitly lowercase with no surrounding spaces. Changing it replaces access. Existing members or invitations must be imported; create never adopts them.", PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"role_id":             schema.StringAttribute{Required: true, MarkdownDescription: "Explicit lowercase UUID of a user-assignable role in this workspace. Built-in/default roles may be assigned. Accepted updates replace the member's role; pending updates revoke and reissue the invitation."},
			"workspace_id":        schema.StringAttribute{Computed: true, MarkdownDescription: "Authenticated workspace UUID. This is not a selector.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"member_id":           schema.StringAttribute{Computed: true, MarkdownDescription: "Workspace-member UUID after acceptance, otherwise null. Not the global user or user-workspace ID."},
			"invitation_id":       schema.StringAttribute{Computed: true, MarkdownDescription: "Native invitation UUID while pending or expired, otherwise null. No invitation token or link is stored."},
			"status":              schema.StringAttribute{Computed: true, MarkdownDescription: "pending, accepted, or expired on successful reads. Expired invitations plan replacement. unconfirmed or absent can remain after a failed mutation until refresh confirms the remaining access."},
			"expires_at":          schema.StringAttribute{Computed: true, MarkdownDescription: "Invitation expiration in RFC3339 format, otherwise null. Expired invitations do not count as active desired access."},
			"ownership_confirmed": schema.BoolAttribute{Computed: true, MarkdownDescription: "True after explicit import or a confirmed invitation send. False after an ambiguous send requires inspection and explicit import before further writes, even when refresh can see pending or accepted access. Prevents accidental adoption of a concurrent external invitation."},
		},
	}
}
func (r *workspaceMemberResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client, r.mutationLock, r.identity = nil, nil, client.Identity{}
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*ClientData)
	if !ok || data == nil || data.Client == nil {
		resp.Diagnostics.AddError("Invalid Twenty Client", "The membership resource did not receive an authenticated Twenty session.")
		return
	}
	r.client, r.identity, r.mutationLock = data.Client.Client(), data.Client.Identity(), &data.MutationLock
}
func (m workspaceMemberModel) validate(known bool) diag.Diagnostics {
	var d diag.Diagnostics
	if m.Email.IsUnknown() {
		if known {
			d.AddAttributeError(path.Root("email"), "Unknown Twenty Member Email", "Email must be known before mutation.")
		}
	} else if !normalizedMemberEmail(m.Email.ValueString()) {
		d.AddAttributeError(path.Root("email"), "Invalid Twenty Member Email", "Use one bare ASCII email address, explicitly lowercase without surrounding spaces or a slash.")
	}
	if m.RoleID.IsUnknown() {
		if known {
			d.AddAttributeError(path.Root("role_id"), "Unknown Twenty Member Role", "Role ID must be known before mutation.")
		}
	} else if !validRoleUUID(m.RoleID.ValueString()) || m.RoleID.ValueString() == "00000000-0000-0000-0000-000000000000" || m.RoleID.ValueString() != strings.ToLower(m.RoleID.ValueString()) {
		d.AddAttributeError(path.Root("role_id"), "Invalid Twenty Member Role", "Use an explicit lowercase, nonzero role UUID.")
	}
	return d
}
func (r *workspaceMemberResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var model workspaceMemberModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(model.validate(false)...)
	}
}
func (r *workspaceMemberResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var state workspaceMemberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if !resp.Diagnostics.HasError() && state.Status.ValueString() == "expired" {
		resp.RequiresReplace = append(resp.RequiresReplace, path.Root("id"))
	}
}
func memberError(d *diag.Diagnostics, action string, err error) {
	detail := client.DiagnosticMessage(err)
	for _, safe := range []error{errInvalidMemberResponse, errMemberIdentity, errMemberExists, errUnsafeMember, errMemberRace, errMemberPartial, errMemberRole, errMemberOwnership, errMemberMissing} {
		if errors.Is(err, safe) {
			detail = safe.Error()
			break
		}
	}
	d.AddError("Unable to "+action+" Twenty Workspace Member", detail)
}
func (r *workspaceMemberResource) ready(d *diag.Diagnostics) bool {
	if r.client == nil || r.mutationLock == nil {
		d.AddError("Twenty Client Not Configured", "Configure the Twenty provider before managing workspace membership.")
		return false
	}
	return true
}
func (r *workspaceMemberResource) scoped(m workspaceMemberModel) error {
	workspace, email, err := parseMemberID(m.ID.ValueString())
	if err != nil || workspace != r.identity.WorkspaceID || m.WorkspaceID.ValueString() != workspace || m.Email.ValueString() != email || strings.EqualFold(email, r.identity.Email) || m.MemberID.ValueString() == r.identity.WorkspaceMemberID {
		return errMemberIdentity
	}
	return nil
}
func (r *workspaceMemberResource) snapshot(ctx context.Context, m workspaceMemberModel) (*memberSnapshot, *memberAccess, error) {
	if err := r.scoped(m); err != nil {
		return nil, nil, err
	}
	s, err := readMemberSnapshot(ctx, r.client, r.identity)
	if err != nil {
		return nil, nil, err
	}
	a, err := s.access(m.Email.ValueString())
	if err == nil {
		err = s.operatorGuard(m.Email.ValueString(), a, r.identity)
	}
	return s, a, err
}
func (r *workspaceMemberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan workspaceMemberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(plan.validate(true)...)
	if resp.Diagnostics.HasError() || !r.ready(&resp.Diagnostics) {
		return
	}
	plan.ID = types.StringValue(memberImportID(r.identity.WorkspaceID, plan.Email.ValueString()))
	plan.WorkspaceID = types.StringValue(r.identity.WorkspaceID)
	plan.MemberID, plan.InvitationID, plan.ExpiresAt = types.StringNull(), types.StringNull(), types.StringNull()
	plan.Status = types.StringValue("unconfirmed")
	plan.OwnershipConfirmed = types.BoolValue(false)
	r.mutationLock.Lock()
	defer r.mutationLock.Unlock()
	s, a, err := r.snapshot(ctx, plan)
	if err == nil && a.status != "absent" {
		err = errMemberExists
	}
	if err == nil {
		err = s.guardMutation(a, plan.RoleID.ValueString(), false, r.identity)
	}
	if err != nil {
		memberError(&resp.Diagnostics, "Create", err)
		return
	}
	// Persist the compound identity even if the server commits the invitation but
	// mail delivery or the response fails. Never retry sending automatically.
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	err = r.send(ctx, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if err != nil {
		memberError(&resp.Diagnostics, "Create", err)
		resp.Diagnostics.AddError("Membership Recovery Required", errMemberPartial.Error())
	}
}
func (r *workspaceMemberResource) send(ctx context.Context, model *workspaceMemberModel) error {
	model.OwnershipConfirmed = types.BoolValue(false)
	desiredRole := model.RoleID.ValueString()
	response, mutationErr := client.SendInvitations(ctx, memberQueryClient{r.client}, []string{model.Email.ValueString()}, nullable.NewNullableWithValue(desiredRole))
	if mutationErr == nil {
		payload := response.SendInvitations
		if !payload.Success || len(payload.Errors) != 0 || len(payload.Result) != 1 || payload.Result[0].Email != model.Email.ValueString() || payload.Result[0].RoleId.GetOrEmpty() != desiredRole {
			mutationErr = errMemberPartial
		}
	}
	_, a, readErr := r.snapshot(ctx, *model)
	if readErr == nil {
		model.fromAccess(a)
		if a.status != "pending" || a.roleID != desiredRole {
			readErr = errMemberRace
		}
		if mutationErr == nil && response.SendInvitations.Result[0].Id != model.InvitationID.ValueString() {
			readErr = errInvalidMemberResponse
		}
	}
	if mutationErr != nil {
		return mutationErr
	}
	if readErr == nil {
		model.OwnershipConfirmed = types.BoolValue(true)
	}
	return readErr
}
func (r *workspaceMemberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state workspaceMemberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || !r.ready(&resp.Diagnostics) {
		return
	}
	r.mutationLock.Lock()
	defer r.mutationLock.Unlock()
	_, a, err := r.snapshot(ctx, state)
	if err != nil {
		memberError(&resp.Diagnostics, "Read", err)
		return
	}
	if a.status == "absent" {
		resp.State.RemoveResource(ctx)
		return
	}
	state.fromAccess(a)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
func (r *workspaceMemberResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state workspaceMemberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(plan.validate(true)...)
	if resp.Diagnostics.HasError() || !r.ready(&resp.Diagnostics) {
		return
	}
	if !state.OwnershipConfirmed.ValueBool() {
		memberError(&resp.Diagnostics, "Update", errMemberOwnership)
		return
	}
	if !plan.ID.Equal(state.ID) || !plan.Email.Equal(state.Email) {
		memberError(&resp.Diagnostics, "Update", errMemberIdentity)
		return
	}
	r.mutationLock.Lock()
	defer r.mutationLock.Unlock()
	s, a, err := r.snapshot(ctx, state)
	if err == nil {
		err = s.guardMutation(a, plan.RoleID.ValueString(), false, r.identity)
	}
	if err != nil {
		memberError(&resp.Diagnostics, "Update", err)
		return
	}
	if a.status == "absent" || a.status == "expired" {
		memberError(&resp.Diagnostics, "Update", errMemberRace)
		return
	}
	desired := plan.RoleID.ValueString()
	if a.member != nil {
		if state.Status.ValueString() != "accepted" || state.MemberID.ValueString() != a.member.Id {
			state.fromAccess(a)
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			memberError(&resp.Diagnostics, "Update", errMemberRace)
			return
		}
		if a.roleID != desired {
			changed, changeErr := client.UpdateWorkspaceMemberRole(ctx, memberQueryClient{r.client}, a.member.Id, desired)
			err = changeErr
			if err == nil && (changed.UpdateWorkspaceMemberRole.Id != a.member.Id || changed.UpdateWorkspaceMemberRole.UserId != a.member.UserId) {
				err = errInvalidMemberResponse
			}
		}
	} else {
		if state.InvitationID.ValueString() != a.invitation.Id {
			memberError(&resp.Diagnostics, "Update", errMemberRace)
			return
		}
		if a.roleID != desired {
			originalInvitationID := a.invitation.Id
			a, err = r.cancel(ctx, state, a)
			state.fromAccess(a)
			if a.invitation != nil && a.invitation.Id != originalInvitationID {
				state.OwnershipConfirmed = types.BoolValue(false)
			}
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			if err == nil {
				// Cancellation and reissue are separate server operations. Revalidate the
				// operator, target role and recovery administrators before the second write.
				s, a, err = r.snapshot(ctx, state)
				if err == nil && a.status != "absent" {
					err = errMemberRace
				}
				if err == nil {
					err = s.guardMutation(a, desired, false, r.identity)
				}
				if err == nil {
					state.RoleID = plan.RoleID
					err = r.send(ctx, &state)
				}
			}
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			if err != nil {
				memberError(&resp.Diagnostics, "Update", err)
				resp.Diagnostics.AddError("Membership Recovery Required", errMemberPartial.Error())
				return
			}
		}
	}
	_, actual, readErr := r.snapshot(ctx, state)
	if readErr == nil {
		state.fromAccess(actual)
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		if actual.roleID != desired || (actual.status != "accepted" && actual.status != "pending") {
			readErr = errMemberPartial
		}
	}
	if err != nil {
		memberError(&resp.Diagnostics, "Update", err)
		return
	}
	if readErr != nil {
		memberError(&resp.Diagnostics, "Update", readErr)
	}
}

// cancel only the validated target invitation. A concurrent acceptance never
// falls through to member deletion or role reassignment, even on "error".
func (r *workspaceMemberResource) cancel(ctx context.Context, state workspaceMemberModel, before *memberAccess) (*memberAccess, error) {
	revoked, mutationErr := client.DeleteWorkspaceInvitation(ctx, memberQueryClient{r.client}, before.invitation.Id)
	if mutationErr == nil && revoked.DeleteWorkspaceInvitation != "success" {
		mutationErr = errMemberPartial
	}
	_, after, readErr := r.snapshot(ctx, state)
	if readErr != nil {
		return before, readErr
	}
	if after.member != nil {
		return after, errMemberRace
	}
	if mutationErr != nil {
		return after, mutationErr
	}
	if after.status != "absent" {
		return after, errMemberPartial
	}
	return after, nil
}
func (r *workspaceMemberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state workspaceMemberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || !r.ready(&resp.Diagnostics) {
		return
	}
	r.mutationLock.Lock()
	defer r.mutationLock.Unlock()
	s, a, err := r.snapshot(ctx, state)
	if err == nil {
		err = s.guardMutation(a, "", true, r.identity)
	}
	if err != nil {
		memberError(&resp.Diagnostics, "Delete", err)
		return
	}
	if a.status == "absent" {
		return
	}
	if !state.OwnershipConfirmed.ValueBool() {
		memberError(&resp.Diagnostics, "Delete", errMemberOwnership)
		return
	}
	if a.invitation != nil {
		if state.InvitationID.ValueString() != a.invitation.Id {
			memberError(&resp.Diagnostics, "Delete", errMemberRace)
			return
		}
		_, err = r.cancel(ctx, state, a)
	} else {
		if state.Status.ValueString() != "accepted" || state.MemberID.ValueString() != a.member.Id {
			memberError(&resp.Diagnostics, "Delete", errMemberRace)
			return
		}
		removed, mutationErr := client.DeleteUserFromWorkspace(ctx, memberQueryClient{r.client}, a.member.Id)
		err = mutationErr
		// The server returns the pre-deletion userWorkspace entity, not member ID.
		if err == nil && (removed.DeleteUserFromWorkspace.Id != a.member.UserWorkspaceId.GetOrEmpty() || removed.DeleteUserFromWorkspace.UserId != a.member.UserId) {
			err = errInvalidMemberResponse
		}
		_, actual, readErr := r.snapshot(ctx, state)
		if readErr == nil && actual.status != "absent" {
			readErr = errMemberPartial
		}
		if err == nil {
			err = readErr
		}
	}
	if err != nil {
		memberError(&resp.Diagnostics, "Delete", err)
	}
}
func (r *workspaceMemberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	workspace, email, err := parseMemberID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Import Identifier", "Use lowercase workspace UUID/email, with one normalized bare ASCII email address.")
		return
	}
	if !r.ready(&resp.Diagnostics) {
		return
	}
	model := workspaceMemberModel{ID: types.StringValue(req.ID), WorkspaceID: types.StringValue(workspace), Email: types.StringValue(email), MemberID: types.StringNull()}
	r.mutationLock.Lock()
	defer r.mutationLock.Unlock()
	s, a, err := r.snapshot(ctx, model)
	if err == nil && a.status == "absent" {
		err = errMemberMissing
	}
	if err == nil && (s.role(a.roleID) == nil || !s.role(a.roleID).CanBeAssignedToUsers) {
		err = errMemberRole
	}
	if err != nil {
		memberError(&resp.Diagnostics, "Import", err)
		return
	}
	model.fromAccess(a)
	model.OwnershipConfirmed = types.BoolValue(true)
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}
