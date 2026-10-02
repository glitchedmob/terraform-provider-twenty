// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"os"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ provider.Provider = &TwentyProvider{}

// TwentyProvider implements the initial, configuration-only provider.
type TwentyProvider struct {
	version string
}

// TwentyProviderModel describes the provider configuration.
type TwentyProviderModel struct {
	Endpoint types.String `tfsdk:"endpoint"`
	Email    types.String `tfsdk:"email"`
	Password types.String `tfsdk:"password"`
}

// providerConfig is a temporary handoff type, not an authenticated API client.
// Keep credentials in provider-process memory, never in resource state or logs.
type providerConfig struct {
	endpoint string
	email    string
	password string
}

func (p *TwentyProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "twenty"
	resp.Version = p.version
}

func (p *TwentyProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Initial scaffold for Twenty IAM and configuration through Metadata GraphQL. Authentication, resources, and data sources are not implemented. Intended target: Twenty v2.44.0.",
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				MarkdownDescription: "Twenty instance base URL, without the `/metadata` suffix. May also be set with `TWENTY_ENDPOINT`. No default. Explicit values override the environment. This scaffold does not contact the endpoint.",
				Optional:            true,
			},
			"email": schema.StringAttribute{
				MarkdownDescription: "Dedicated automation account email for forthcoming password-session authentication. May also be set with `TWENTY_EMAIL`. Explicit values override the environment. This scaffold does not authenticate.",
				Optional:            true,
			},
			"password": schema.StringAttribute{
				MarkdownDescription: "Automation account password for forthcoming password-session authentication. May also be set with `TWENTY_PASSWORD`. Explicit values override the environment. Sensitive values can still appear in saved Terraform plans; inject credentials through the environment.",
				Optional:            true,
				Sensitive:           true,
			},
		},
	}
}

func (p *TwentyProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	// Schema tools can configure the server without a provider configuration.
	// Return before decoding configuration or reading credential environment variables.
	if req.Config.Raw.Type() == nil || req.Config.Raw.IsNull() {
		return
	}

	var config TwentyProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if config.Endpoint.IsNull() && config.Email.IsNull() && config.Password.IsNull() {
		return
	}

	for name, value := range map[string]types.String{
		"endpoint": config.Endpoint,
		"email":    config.Email,
		"password": config.Password,
	} {
		if value.IsUnknown() {
			resp.Diagnostics.AddAttributeError(
				path.Root(name),
				"Unknown Twenty Provider Configuration",
				"The "+name+" value must be known when configuring the provider.",
			)
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}

	// Stage 1 only resolves configuration. Do not add authentication or network
	// requests here until the generated client and isolated container tests exist.
	resolved := resolveProviderConfig(config, os.Getenv)
	resp.DataSourceData = resolved
	resp.ResourceData = resolved
}

func (p *TwentyProvider) Resources(context.Context) []func() resource.Resource {
	return nil
}

func (p *TwentyProvider) DataSources(context.Context) []func() datasource.DataSource {
	return nil
}

// New returns a provider factory for protocol server registration and tests.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &TwentyProvider{version: version}
	}
}

func resolveProviderConfig(config TwentyProviderModel, getenv func(string) string) *providerConfig {
	endpoint := getenv("TWENTY_ENDPOINT")
	if !config.Endpoint.IsNull() {
		endpoint = config.Endpoint.ValueString()
	}
	email := getenv("TWENTY_EMAIL")
	if !config.Email.IsNull() {
		email = config.Email.ValueString()
	}
	password := getenv("TWENTY_PASSWORD")
	if !config.Password.IsNull() {
		password = config.Password.ValueString()
	}
	return &providerConfig{
		endpoint: strings.TrimSpace(endpoint),
		email:    strings.TrimSpace(email),
		password: password,
	}
}
