// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/Khan/genqlient/graphql"
	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
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
	_ resource.Resource                   = &fieldResource{}
	_ resource.ResourceWithConfigure      = &fieldResource{}
	_ resource.ResourceWithImportState    = &fieldResource{}
	_ resource.ResourceWithValidateConfig = &fieldResource{}
)

type fieldResource struct {
	client       graphql.Client
	identity     client.Identity
	mutationLock *sync.Mutex
}

type fieldResourceModel struct {
	ID                 types.String `tfsdk:"id"`
	ObjectID           types.String `tfsdk:"object_id"`
	Name               types.String `tfsdk:"name"`
	Type               types.String `tfsdk:"type"`
	Label              types.String `tfsdk:"label"`
	Description        types.String `tfsdk:"description"`
	Icon               types.String `tfsdk:"icon"`
	DeletionProtection types.Bool   `tfsdk:"deletion_protection"`
}

func NewFieldResource() resource.Resource { return &fieldResource{} }
func (r *fieldResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_field"
}
func (r *fieldResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a nullable workspace-custom field, including custom fields on standard company/person objects. Only TEXT, NUMBER, BOOLEAN, DATE, and DATE_TIME are supported. Built-in, system, remote, required, application-owned, and label-identifier fields are protected. Deletion permanently removes field data. No default values, options, relationships, or migrations are managed.",
		Attributes: map[string]schema.Attribute{
			"id":                  schema.StringAttribute{Computed: true, MarkdownDescription: "Native field UUID used for import.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"object_id":           schema.StringAttribute{Required: true, MarkdownDescription: "Parent object's native UUID. Changing it requires replacement.", PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"name":                schema.StringAttribute{Required: true, MarkdownDescription: "ASCII lowerCamel field name, at most 63 characters. The default name field is protected. Create rejects existing names instead of adopting them. Changing it requires replacement.", PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"type":                schema.StringAttribute{Required: true, MarkdownDescription: "One of TEXT, NUMBER, BOOLEAN, DATE, or DATE_TIME. Changing it requires replacement, not a data migration.", PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"label":               schema.StringAttribute{Required: true, MarkdownDescription: "Nonblank display label. Use single spaces without surrounding whitespace."},
			"description":         schema.StringAttribute{Optional: true, MarkdownDescription: "Description. Omission or null clears it; empty stays empty. Use single spaces without surrounding whitespace."},
			"icon":                schema.StringAttribute{Optional: true, MarkdownDescription: "Icon identifier. Omission or null clears it; empty stays empty. Use single spaces without surrounding whitespace."},
			"deletion_protection": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), MarkdownDescription: "Defaults to true. Apply false before destroying or replacing the field. Deletion permanently removes CRM data and does not deactivate the field first."},
		}}
}
func (r *fieldResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client, r.identity, r.mutationLock = nil, client.Identity{}, nil
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*ClientData)
	if !ok || data == nil || data.Client == nil {
		resp.Diagnostics.AddError("Invalid Twenty Client", "The field resource did not receive an authenticated Twenty session.")
		return
	}
	r.client, r.identity, r.mutationLock = data.Client.Client(), data.Client.Identity(), &data.MutationLock
}
func (r *fieldResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config fieldResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(config.validate(false)...)
	}
}
func (m fieldResourceModel) validate(known bool) diag.Diagnostics {
	var d diag.Diagnostics
	for _, field := range []struct {
		name     string
		value    types.String
		required bool
	}{
		{"object_id", m.ObjectID, true}, {"name", m.Name, true}, {"type", m.Type, true}, {"label", m.Label, true}, {"description", m.Description, false}, {"icon", m.Icon, false},
	} {
		if field.value.IsUnknown() {
			if known {
				d.AddAttributeError(path.Root(field.name), "Unknown Twenty Field Input", "All field inputs must be known before mutation.")
			}
			continue
		}
		if field.value.IsNull() {
			if field.required {
				d.AddAttributeError(path.Root(field.name), "Missing Twenty Field Input", "This field input must be non-null.")
			}
			continue
		}
		value := field.value.ValueString()
		var valid bool
		switch field.name {
		case "object_id":
			valid = metadataUUID(value)
		case "name":
			valid = metadataName(value) && value != "name"
		case "type":
			valid = supportedMetadataFieldType(value)
		default:
			valid = value == strings.Join(strings.Fields(value), " ") && (field.name != "label" || value != "")
		}
		if !valid {
			d.AddAttributeError(path.Root(field.name), "Invalid Twenty Field Input", "Use a native object UUID, ASCII lowerCamel name of at most 63 characters other than the protected default name, supported type, and canonical strings with single spaces and no surrounding whitespace. The label must not be blank.")
		}
	}
	if known && (m.DeletionProtection.IsUnknown() || m.DeletionProtection.IsNull()) {
		d.AddAttributeError(path.Root("deletion_protection"), "Unknown Twenty Field Input", "Deletion protection must be known and non-null before mutation.")
	}
	return d
}
func (r *fieldResource) ready(d *diag.Diagnostics) bool {
	if r.client == nil || r.mutationLock == nil {
		d.AddError("Twenty Client Not Configured", "Configure the Twenty provider before managing a field.")
		return false
	}
	return true
}

// Standard objects can host custom fields. Their application need not be Custom.
func fieldParentSafe(parent *client.ObjectMetadata) error {
	if parent == nil {
		return errMetadataNotFound
	}
	if parent.IsSystem || parent.IsRemote || parent.Writability != client.MetadataWritabilityOpen {
		return errUnsafeMetadata
	}
	return nil
}
func (r *fieldResource) guardedField(ctx context.Context, id, app string) (*client.FieldMetadata, error) {
	field, err := readMetadataField(ctx, r.client, id)
	if err != nil || field == nil {
		return field, err
	}
	parent, err := readMetadataObject(ctx, r.client, field.ObjectMetadataId)
	if err != nil {
		return nil, err
	}
	if parent == nil {
		return nil, errInvalidMetadataResponse
	}
	if err := guardMetadataField(field, parent, app); err != nil {
		return nil, err
	}
	// The default name field remains protected even if another field becomes
	// the object's label identifier later.
	if field.Name == "name" {
		return nil, errUnsafeMetadata
	}
	return field, nil
}
func fieldNameAvailable(ctx context.Context, api graphql.Client, objectID, name, ownID string) error {
	fields, err := readMetadataFields(ctx, api, objectID)
	if err != nil {
		return err
	}
	for _, field := range fields {
		if field.Name == name && !strings.EqualFold(field.Id, ownID) {
			return errMetadataConflict
		}
	}
	return nil
}
func (m fieldResourceModel) matchesIdentity(field *client.FieldMetadata) bool {
	return field != nil && strings.EqualFold(m.ObjectID.ValueString(), field.ObjectMetadataId) && m.Name.ValueString() == field.Name && m.Type.ValueString() == string(field.Type)
}
func (m fieldResourceModel) matchesWrite(field *client.FieldMetadata) bool {
	return m.matchesIdentity(field) && m.Label.ValueString() == field.Label && m.Description.Equal(roleNullableString(field.Description)) && m.Icon.Equal(roleNullableString(field.Icon)) && !field.IsLabelSyncedWithName.GetOrEmpty()
}
func (m *fieldResourceModel) fromAPI(field *client.FieldMetadata) {
	m.ID = types.StringValue(strings.ToLower(field.Id))
	// Keep the configured UUID spelling when it identifies the same parent.
	if !strings.EqualFold(m.ObjectID.ValueString(), field.ObjectMetadataId) {
		m.ObjectID = types.StringValue(strings.ToLower(field.ObjectMetadataId))
	}
	m.Name, m.Type, m.Label = types.StringValue(field.Name), types.StringValue(string(field.Type)), types.StringValue(field.Label)
	m.Description, m.Icon = roleNullableString(field.Description), roleNullableString(field.Icon)
	if m.DeletionProtection.IsNull() || m.DeletionProtection.IsUnknown() {
		m.DeletionProtection = types.BoolValue(true)
	}
}
func (r *fieldResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan fieldResourceModel
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
	app, err := metadataCustomApplication(ctx, r.client, r.identity)
	if err == nil {
		var parent *client.ObjectMetadata
		parent, err = readMetadataObject(ctx, r.client, plan.ObjectID.ValueString())
		if err == nil {
			err = fieldParentSafe(parent)
		}
	}
	if err == nil {
		err = fieldNameAvailable(ctx, r.client, plan.ObjectID.ValueString(), plan.Name.ValueString(), "")
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		metadataError(&resp.Diagnostics, "Create", "Field", err)
		return
	}
	response, err := client.CreateMetadataField(ctx, metadataQueryClient{r.client}, client.CreateOneFieldMetadataInput{Field: client.CreateFieldInput{
		ObjectMetadataId: strings.ToLower(plan.ObjectID.ValueString()), Name: plan.Name.ValueString(), Type: client.FieldMetadataType(plan.Type.ValueString()), Label: plan.Label.ValueString(),
		Description: roleStringInput(plan.Description), Icon: roleStringInput(plan.Icon), DefaultValue: nullable.NewNullNullable[json.RawMessage](),
		IsNullable: nullable.NewNullableWithValue(true), IsActive: nullable.NewNullableWithValue(true), IsLabelSyncedWithName: nullable.NewNullableWithValue(false), IsSystem: nullable.NewNullableWithValue(false),
	}})
	if err != nil {
		metadataError(&resp.Diagnostics, "Create", "Field", err)
		resp.Diagnostics.AddError("Field Creation May Have Succeeded", "Twenty chooses the field UUID. Inspect the workspace and explicitly import the intended native field UUID before retrying. A matching name is never adopted automatically.")
		return
	}
	// Store the server-assigned UUID before any read-back can fail.
	plan.ID = types.StringValue(strings.ToLower(response.CreateOneField.Id))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	field, err := r.guardedField(ctx, plan.ID.ValueString(), app)
	if err == nil && (!plan.matchesWrite(field) || !field.IsActive.GetOrEmpty()) {
		err = errInvalidMetadataResponse
	}
	if err != nil {
		metadataError(&resp.Diagnostics, "Verify Created", "Field", err)
		return
	}
	plan.fromAPI(field)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}
func (r *fieldResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state fieldResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || !r.ready(&resp.Diagnostics) {
		return
	}
	r.mutationLock.Lock()
	defer r.mutationLock.Unlock()
	app, err := metadataCustomApplication(ctx, r.client, r.identity)
	var field *client.FieldMetadata
	if err == nil {
		field, err = r.guardedField(ctx, state.ID.ValueString(), app)
	}
	if err != nil {
		metadataError(&resp.Diagnostics, "Read", "Field", err)
		return
	}
	if field == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	state.fromAPI(field)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
func (r *fieldResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state fieldResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(plan.validate(true)...)
	if resp.Diagnostics.HasError() || !r.ready(&resp.Diagnostics) {
		return
	}
	if !metadataUUID(state.ID.ValueString()) || !strings.EqualFold(plan.ID.ValueString(), state.ID.ValueString()) || !strings.EqualFold(plan.ObjectID.ValueString(), state.ObjectID.ValueString()) || !plan.Name.Equal(state.Name) || !plan.Type.Equal(state.Type) {
		metadataError(&resp.Diagnostics, "Update", "Field", errInvalidMetadataResponse)
		return
	}
	r.mutationLock.Lock()
	defer r.mutationLock.Unlock()
	app, err := metadataCustomApplication(ctx, r.client, r.identity)
	var field *client.FieldMetadata
	if err == nil {
		field, err = r.guardedField(ctx, state.ID.ValueString(), app)
	}
	if err == nil && !state.matchesIdentity(field) {
		err = errInvalidMetadataResponse
	}
	if err == nil {
		err = fieldNameAvailable(ctx, r.client, state.ObjectID.ValueString(), state.Name.ValueString(), state.ID.ValueString())
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		metadataError(&resp.Diagnostics, "Update", "Field", err)
		return
	}
	response, err := client.UpdateMetadataField(ctx, metadataQueryClient{r.client}, client.UpdateOneFieldMetadataInput{Id: state.ID.ValueString(), Update: client.UpdateFieldInput{
		Label: roleStringInput(plan.Label), Description: roleStringInput(plan.Description), Icon: roleStringInput(plan.Icon), IsLabelSyncedWithName: nullable.NewNullableWithValue(false),
	}})
	if err == nil && !strings.EqualFold(response.UpdateOneField.Id, state.ID.ValueString()) {
		err = errInvalidMetadataResponse
	}
	if err != nil {
		metadataError(&resp.Diagnostics, "Update", "Field", err)
		return
	}
	field, err = r.guardedField(ctx, state.ID.ValueString(), app)
	if err == nil && !plan.matchesWrite(field) {
		err = errInvalidMetadataResponse
	}
	if err != nil {
		metadataError(&resp.Diagnostics, "Verify Updated", "Field", err)
		return
	}
	plan.fromAPI(field)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}
func (r *fieldResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state fieldResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if state.DeletionProtection.IsNull() || state.DeletionProtection.IsUnknown() || state.DeletionProtection.ValueBool() {
		metadataError(&resp.Diagnostics, "Delete", "Field", errMetadataProtected)
		return
	}
	if !r.ready(&resp.Diagnostics) {
		return
	}
	r.mutationLock.Lock()
	defer r.mutationLock.Unlock()
	app, err := metadataCustomApplication(ctx, r.client, r.identity)
	var field *client.FieldMetadata
	if err == nil {
		field, err = r.guardedField(ctx, state.ID.ValueString(), app)
	}
	if err != nil {
		metadataError(&resp.Diagnostics, "Delete", "Field", err)
		return
	}
	if field == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	if !state.matchesIdentity(field) {
		metadataError(&resp.Diagnostics, "Delete", "Field", errInvalidMetadataResponse)
		return
	}
	if err := ctx.Err(); err != nil {
		metadataError(&resp.Diagnostics, "Delete", "Field", err)
		return
	}
	// Deactivation deletes view fields and filters. Hard-delete directly instead.
	response, err := client.DeleteMetadataField(ctx, metadataQueryClient{r.client}, client.DeleteOneFieldInput{Id: state.ID.ValueString()})
	if err == nil && !strings.EqualFold(response.DeleteOneField.Id, state.ID.ValueString()) {
		err = errInvalidMetadataResponse
	}
	if err != nil {
		metadataError(&resp.Diagnostics, "Delete", "Field", err)
		return
	}
	field, err = readMetadataField(ctx, r.client, state.ID.ValueString())
	if err == nil && field != nil {
		err = errInvalidMetadataResponse
	}
	if err != nil {
		metadataError(&resp.Diagnostics, "Verify Deleted", "Field", err)
		return
	}
	resp.State.RemoveResource(ctx)
}
func (r *fieldResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !metadataUUID(req.ID) {
		resp.Diagnostics.AddError("Invalid Import Identifier", "Use the native field UUID, not a name or composite ID.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), strings.ToLower(req.ID))...)
}
