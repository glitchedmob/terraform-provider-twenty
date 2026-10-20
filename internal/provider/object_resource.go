// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/Khan/genqlient/graphql"
	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/oapi-codegen/nullable"
)

var (
	_ resource.Resource                   = &objectResource{}
	_ resource.ResourceWithConfigure      = &objectResource{}
	_ resource.ResourceWithImportState    = &objectResource{}
	_ resource.ResourceWithValidateConfig = &objectResource{}
)

type objectResource struct {
	client       graphql.Client
	identity     client.Identity
	mutationLock *sync.Mutex
}

type objectResourceModel struct {
	ID                 types.String `tfsdk:"id"`
	NameSingular       types.String `tfsdk:"name_singular"`
	NamePlural         types.String `tfsdk:"name_plural"`
	LabelSingular      types.String `tfsdk:"label_singular"`
	LabelPlural        types.String `tfsdk:"label_plural"`
	Description        types.String `tfsdk:"description"`
	Icon               types.String `tfsdk:"icon"`
	DeletionProtection types.Bool   `tfsdk:"deletion_protection"`
}

func NewObjectResource() resource.Resource { return &objectResource{} }
func (r *objectResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_object"
}
func (r *objectResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a workspace-custom Twenty object. Reads and mutations require the DATA_MODEL settings permission. Built-in, system, remote, and application-owned objects are protected. Deletion immediately drops the object table and cascades all fields and CRM data. Twenty owns generated default name and system fields; do not manage those as separate field resources.",
		Attributes: map[string]schema.Attribute{
			"id":                  schema.StringAttribute{Computed: true, MarkdownDescription: "Native object UUID used for import.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"name_singular":       schema.StringAttribute{Required: true, MarkdownDescription: "Unique ASCII lowerCamelCase singular name, at most 63 characters. Changing it replaces the object. Creation never adopts by name.", PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"name_plural":         schema.StringAttribute{Required: true, MarkdownDescription: "Unique ASCII lowerCamelCase plural name, at most 63 characters. Must differ from the singular name. Changing it replaces the object.", PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"label_singular":      schema.StringAttribute{Required: true, MarkdownDescription: "Nonblank singular display label, independent of the name. Start with an uppercase letter when the first character has case. Use single spaces with no surrounding whitespace."},
			"label_plural":        schema.StringAttribute{Required: true, MarkdownDescription: "Nonblank plural display label, independent of the name. Start with an uppercase letter when the first character has case. Use single spaces with no surrounding whitespace."},
			"description":         schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""), MarkdownDescription: "Description. Defaults to an empty string. Omission or null clears it. Use single spaces with no surrounding whitespace."},
			"icon":                schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""), MarkdownDescription: "Icon identifier. Defaults to an empty string. Omission or null clears it. Use single spaces with no surrounding whitespace."},
			"deletion_protection": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), MarkdownDescription: "Defaults to true, including on import. Apply false before destroying. Deletion permanently drops the table and all fields and records without deactivating it first."},
		},
	}
}
func (r *objectResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client, r.identity, r.mutationLock = nil, client.Identity{}, nil
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*ClientData)
	if !ok || data == nil || data.Client == nil {
		resp.Diagnostics.AddError("Invalid Twenty Client", "The object resource did not receive an authenticated Twenty session.")
		return
	}
	r.client, r.identity, r.mutationLock = data.Client.Client(), data.Client.Identity(), &data.MutationLock
}
func (r *objectResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config objectResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(config.validate(false)...)
	}
}
func (m objectResourceModel) validate(known bool) diag.Diagnostics {
	var d diag.Diagnostics
	for _, field := range []struct {
		name                string
		value               types.String
		required, nameField bool
	}{
		{"name_singular", m.NameSingular, true, true}, {"name_plural", m.NamePlural, true, true},
		{"label_singular", m.LabelSingular, true, false}, {"label_plural", m.LabelPlural, true, false},
		{"description", m.Description, false, false}, {"icon", m.Icon, false, false},
	} {
		if field.value.IsUnknown() {
			if known {
				d.AddAttributeError(path.Root(field.name), "Unknown Twenty Object Input", "All object inputs must be known before mutation.")
			}
			continue
		}
		value := field.value.ValueString()
		if field.required && (field.value.IsNull() || strings.TrimSpace(value) == "") {
			d.AddAttributeError(path.Root(field.name), "Invalid Twenty Object Input", "Names and labels must be nonblank strings.")
		}
		if field.name == "label_singular" || field.name == "label_plural" {
			first, _ := utf8.DecodeRuneInString(value)
			if unicode.ToUpper(first) != first || unicode.IsLower(first) || unicode.IsTitle(first) {
				d.AddAttributeError(path.Root(field.name), "Invalid Twenty Object Label", "Start the label with an uppercase letter when its first character has case. Twenty capitalizes object labels during creation.")
			}
		}
		if field.nameField && !metadataName(value) {
			d.AddAttributeError(path.Root(field.name), "Invalid Twenty Object Name", "Use an ASCII lowerCamelCase name starting with a lowercase letter, with letters and digits only, at most 63 characters.")
		}
		if !field.value.IsNull() && value != strings.Join(strings.Fields(value), " ") {
			d.AddAttributeError(path.Root(field.name), "Invalid Twenty Object Whitespace", "Use single spaces without leading or trailing whitespace. Twenty normalizes strings during mutation.")
		}
	}
	if !m.NameSingular.IsUnknown() && !m.NamePlural.IsUnknown() && m.NameSingular.Equal(m.NamePlural) {
		d.AddError("Invalid Twenty Object Names", "Singular and plural names must differ.")
	}
	if known && (m.DeletionProtection.IsNull() || m.DeletionProtection.IsUnknown()) {
		d.AddAttributeError(path.Root("deletion_protection"), "Unknown Twenty Object Input", "Deletion protection must be known and non-null before mutation.")
	}
	return d
}
func (r *objectResource) ready(d *diag.Diagnostics) bool {
	if r.client == nil || r.mutationLock == nil {
		d.AddError("Twenty Client Not Configured", "Configure the Twenty provider before managing an object.")
		return false
	}
	return true
}
func (m *objectResourceModel) fromAPI(object *client.ObjectMetadata) {
	m.ID = types.StringValue(strings.ToLower(object.Id))
	m.NameSingular, m.NamePlural = types.StringValue(object.NameSingular), types.StringValue(object.NamePlural)
	m.LabelSingular, m.LabelPlural = types.StringValue(object.LabelSingular), types.StringValue(object.LabelPlural)
	m.Description, m.Icon = types.StringValue(object.Description.GetOrEmpty()), types.StringValue(object.Icon.GetOrEmpty())
	if m.DeletionProtection.IsNull() {
		m.DeletionProtection = types.BoolValue(true)
	}
}
func (m objectResourceModel) verify(object *client.ObjectMetadata, customApp string) error {
	if err := guardMetadataObject(object, customApp); err != nil {
		return err
	}
	if !strings.EqualFold(m.ID.ValueString(), object.Id) || m.NameSingular.ValueString() != object.NameSingular || m.NamePlural.ValueString() != object.NamePlural || m.LabelSingular.ValueString() != object.LabelSingular || m.LabelPlural.ValueString() != object.LabelPlural || m.Description.ValueString() != object.Description.GetOrEmpty() || m.Icon.ValueString() != object.Icon.GetOrEmpty() || object.IsLabelSyncedWithName {
		return errInvalidMetadataResponse
	}
	return nil
}
func (r *objectResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan objectResourceModel
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
	customApp, err := metadataCustomApplication(ctx, r.client, r.identity)
	if err != nil {
		metadataError(&resp.Diagnostics, "Create", "Object", err)
		return
	}
	objects, err := readMetadataObjects(ctx, r.client)
	if err != nil {
		metadataError(&resp.Diagnostics, "Create", "Object", err)
		return
	}
	for _, object := range objects {
		for _, name := range []string{plan.NameSingular.ValueString(), plan.NamePlural.ValueString()} {
			if name == object.NameSingular || name == object.NamePlural {
				metadataError(&resp.Diagnostics, "Create", "Object", errMetadataConflict)
				return
			}
		}
	}
	if err := ctx.Err(); err != nil {
		metadataError(&resp.Diagnostics, "Create", "Object", err)
		return
	}
	response, err := client.CreateOneObject(ctx, metadataQueryClient{r.client}, client.CreateOneObjectInput{Object: client.CreateObjectInput{
		NameSingular: plan.NameSingular.ValueString(), NamePlural: plan.NamePlural.ValueString(),
		LabelSingular: plan.LabelSingular.ValueString(), LabelPlural: plan.LabelPlural.ValueString(),
		Description: roleStringInput(plan.Description), Icon: roleStringInput(plan.Icon),
		IsLabelSyncedWithName: nullable.NewNullableWithValue(false), IsRemote: nullable.NewNullableWithValue(false),
	}})
	if err != nil {
		metadataError(&resp.Diagnostics, "Create", "Object", err)
		resp.Diagnostics.AddError("Object Creation May Have Completed", "No native object UUID was confirmed. Inspect Twenty and explicitly import the intended workspace-custom object by its native UUID before retrying. The provider will not adopt an object by name.")
		return
	}
	plan.ID = types.StringValue(strings.ToLower(response.CreateOneObject.Id))
	// Retain the confirmed native ID even if refresh or verification fails.
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	object, err := readMetadataObject(ctx, r.client, plan.ID.ValueString())
	if err == nil {
		err = plan.verify(object, customApp)
	}
	if err != nil {
		metadataError(&resp.Diagnostics, "Create", "Object", err)
		return
	}
	plan.fromAPI(object)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}
func (r *objectResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state objectResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || !r.ready(&resp.Diagnostics) {
		return
	}
	if !metadataUUID(state.ID.ValueString()) {
		metadataError(&resp.Diagnostics, "Read", "Object", errInvalidMetadataResponse)
		return
	}
	customApp, err := metadataCustomApplication(ctx, r.client, r.identity)
	if err != nil {
		metadataError(&resp.Diagnostics, "Read", "Object", err)
		return
	}
	object, err := readMetadataObject(ctx, r.client, state.ID.ValueString())
	if err != nil {
		metadataError(&resp.Diagnostics, "Read", "Object", err)
		return
	}
	if object == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	if err := guardMetadataObject(object, customApp); err != nil {
		metadataError(&resp.Diagnostics, "Read", "Object", err)
		return
	}
	state.fromAPI(object)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
func (r *objectResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state objectResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(plan.validate(true)...)
	if resp.Diagnostics.HasError() || !r.ready(&resp.Diagnostics) {
		return
	}
	if !metadataUUID(state.ID.ValueString()) || !plan.ID.Equal(state.ID) || !plan.NameSingular.Equal(state.NameSingular) || !plan.NamePlural.Equal(state.NamePlural) {
		metadataError(&resp.Diagnostics, "Update", "Object", errInvalidMetadataResponse)
		return
	}
	r.mutationLock.Lock()
	defer r.mutationLock.Unlock()
	customApp, err := metadataCustomApplication(ctx, r.client, r.identity)
	if err != nil {
		metadataError(&resp.Diagnostics, "Update", "Object", err)
		return
	}
	object, err := readMetadataObject(ctx, r.client, state.ID.ValueString())
	if err == nil {
		err = guardMetadataObject(object, customApp)
	}
	if err == nil && (object.NameSingular != state.NameSingular.ValueString() || object.NamePlural != state.NamePlural.ValueString()) {
		err = errInvalidMetadataResponse
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		metadataError(&resp.Diagnostics, "Update", "Object", err)
		return
	}
	response, err := client.UpdateOneObject(ctx, metadataQueryClient{r.client}, client.UpdateOneObjectInput{Id: state.ID.ValueString(), Update: client.UpdateObjectPayload{
		LabelSingular: nullable.NewNullableWithValue(plan.LabelSingular.ValueString()), LabelPlural: nullable.NewNullableWithValue(plan.LabelPlural.ValueString()),
		Description: roleStringInput(plan.Description), Icon: roleStringInput(plan.Icon), IsLabelSyncedWithName: nullable.NewNullableWithValue(false),
	}})
	if err == nil && !strings.EqualFold(response.UpdateOneObject.Id, state.ID.ValueString()) {
		err = errInvalidMetadataResponse
	}
	if err != nil {
		metadataError(&resp.Diagnostics, "Update", "Object", err)
		return
	}
	object, err = readMetadataObject(ctx, r.client, state.ID.ValueString())
	if err == nil {
		err = plan.verify(object, customApp)
	}
	if err != nil {
		metadataError(&resp.Diagnostics, "Update", "Object", err)
		return
	}
	plan.fromAPI(object)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}
func (r *objectResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state objectResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || !r.ready(&resp.Diagnostics) {
		return
	}
	if state.DeletionProtection.IsNull() || state.DeletionProtection.IsUnknown() || state.DeletionProtection.ValueBool() {
		metadataError(&resp.Diagnostics, "Delete", "Object", errMetadataProtected)
		return
	}
	if !metadataUUID(state.ID.ValueString()) {
		metadataError(&resp.Diagnostics, "Delete", "Object", errInvalidMetadataResponse)
		return
	}
	r.mutationLock.Lock()
	defer r.mutationLock.Unlock()
	customApp, err := metadataCustomApplication(ctx, r.client, r.identity)
	if err != nil {
		metadataError(&resp.Diagnostics, "Delete", "Object", err)
		return
	}
	object, err := readMetadataObject(ctx, r.client, state.ID.ValueString())
	if err != nil {
		metadataError(&resp.Diagnostics, "Delete", "Object", err)
		return
	}
	if object == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	if err := guardMetadataObject(object, customApp); err != nil {
		metadataError(&resp.Diagnostics, "Delete", "Object", err)
		return
	}
	if err := ctx.Err(); err != nil {
		metadataError(&resp.Diagnostics, "Delete", "Object", err)
		return
	}
	response, err := client.DeleteOneObject(ctx, metadataQueryClient{r.client}, client.DeleteOneObjectInput{Id: state.ID.ValueString()})
	if err == nil && !strings.EqualFold(response.DeleteOneObject.Id, state.ID.ValueString()) {
		err = errInvalidMetadataResponse
	}
	if err != nil {
		metadataError(&resp.Diagnostics, "Delete", "Object", err)
		return
	}
	object, err = readMetadataObject(ctx, r.client, state.ID.ValueString())
	if err == nil && object != nil {
		err = errInvalidMetadataResponse
	}
	if err != nil {
		metadataError(&resp.Diagnostics, "Delete", "Object", err)
		return
	}
	resp.State.RemoveResource(ctx)
}
func (r *objectResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !metadataUUID(req.ID) {
		resp.Diagnostics.AddError("Invalid Import Identifier", "Use the native nonzero object UUID, not a name or composite ID.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), strings.ToLower(req.ID))...)
}
