// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"

	"github.com/Khan/genqlient/graphql"
	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &workspaceDataSource{}
	_ datasource.DataSourceWithConfigure = &workspaceDataSource{}
)

type workspaceDataSource struct {
	client   graphql.Client
	identity client.Identity
}

// NewWorkspaceDataSource returns the read-only authenticated workspace lookup.
func NewWorkspaceDataSource() datasource.DataSource {
	return &workspaceDataSource{}
}

func (d *workspaceDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workspace"
}

func (d *workspaceDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads the authenticated session's current Twenty workspace through Metadata GraphQL. Takes no selectors and cannot look up another workspace. No settings permission is required.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Stable UUID of the authenticated workspace.",
				Computed:            true,
			},
			"display_name": schema.StringAttribute{
				MarkdownDescription: "Workspace display name, or null when the server has none.",
				Computed:            true,
			},
			"default_role_id": schema.StringAttribute{
				MarkdownDescription: "UUID of the workspace's default role, or null when no default role is set. This is not the authenticated member's role.",
				Computed:            true,
			},
			"activation_status": schema.StringAttribute{
				MarkdownDescription: "Workspace activation status returned by Twenty, such as ACTIVE. The provider authenticates only active workspaces.",
				Computed:            true,
			},
			"subdomain": schema.StringAttribute{
				MarkdownDescription: "Workspace subdomain returned by Twenty.",
				Computed:            true,
			},
			"custom_domain": schema.StringAttribute{
				MarkdownDescription: "Workspace custom domain, or null when none is configured.",
				Computed:            true,
			},
			"subdomain_url": schema.StringAttribute{
				MarkdownDescription: "Absolute workspace subdomain URL returned by Twenty. This can differ from the configured provider endpoint.",
				Computed:            true,
			},
			"custom_url": schema.StringAttribute{
				MarkdownDescription: "Absolute workspace custom-domain URL, or null when the server has none.",
				Computed:            true,
			},
			"workspace_members_count": schema.Int64Attribute{
				MarkdownDescription: "Workspace user count returned by Twenty, excluding pending invitations. Null means the server returned no count, not zero.",
				Computed:            true,
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "Workspace creation timestamp in RFC3339 format.",
				Computed:            true,
			},
			"updated_at": schema.StringAttribute{
				MarkdownDescription: "Workspace last-update timestamp in RFC3339 format.",
				Computed:            true,
			},
		},
	}
}

func (d *workspaceDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client, d.identity = nil, client.Identity{}
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*ClientData)
	if !ok || data == nil || data.Client == nil {
		resp.Diagnostics.AddError("Invalid Twenty Client", "The workspace data source did not receive an authenticated Twenty client. Report this provider configuration error.")
		return
	}
	d.client, d.identity = data.Client.Client(), data.Client.Identity()
}

func (d *workspaceDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.client == nil {
		resp.Diagnostics.AddError("Twenty Client Not Configured", "Configure the Twenty provider before reading the current workspace.")
		return
	}
	workspace, err := readCurrentWorkspace(ctx, d.client, d.identity)
	if err != nil {
		detail := client.DiagnosticMessage(err)
		switch {
		case errors.Is(err, errInvalidWorkspaceResponse):
			detail = errInvalidWorkspaceResponse.Error()
		case errors.Is(err, errWorkspaceIdentityMismatch):
			detail = errWorkspaceIdentityMismatch.Error()
		}
		resp.Diagnostics.AddError("Unable to Read Twenty Workspace", detail+". No workspace settings are changed by this data source.")
		return
	}
	state := workspaceModelFromAPI(*workspace)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

type workspaceModel struct {
	ID                    types.String `tfsdk:"id"`
	DisplayName           types.String `tfsdk:"display_name"`
	DefaultRoleID         types.String `tfsdk:"default_role_id"`
	ActivationStatus      types.String `tfsdk:"activation_status"`
	Subdomain             types.String `tfsdk:"subdomain"`
	CustomDomain          types.String `tfsdk:"custom_domain"`
	SubdomainURL          types.String `tfsdk:"subdomain_url"`
	CustomURL             types.String `tfsdk:"custom_url"`
	WorkspaceMembersCount types.Int64  `tfsdk:"workspace_members_count"`
	CreatedAt             types.String `tfsdk:"created_at"`
	UpdatedAt             types.String `tfsdk:"updated_at"`
}
