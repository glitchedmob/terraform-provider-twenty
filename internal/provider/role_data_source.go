// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"

	"github.com/Khan/genqlient/graphql"
	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource                   = &roleDataSource{}
	_ datasource.DataSourceWithConfigure      = &roleDataSource{}
	_ datasource.DataSourceWithValidateConfig = &roleDataSource{}
)

type roleDataSource struct {
	client graphql.Client
}

// NewRoleDataSource returns the read-only Twenty role lookup.
func NewRoleDataSource() datasource.DataSource {
	return &roleDataSource{}
}

func (d *roleDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_role"
}

func (d *roleDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads an existing Twenty role through Metadata GraphQL. Specify exactly one of `role_id` or `label`. Requires the `ROLES` settings permission.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Stable UUID of the matching role.",
				Computed:            true,
			},
			"role_id": schema.StringAttribute{
				MarkdownDescription: "Role UUID to look up. Specify exactly one of `role_id` or `label`.",
				Optional:            true,
			},
			"label": schema.StringAttribute{
				MarkdownDescription: "Role label. When used as the selector, matching is exact and case-sensitive, including whitespace. A label must identify exactly one role. Specify exactly one of `role_id` or `label`.",
				Optional:            true,
				Computed:            true,
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Role description, or null when the server has none.",
				Computed:            true,
			},
			"icon": schema.StringAttribute{
				MarkdownDescription: "Role icon identifier, or null when the server has none.",
				Computed:            true,
			},
			"is_editable": schema.BoolAttribute{
				MarkdownDescription: "Whether Twenty permits editing this role.",
				Computed:            true,
			},
			"can_be_assigned_to_users": schema.BoolAttribute{
				MarkdownDescription: "Whether the role can be assigned to workspace users.",
				Computed:            true,
			},
			"can_be_assigned_to_agents": schema.BoolAttribute{
				MarkdownDescription: "Whether the role can be assigned to agents.",
				Computed:            true,
			},
			"can_be_assigned_to_api_keys": schema.BoolAttribute{
				MarkdownDescription: "Whether the role can be assigned to API keys.",
				Computed:            true,
			},
			"can_update_all_settings": schema.BoolAttribute{
				MarkdownDescription: "Whether the role grants access to update all workspace settings.",
				Computed:            true,
			},
			"can_access_all_tools": schema.BoolAttribute{
				MarkdownDescription: "Whether the role grants access to all tools.",
				Computed:            true,
			},
			"can_read_all_object_records": schema.BoolAttribute{
				MarkdownDescription: "Whether the role grants read access to all object records. This data source does not read CRM records.",
				Computed:            true,
			},
			"can_update_all_object_records": schema.BoolAttribute{
				MarkdownDescription: "Whether the role grants update access to all object records.",
				Computed:            true,
			},
			"can_soft_delete_all_object_records": schema.BoolAttribute{
				MarkdownDescription: "Whether the role grants soft-delete access to all object records.",
				Computed:            true,
			},
			"can_destroy_all_object_records": schema.BoolAttribute{
				MarkdownDescription: "Whether the role grants permanent-delete access to all object records.",
				Computed:            true,
			},
			"permission_flags": schema.SetAttribute{
				MarkdownDescription: "Explicit permission flag keys returned by Twenty. Global capability booleans can grant additional access. Null means the server did not return a flag list; an empty set means it returned no explicit flags.",
				Computed:            true,
				ElementType:         types.StringType,
			},
		},
	}
}

func (d *roleDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var config roleModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(validateRoleSelector(config, false)...)
	}
}

func (d *roleDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = nil
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*ClientData)
	if !ok || data == nil || data.Client == nil {
		resp.Diagnostics.AddError("Invalid Twenty Client", "The role data source did not receive an authenticated Twenty client. Report this provider configuration error.")
		return
	}
	d.client = data.Client.Client()
}

func (d *roleDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config roleModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateRoleSelector(config, true)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if d.client == nil {
		resp.Diagnostics.AddError("Twenty Client Not Configured", "Configure the Twenty provider before reading a role.")
		return
	}

	response, err := client.GetRoles(ctx, roleQueryClient{Client: d.client})
	if err != nil {
		// Preserve only fixed, known classifications. Never print upstream errors.
		detail := client.DiagnosticMessage(err)
		if errors.Is(err, errInvalidRoleResponse) {
			detail = errInvalidRoleResponse.Error()
		}
		resp.Diagnostics.AddError("Unable to Read Twenty Roles", detail+". Role listing requires the account's ROLES settings permission.")
		return
	}
	role, count := selectRole(response.GetRoles, config)
	if count == 0 {
		resp.Diagnostics.AddError("Twenty Role Not Found", "No role matches the configured selector in the authenticated workspace.")
		return
	}
	if count != 1 {
		resp.Diagnostics.AddError("Ambiguous Twenty Role", "More than one role matches the configured selector. Use a unique role UUID instead of an ambiguous label.")
		return
	}

	state := roleModelFromAPI(ctx, *role, config.RoleID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func validateRoleSelector(config roleModel, requireKnown bool) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if config.RoleID.IsNull() == config.Label.IsNull() {
		diagnostics.AddError("Invalid Twenty Role Selector", "Specify exactly one of role_id or label.")
		return diagnostics
	}
	if config.RoleID.IsUnknown() || config.Label.IsUnknown() {
		if requireKnown {
			diagnostics.AddError("Unknown Twenty Role Selector", "The role selector must be known before reading the role.")
		}
		return diagnostics
	}
	if !config.RoleID.IsNull() && !validRoleUUID(config.RoleID.ValueString()) {
		diagnostics.AddAttributeError(path.Root("role_id"), "Invalid Twenty Role UUID", "The role_id must use UUID syntax, with 32 hexadecimal digits separated by hyphens in groups of 8-4-4-4-12.")
	}
	if !config.Label.IsNull() && blankRoleLabel(config.Label.ValueString()) {
		diagnostics.AddAttributeError(path.Root("label"), "Invalid Twenty Role Label", "The label must contain a non-whitespace character. Labels are matched exactly, without trimming or case normalization.")
	}
	return diagnostics
}
