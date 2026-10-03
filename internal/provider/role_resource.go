// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/Khan/genqlient/graphql"
	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/oapi-codegen/nullable"
)

var (
	_ resource.Resource                   = &roleResource{}
	_ resource.ResourceWithConfigure      = &roleResource{}
	_ resource.ResourceWithImportState    = &roleResource{}
	_ resource.ResourceWithValidateConfig = &roleResource{}
)

type roleResource struct {
	client       graphql.Client
	identity     client.Identity
	mutationLock *sync.Mutex
}

type roleResourceModel struct {
	ID                            types.String `tfsdk:"id"`
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

func NewRoleResource() resource.Resource { return &roleResource{} }
func (r *roleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_role"
}
func (r *roleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		"id":               schema.StringAttribute{Computed: true, MarkdownDescription: "Native role UUID. Import uses this ID, not the label.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"label":            schema.StringAttribute{Required: true, MarkdownDescription: "Nonblank custom role label. Admin, Member, and Guest are reserved, case-insensitively. Use single spaces without leading or trailing whitespace; Twenty normalizes strings during creation. Create never adopts an existing label."},
		"description":      schema.StringAttribute{Optional: true, MarkdownDescription: "Nullable description. Omission or null clears it; an empty string stays empty. Use normalized single-space whitespace."},
		"icon":             schema.StringAttribute{Optional: true, MarkdownDescription: "Nullable icon identifier. Omission or null clears it; an empty string stays empty. Use normalized single-space whitespace."},
		"is_editable":      schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether Twenty permits editing the role. Non-editable roles cannot be mutated."},
		"permission_flags": schema.SetAttribute{Required: true, ElementType: types.StringType, MarkdownDescription: "Complete owned set of explicit v2.44.0 permission flag keys. Updates replace the whole set; [] clears it. Null, null elements, and unsupported keys are rejected. Global booleans can grant additional access."},
	}
	for _, field := range roleBooleanFields() {
		attrs[field.name] = schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(field.defaultValue), MarkdownDescription: field.description}
	}
	resp.Schema = schema.Schema{MarkdownDescription: "Manages a custom Twenty role and its complete explicit permission flag set through Metadata GraphQL. Requires ROLES and visibility of current workspace members and roles. Deletion also requires APPLICATIONS to check application defaults. Protects the operator's current roles, the workspace default, built-in roles, and the final full-settings administrator. Deletion refuses assigned roles.", Attributes: attrs}
}

type roleBooleanField struct {
	name         string
	defaultValue bool
	description  string
}

func roleBooleanFields() []roleBooleanField {
	return []roleBooleanField{
		{name: "can_be_assigned_to_users", defaultValue: true, description: "Whether workspace users can receive the role. Defaults to true."},
		{name: "can_be_assigned_to_agents", description: "Whether agents can receive the role. Defaults to false."},
		{name: "can_be_assigned_to_api_keys", description: "Whether API keys can receive the role. Defaults to false."},
		{name: "can_update_all_settings", description: "Grants all settings permissions. Defaults to false. Explicit flags do not limit a true global grant."},
		{name: "can_access_all_tools", description: "Grants all tools permissions. Defaults to false."},
		{name: "can_read_all_object_records", description: "Grants read access to all object records. Defaults to false. Must be true for any global object write/delete grant; this resource never reads CRM records."},
		{name: "can_update_all_object_records", description: "Grants update access to all object records. Defaults to false."},
		{name: "can_soft_delete_all_object_records", description: "Grants soft-delete access to all object records. Defaults to false."},
		{name: "can_destroy_all_object_records", description: "Grants permanent-delete access to all object records. Defaults to false."},
	}
}
func (m roleResourceModel) booleans() []types.Bool {
	return []types.Bool{m.CanBeAssignedToUsers, m.CanBeAssignedToAgents, m.CanBeAssignedToAPIKeys, m.CanUpdateAllSettings, m.CanAccessAllTools, m.CanReadAllObjectRecords, m.CanUpdateAllObjectRecords, m.CanSoftDeleteAllObjectRecords, m.CanDestroyAllObjectRecords}
}
func (r *roleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client, r.mutationLock, r.identity = nil, nil, client.Identity{}
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*ClientData)
	if !ok || data == nil || data.Client == nil {
		resp.Diagnostics.AddError("Invalid Twenty Client", "The role resource did not receive an authenticated Twenty session.")
		return
	}
	r.client, r.identity, r.mutationLock = data.Client.Client(), data.Client.Identity(), &data.MutationLock
}
func (r *roleResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config roleResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(config.validate(false)...)
	}
}
func (m roleResourceModel) validate(known bool) diag.Diagnostics {
	var d diag.Diagnostics
	for _, field := range []struct {
		name  string
		value types.String
	}{{"label", m.Label}, {"description", m.Description}, {"icon", m.Icon}} {
		if field.value.IsUnknown() {
			if known {
				d.AddAttributeError(path.Root(field.name), "Unknown Twenty Role Input", "All role inputs must be known before mutation.")
			}
			continue
		}
		if field.name == "label" && (field.value.IsNull() || blankRoleLabel(field.value.ValueString())) {
			d.AddAttributeError(path.Root(field.name), "Invalid Twenty Role Label", "The label must contain a non-whitespace character.")
		}
		if field.name == "label" && reservedRoleLabel(field.value.ValueString()) {
			d.AddAttributeError(path.Root(field.name), "Protected Twenty Role Label", "Admin, Member, and Guest labels are reserved for built-in or seeded roles and cannot be managed.")
		}
		if !field.value.IsNull() && field.value.ValueString() != strings.Join(strings.Fields(field.value.ValueString()), " ") {
			d.AddAttributeError(path.Root(field.name), "Invalid Twenty Role Whitespace", "Use single spaces without leading or trailing whitespace. Twenty normalizes strings during creation.")
		}
	}
	fields := roleBooleanFields()
	for i, value := range m.booleans() {
		if known && (value.IsUnknown() || value.IsNull()) {
			d.AddAttributeError(path.Root(fields[i].name), "Unknown Twenty Role Input", "Capability and assignability booleans must be known and non-null before mutation.")
		}
	}
	if !m.CanReadAllObjectRecords.IsUnknown() && !m.CanReadAllObjectRecords.ValueBool() && (m.CanUpdateAllObjectRecords.ValueBool() || m.CanSoftDeleteAllObjectRecords.ValueBool() || m.CanDestroyAllObjectRecords.ValueBool()) {
		d.AddError("Invalid Twenty Role Permissions", "Global object update or delete grants require can_read_all_object_records = true.")
	}
	if m.PermissionFlags.IsNull() || known && m.PermissionFlags.IsUnknown() {
		d.AddAttributeError(path.Root("permission_flags"), "Invalid Twenty Role Flags", "Specify an explicit set of supported flags, or [] to clear it. Flags must be known before mutation.")
	}
	for _, element := range m.PermissionFlags.Elements() {
		value, ok := element.(types.String)
		if !ok || value.IsNull() {
			d.AddError("Invalid Twenty Role Flag", "Flag elements must be non-null strings.")
			continue
		}
		if value.IsUnknown() {
			if known {
				d.AddError("Unknown Twenty Role Flag", "Flag elements must be known before mutation.")
			}
			continue
		}
		if !slices.Contains(client.AllPermissionFlagType, client.PermissionFlagType(value.ValueString())) {
			d.AddError("Invalid Twenty Role Flag", "Use the exact supported v2.44.0 permission flag vocabulary documented in the permissions guide.")
		}
	}
	return d
}
func roleStringInput(value types.String) nullable.Nullable[string] {
	if value.IsNull() {
		return nullable.NewNullNullable[string]()
	}
	return nullable.NewNullableWithValue(value.ValueString())
}
func (m roleResourceModel) payload() client.UpdateRolePayload {
	return client.UpdateRolePayload{
		Label:                         nullable.NewNullableWithValue(m.Label.ValueString()),
		Description:                   roleStringInput(m.Description),
		Icon:                          roleStringInput(m.Icon),
		CanBeAssignedToUsers:          nullable.NewNullableWithValue(m.CanBeAssignedToUsers.ValueBool()),
		CanBeAssignedToAgents:         nullable.NewNullableWithValue(m.CanBeAssignedToAgents.ValueBool()),
		CanBeAssignedToApiKeys:        nullable.NewNullableWithValue(m.CanBeAssignedToAPIKeys.ValueBool()),
		CanUpdateAllSettings:          nullable.NewNullableWithValue(m.CanUpdateAllSettings.ValueBool()),
		CanAccessAllTools:             nullable.NewNullableWithValue(m.CanAccessAllTools.ValueBool()),
		CanReadAllObjectRecords:       nullable.NewNullableWithValue(m.CanReadAllObjectRecords.ValueBool()),
		CanUpdateAllObjectRecords:     nullable.NewNullableWithValue(m.CanUpdateAllObjectRecords.ValueBool()),
		CanSoftDeleteAllObjectRecords: nullable.NewNullableWithValue(m.CanSoftDeleteAllObjectRecords.ValueBool()),
		CanDestroyAllObjectRecords:    nullable.NewNullableWithValue(m.CanDestroyAllObjectRecords.ValueBool()),
	}
}
func (m roleResourceModel) createInput() client.CreateRoleInput {
	p := m.payload()
	return client.CreateRoleInput{
		Id:                            nullable.NewNullableWithValue(m.ID.ValueString()),
		Label:                         m.Label.ValueString(),
		Description:                   p.Description,
		Icon:                          p.Icon,
		CanBeAssignedToUsers:          p.CanBeAssignedToUsers,
		CanBeAssignedToAgents:         p.CanBeAssignedToAgents,
		CanBeAssignedToApiKeys:        p.CanBeAssignedToApiKeys,
		CanUpdateAllSettings:          p.CanUpdateAllSettings,
		CanAccessAllTools:             p.CanAccessAllTools,
		CanReadAllObjectRecords:       p.CanReadAllObjectRecords,
		CanUpdateAllObjectRecords:     p.CanUpdateAllObjectRecords,
		CanSoftDeleteAllObjectRecords: p.CanSoftDeleteAllObjectRecords,
		CanDestroyAllObjectRecords:    p.CanDestroyAllObjectRecords,
	}
}
func (m *roleResourceModel) fromAPI(ctx context.Context, role client.GetRolesGetRolesRole) error {
	values := roleModelFromAPI(ctx, role, types.StringNull())
	*m = roleResourceModel{
		ID:                            values.ID,
		Label:                         values.Label,
		Description:                   values.Description,
		Icon:                          values.Icon,
		IsEditable:                    values.IsEditable,
		CanBeAssignedToUsers:          values.CanBeAssignedToUsers,
		CanBeAssignedToAgents:         values.CanBeAssignedToAgents,
		CanBeAssignedToAPIKeys:        values.CanBeAssignedToAPIKeys,
		CanUpdateAllSettings:          values.CanUpdateAllSettings,
		CanAccessAllTools:             values.CanAccessAllTools,
		CanReadAllObjectRecords:       values.CanReadAllObjectRecords,
		CanUpdateAllObjectRecords:     values.CanUpdateAllObjectRecords,
		CanSoftDeleteAllObjectRecords: values.CanSoftDeleteAllObjectRecords,
		CanDestroyAllObjectRecords:    values.CanDestroyAllObjectRecords,
		PermissionFlags:               values.PermissionFlags,
	}
	// A resource must not silently own an unavailable or unsupported flag set.
	if m.PermissionFlags.IsNull() {
		return errInvalidRoleResponse
	}
	for _, flag := range role.PermissionFlags {
		if !slices.Contains(client.AllPermissionFlagType, client.PermissionFlagType(flag.Flag)) {
			return errInvalidRoleResponse
		}
	}
	return nil
}
func readManagedRole(ctx context.Context, api graphql.Client, id string) (*client.GetRolesGetRolesRole, error) {
	if !validRoleUUID(id) {
		return nil, errInvalidRoleResponse
	}
	response, err := client.GetRoles(ctx, roleQueryClient{api})
	if err != nil {
		return nil, err
	}
	var selected *client.GetRolesGetRolesRole
	seen := map[string]bool{}
	for i := range response.GetRoles {
		role := &response.GetRoles[i]
		key := strings.ToLower(role.Id)
		if seen[key] {
			return nil, errInvalidRoleResponse
		}
		seen[key] = true
		if strings.EqualFold(role.Id, id) {
			selected = role
		}
	}
	return selected, nil
}
func roleError(d *diag.Diagnostics, action string, err error) {
	detail := client.DiagnosticMessage(err)
	if errors.Is(err, errInvalidRoleResponse) || errors.Is(err, errUnsafeRole) {
		detail = err.Error()
	}
	d.AddError("Unable to "+action+" Twenty Role", detail)
}
func (r *roleResource) ready(d *diag.Diagnostics) bool {
	if r.client == nil || r.mutationLock == nil {
		d.AddError("Twenty Client Not Configured", "Configure the Twenty provider before managing a role.")
		return false
	}
	return true
}
func (r *roleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan roleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(plan.validate(true)...)
	if resp.Diagnostics.HasError() || !r.ready(&resp.Diagnostics) {
		return
	}
	r.mutationLock.Lock()
	defer r.mutationLock.Unlock()
	if _, err := readRoleSafety(ctx, r.client, r.identity); err != nil {
		roleError(&resp.Diagnostics, "Create", err)
		return
	}
	// Supply a native UUID before sending the mutation. An ambiguous response still
	// leaves an importable ID in state. Never retry creation or adopt by label.
	plan.ID, plan.IsEditable = types.StringValue(uuid.NewString()), types.BoolValue(true)
	response, err := client.CreateOneRole(ctx, r.client, plan.createInput())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if err != nil {
		roleError(&resp.Diagnostics, "Create", err)
		return
	}
	if response.CreateOneRole.Id != plan.ID.ValueString() {
		roleError(&resp.Diagnostics, "Create", errInvalidRoleResponse)
		return
	}
	err = r.writeFlags(ctx, plan)
	role, readErr := readManagedRole(ctx, r.client, plan.ID.ValueString())
	if readErr == nil && role == nil {
		readErr = errInvalidRoleResponse
	}
	if readErr == nil {
		readErr = plan.fromAPI(ctx, *role)
		if readErr == nil {
			resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		}
	}
	if err != nil {
		roleError(&resp.Diagnostics, "Create", err)
		return
	}
	if readErr != nil {
		roleError(&resp.Diagnostics, "Create", readErr)
	}
}
func (r *roleResource) writeFlags(ctx context.Context, plan roleResourceModel) error {
	flags := []string{}
	if d := plan.PermissionFlags.ElementsAs(ctx, &flags, false); d.HasError() {
		return errInvalidRoleResponse
	}
	if flags == nil {
		flags = []string{}
	}
	response, err := client.UpsertPermissionFlags(ctx, r.client, client.UpsertPermissionFlagsInput{RoleId: plan.ID.ValueString(), PermissionFlagKeys: flags})
	if err != nil {
		return err
	}
	if response.UpsertPermissionFlags == nil || len(response.UpsertPermissionFlags) != len(flags) {
		return errInvalidRoleResponse
	}
	raw, err := json.Marshal(response.UpsertPermissionFlags)
	if err != nil || validateRoleFlags(raw, plan.ID.ValueString()) != nil {
		return errInvalidRoleResponse
	}
	for _, flag := range response.UpsertPermissionFlags {
		if !slices.Contains(flags, flag.Flag) {
			return errInvalidRoleResponse
		}
	}
	return nil
}
func (r *roleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state roleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || !r.ready(&resp.Diagnostics) {
		return
	}
	role, err := readManagedRole(ctx, r.client, state.ID.ValueString())
	if err != nil {
		roleError(&resp.Diagnostics, "Read", err)
		return
	}
	if role == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	if err := state.fromAPI(ctx, *role); err != nil {
		roleError(&resp.Diagnostics, "Read", err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
func (r *roleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state roleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(plan.validate(true)...)
	if resp.Diagnostics.HasError() || !r.ready(&resp.Diagnostics) {
		return
	}
	if !validRoleUUID(state.ID.ValueString()) || !plan.ID.Equal(state.ID) {
		roleError(&resp.Diagnostics, "Update", errInvalidRoleResponse)
		return
	}
	r.mutationLock.Lock()
	defer r.mutationLock.Unlock()
	snapshot, err := readRoleSafety(ctx, r.client, r.identity)
	if err == nil && snapshot.role(state.ID.ValueString()) == nil {
		err = errInvalidRoleResponse
	}
	if err == nil {
		err = snapshot.guard(state.ID.ValueString(), false, plan.CanUpdateAllSettings.ValueBool(), plan.CanBeAssignedToUsers.ValueBool())
	}
	if err != nil {
		roleError(&resp.Diagnostics, "Update", err)
		return
	}
	response, err := client.UpdateOneRole(ctx, r.client, client.UpdateRoleInput{Id: state.ID.ValueString(), Update: plan.payload()})
	if err == nil && response.UpdateOneRole.Id != state.ID.ValueString() {
		err = errInvalidRoleResponse
	}
	if err == nil {
		err = r.writeFlags(ctx, plan)
	}
	// Any partial mutation retains the existing ID. Refresh can recover actual
	// server values after access or transient server failures are repaired.
	role, readErr := readManagedRole(ctx, r.client, state.ID.ValueString())
	if readErr == nil && role != nil {
		if mapErr := state.fromAPI(ctx, *role); mapErr == nil {
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		} else {
			readErr = mapErr
		}
	} else if readErr == nil {
		readErr = errInvalidRoleResponse
	}
	if err != nil {
		roleError(&resp.Diagnostics, "Update", err)
		return
	}
	if readErr != nil {
		roleError(&resp.Diagnostics, "Update", readErr)
	}
}
func (r *roleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state roleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || !r.ready(&resp.Diagnostics) {
		return
	}
	if !validRoleUUID(state.ID.ValueString()) {
		roleError(&resp.Diagnostics, "Delete", errInvalidRoleResponse)
		return
	}
	r.mutationLock.Lock()
	defer r.mutationLock.Unlock()
	snapshot, err := readRoleSafety(ctx, r.client, r.identity)
	if err != nil {
		roleError(&resp.Diagnostics, "Delete", err)
		return
	}
	if snapshot.role(state.ID.ValueString()) == nil {
		return
	}
	// Application defaults are not in role assignment relations. Only deletion
	// needs this extra APPLICATIONS read; create/update need no application grant.
	applications, err := client.FindManyApplications(ctx, roleSafetyClient{r.client})
	if err != nil {
		roleError(&resp.Diagnostics, "Delete", err)
		return
	}
	snapshot.applications = applications.FindManyApplications
	if err := snapshot.guard(state.ID.ValueString(), true, false, false); err != nil {
		roleError(&resp.Diagnostics, "Delete", err)
		return
	}
	response, err := client.DeleteOneRole(ctx, r.client, state.ID.ValueString())
	if err != nil {
		roleError(&resp.Diagnostics, "Delete", err)
		return
	}
	if !strings.EqualFold(response.DeleteOneRole, state.ID.ValueString()) {
		roleError(&resp.Diagnostics, "Delete", errInvalidRoleResponse)
		return
	}
	role, err := readManagedRole(ctx, r.client, state.ID.ValueString())
	if err == nil && role != nil {
		err = errInvalidRoleResponse
	}
	if err != nil {
		roleError(&resp.Diagnostics, "Delete", err)
	}
}
func (r *roleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !validRoleUUID(req.ID) {
		resp.Diagnostics.AddError("Invalid Import Identifier", "Use the native role UUID, not a label or composite ID.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), strings.ToLower(req.ID))...)
}
