// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource                   = &objectDataSource{}
	_ datasource.DataSourceWithConfigure      = &objectDataSource{}
	_ datasource.DataSourceWithValidateConfig = &objectDataSource{}
)

type objectDataSource struct{ client graphql.Client }
type objectDataSourceModel struct {
	ID            types.String `tfsdk:"id"`
	ObjectID      types.String `tfsdk:"object_id"`
	NameSingular  types.String `tfsdk:"name_singular"`
	NamePlural    types.String `tfsdk:"name_plural"`
	LabelSingular types.String `tfsdk:"label_singular"`
	LabelPlural   types.String `tfsdk:"label_plural"`
	Description   types.String `tfsdk:"description"`
	Icon          types.String `tfsdk:"icon"`
	IsSystem      types.Bool   `tfsdk:"is_system"`
	IsActive      types.Bool   `tfsdk:"is_active"`
	ApplicationID types.String `tfsdk:"application_id"`
}

func NewObjectDataSource() datasource.DataSource { return &objectDataSource{} }
func (d *objectDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_object"
}
func (d *objectDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{MarkdownDescription: "Reads Twenty object metadata, including built-in objects, without modifying it. Requires the DATA_MODEL settings permission because the provider selects the guarded icon field. Specify exactly one of object_id or name_singular. Does not read CRM records.", Attributes: map[string]schema.Attribute{
		"id":             schema.StringAttribute{Computed: true, MarkdownDescription: "Native object UUID."},
		"object_id":      schema.StringAttribute{Optional: true, MarkdownDescription: "Native nonzero object UUID selector. UUID matching is case-insensitive. Specify exactly one of object_id or name_singular."},
		"name_singular":  schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Singular name. As a selector, matching is exact and case-sensitive without trimming. Specify exactly one of object_id or name_singular."},
		"name_plural":    schema.StringAttribute{Computed: true, MarkdownDescription: "Plural object name."},
		"label_singular": schema.StringAttribute{Computed: true, MarkdownDescription: "Singular display label."},
		"label_plural":   schema.StringAttribute{Computed: true, MarkdownDescription: "Plural display label."},
		"description":    schema.StringAttribute{Computed: true, MarkdownDescription: "Object description, or null."},
		"icon":           schema.StringAttribute{Computed: true, MarkdownDescription: "Icon identifier, or null."},
		"is_system":      schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether Twenty marks the object as system metadata. False alone does not establish custom ownership."},
		"is_active":      schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the object is active."},
		"application_id": schema.StringAttribute{Computed: true, MarkdownDescription: "UUID of the application that owns the object."},
	}}
}
func (d *objectDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = nil
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*ClientData)
	if !ok || data == nil || data.Client == nil {
		resp.Diagnostics.AddError("Invalid Twenty Client", "The object data source did not receive an authenticated Twenty client.")
		return
	}
	d.client = data.Client.Client()
}
func validateObjectSelector(config objectDataSourceModel, known bool) diag.Diagnostics {
	var d diag.Diagnostics
	if config.ObjectID.IsNull() == config.NameSingular.IsNull() {
		d.AddError("Invalid Twenty Object Selector", "Specify exactly one of object_id or name_singular.")
		return d
	}
	if config.ObjectID.IsUnknown() || config.NameSingular.IsUnknown() {
		if known {
			d.AddError("Unknown Twenty Object Selector", "The object selector must be known before reading.")
		}
		return d
	}
	if !config.ObjectID.IsNull() && !metadataUUID(config.ObjectID.ValueString()) {
		d.AddAttributeError(path.Root("object_id"), "Invalid Twenty Object UUID", "Use a native nonzero UUID with hyphens in groups of 8-4-4-4-12.")
	}
	if !config.NameSingular.IsNull() && strings.TrimSpace(config.NameSingular.ValueString()) == "" {
		d.AddAttributeError(path.Root("name_singular"), "Invalid Twenty Object Name", "The selector must be nonblank. Names are matched exactly without trimming or case normalization.")
	}
	return d
}
func (d *objectDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var config objectDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(validateObjectSelector(config, false)...)
	}
}
func (d *objectDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config objectDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateObjectSelector(config, true)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if d.client == nil {
		resp.Diagnostics.AddError("Twenty Client Not Configured", "Configure the Twenty provider before reading an object.")
		return
	}
	objects, err := readMetadataObjects(ctx, d.client)
	if err != nil {
		metadataError(&resp.Diagnostics, "Read", "Object", err)
		return
	}
	selected, count := -1, 0
	for i, object := range objects {
		if !config.ObjectID.IsNull() && strings.EqualFold(config.ObjectID.ValueString(), object.Id) || !config.NameSingular.IsNull() && config.NameSingular.ValueString() == object.NameSingular {
			selected, count = i, count+1
		}
	}
	if count == 0 {
		metadataError(&resp.Diagnostics, "Read", "Object", errMetadataNotFound)
		return
	}
	if count != 1 {
		metadataError(&resp.Diagnostics, "Read", "Object", errAmbiguousMetadata)
		return
	}
	object := objects[selected]
	state := objectDataSourceModel{
		ID: types.StringValue(strings.ToLower(object.Id)), ObjectID: config.ObjectID,
		NameSingular: types.StringValue(object.NameSingular), NamePlural: types.StringValue(object.NamePlural),
		LabelSingular: types.StringValue(object.LabelSingular), LabelPlural: types.StringValue(object.LabelPlural),
		Description: roleNullableString(object.Description), Icon: roleNullableString(object.Icon),
		IsSystem: types.BoolValue(object.IsSystem), IsActive: types.BoolValue(object.IsActive), ApplicationID: types.StringValue(object.ApplicationId),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
