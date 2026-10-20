// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource                   = &fieldDataSource{}
	_ datasource.DataSourceWithConfigure      = &fieldDataSource{}
	_ datasource.DataSourceWithValidateConfig = &fieldDataSource{}
)

type fieldDataSource struct{ client graphql.Client }
type fieldDataSourceModel struct {
	ID            types.String `tfsdk:"id"`
	FieldID       types.String `tfsdk:"field_id"`
	ObjectID      types.String `tfsdk:"object_id"`
	Name          types.String `tfsdk:"name"`
	Type          types.String `tfsdk:"type"`
	Label         types.String `tfsdk:"label"`
	Description   types.String `tfsdk:"description"`
	Icon          types.String `tfsdk:"icon"`
	IsSystem      types.Bool   `tfsdk:"is_system"`
	IsActive      types.Bool   `tfsdk:"is_active"`
	ApplicationID types.String `tfsdk:"application_id"`
}

func NewFieldDataSource() datasource.DataSource { return &fieldDataSource{} }
func (d *fieldDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_field"
}
func (d *fieldDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{MarkdownDescription: "Reads field metadata without changing it. Choose field_id or the pair object_id and name. Built-in and complex field types can be read.", Attributes: map[string]schema.Attribute{
		"id":             schema.StringAttribute{Computed: true, MarkdownDescription: "Native UUID of the matching field."},
		"field_id":       schema.StringAttribute{Optional: true, MarkdownDescription: "Native field UUID. Cannot be combined with object_id or name."},
		"object_id":      schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Parent object UUID. Supply with name, or read it from a field_id lookup."},
		"name":           schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Exact, case-sensitive field name within object_id. Supply with object_id, or read it from a field_id lookup."},
		"type":           schema.StringAttribute{Computed: true, MarkdownDescription: "Twenty field type, including types unsupported by the field resource."},
		"label":          schema.StringAttribute{Computed: true, MarkdownDescription: "Display label."},
		"description":    schema.StringAttribute{Computed: true, MarkdownDescription: "Description, or null when absent."},
		"icon":           schema.StringAttribute{Computed: true, MarkdownDescription: "Icon identifier, or null when absent."},
		"is_system":      schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether Twenty marks this field as system metadata."},
		"is_active":      schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the field is active."},
		"application_id": schema.StringAttribute{Computed: true, MarkdownDescription: "UUID of the application that owns this field."},
	}}
}
func (d *fieldDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = nil
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*ClientData)
	if !ok || data == nil || data.Client == nil {
		resp.Diagnostics.AddError("Invalid Twenty Client", "The field data source did not receive an authenticated Twenty session.")
		return
	}
	d.client = data.Client.Client()
}
func (d *fieldDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var config fieldDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(validateFieldSelector(config, false)...)
	}
}
func validateFieldSelector(config fieldDataSourceModel, known bool) diag.Diagnostics {
	var d diag.Diagnostics
	// Optional+computed selectors remain null in configuration when omitted.
	byID := !config.FieldID.IsNull()
	if byID && (!config.ObjectID.IsNull() || !config.Name.IsNull()) || !byID && (config.ObjectID.IsNull() || config.Name.IsNull()) {
		d.AddError("Invalid Twenty Field Selector", "Specify field_id alone, or both object_id and name.")
		return d
	}
	// Unknown dependencies are deferred until Read, not conflicting selectors.
	if config.FieldID.IsUnknown() || config.ObjectID.IsUnknown() || config.Name.IsUnknown() {
		if known {
			d.AddError("Unknown Twenty Field Selector", "The field selector must be known before reading.")
		}
		return d
	}
	for _, field := range []struct {
		name  string
		value types.String
	}{{"field_id", config.FieldID}, {"object_id", config.ObjectID}} {
		if !field.value.IsNull() && !metadataUUID(field.value.ValueString()) {
			d.AddAttributeError(path.Root(field.name), "Invalid Twenty Field UUID", "Use a native UUID with hyphens in groups of 8-4-4-4-12.")
		}
	}
	if !config.Name.IsNull() && strings.TrimSpace(config.Name.ValueString()) == "" {
		d.AddAttributeError(path.Root("name"), "Invalid Twenty Field Name", "The exact field name must not be blank.")
	}
	return d
}
func (d *fieldDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config fieldDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateFieldSelector(config, true)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if d.client == nil {
		resp.Diagnostics.AddError("Twenty Client Not Configured", "Configure the Twenty provider before reading a field.")
		return
	}
	var field *client.FieldMetadata
	var err error
	if !config.FieldID.IsNull() {
		field, err = readMetadataField(ctx, d.client, config.FieldID.ValueString())
	} else {
		var parent *client.ObjectMetadata
		parent, err = readMetadataObject(ctx, d.client, config.ObjectID.ValueString())
		if err == nil && parent == nil {
			err = errMetadataNotFound
		}
		if err == nil {
			var fields []client.FieldMetadata
			fields, err = readMetadataFields(ctx, d.client, config.ObjectID.ValueString())
			if err == nil {
				for i := range fields {
					if fields[i].Name != config.Name.ValueString() {
						continue
					}
					if field != nil {
						err = errAmbiguousMetadata
						break
					}
					field = &fields[i]
				}
			}
		}
	}
	if err == nil && field == nil {
		err = errMetadataNotFound
	}
	if err != nil {
		metadataError(&resp.Diagnostics, "Read", "Field", err)
		return
	}
	state := fieldDataSourceModel{
		ID: types.StringValue(strings.ToLower(field.Id)), FieldID: config.FieldID,
		ObjectID: config.ObjectID, Name: config.Name,
		Type: types.StringValue(string(field.Type)), Label: types.StringValue(field.Label),
		Description: roleNullableString(field.Description), Icon: roleNullableString(field.Icon),
		IsSystem: types.BoolValue(field.IsSystem.GetOrEmpty()), IsActive: types.BoolValue(field.IsActive.GetOrEmpty()), ApplicationID: types.StringValue(strings.ToLower(field.ApplicationId)),
	}
	if config.ObjectID.IsNull() {
		state.ObjectID = types.StringValue(strings.ToLower(field.ObjectMetadataId))
	}
	if config.Name.IsNull() {
		state.Name = types.StringValue(field.Name)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
