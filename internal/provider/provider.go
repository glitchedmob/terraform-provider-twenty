// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"os"
	"strings"
	"sync"

	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ provider.Provider = &TwentyProvider{}

// TwentyProvider configures an authenticated Twenty Metadata session.
type TwentyProvider struct {
	version string
}

// TwentyProviderModel describes the provider configuration.
type TwentyProviderModel struct {
	Endpoint          types.String `tfsdk:"endpoint"`
	Email             types.String `tfsdk:"email"`
	Password          types.String `tfsdk:"password"`
	AllowInsecureHTTP types.Bool   `tfsdk:"allow_insecure_http"`
}

// ClientData shares one in-memory session with resources and data sources.
type ClientData struct {
	Client *client.Session
	// MutationLock serializes fresh safety checks and IAM mutations across all
	// resources sharing this provider configuration. It is not a server lock.
	MutationLock sync.Mutex
}

// resolvedProviderConfig exists only while configuring the session.
type resolvedProviderConfig struct {
	endpoint  string
	email     string
	password  string
	allowHTTP bool
}

func (p *TwentyProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "twenty"
	resp.Version = p.version
}

func (p *TwentyProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage Twenty roles and workspace memberships through Metadata GraphQL. Requires Twenty v2.46.0 or later and a dedicated verified automation account.",
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				MarkdownDescription: "Instance HTTPS base URL without `/metadata`, other paths, credentials, query, or fragment. Environment fallback: `TWENTY_ENDPOINT`. No default.",
				Optional:            true,
			},
			"email": schema.StringAttribute{
				MarkdownDescription: "Verified automation account email. Use one bare ASCII mailbox address, without a display name or comments. Surrounding spaces are trimmed. Environment fallback: `TWENTY_EMAIL`.",
				Optional:            true,
			},
			"password": schema.StringAttribute{
				MarkdownDescription: "Automation account password; whitespace is preserved. Environment fallback: `TWENTY_PASSWORD`. Inject through the environment or an ephemeral variable to avoid saving it in plans.",
				Optional:            true,
				Sensitive:           true,
			},
			"allow_insecure_http": schema.BoolAttribute{
				MarkdownDescription: "Allow HTTP for local testing on localhost or a literal loopback IP. Defaults to false. No environment fallback; HTTPS certificate verification remains enabled.",
				Optional:            true,
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
	if diags := req.Config.Get(ctx, &config); diags.HasError() {
		resp.Diagnostics.AddError("Invalid Twenty Provider Configuration", "Provider configuration must contain string credentials and a boolean allow_insecure_http value.")
		return
	}
	resolved, diags := resolveProviderConfig(config, os.Getenv)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	session, err := client.NewSession(ctx, resolved.endpoint, resolved.email, resolved.password, resolved.allowHTTP)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Configure Twenty Provider", client.DiagnosticMessage(err))
		return
	}
	data := &ClientData{Client: session}
	resp.DataSourceData = data
	resp.ResourceData = data
}

func (p *TwentyProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{NewRoleResource, NewWorkspaceMemberResource}
}

func (p *TwentyProvider) DataSources(context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{NewRoleDataSource, NewWorkspaceDataSource}
}

// New returns a provider factory for protocol server registration and tests.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &TwentyProvider{version: version}
	}
}

func resolveProviderConfig(config TwentyProviderModel, getenv func(string) string) (resolvedProviderConfig, diag.Diagnostics) {
	var diags diag.Diagnostics
	attributes := []struct {
		name  string
		value types.String
	}{
		{"endpoint", config.Endpoint},
		{"email", config.Email},
		{"password", config.Password},
	}
	for _, attribute := range attributes {
		if attribute.value.IsUnknown() {
			diags.AddAttributeError(path.Root(attribute.name), "Unknown Twenty Provider Configuration", "The "+attribute.name+" value must be known when configuring the provider.")
		}
	}
	if config.AllowInsecureHTTP.IsUnknown() {
		diags.AddAttributeError(path.Root("allow_insecure_http"), "Unknown Twenty Provider Configuration", "The allow_insecure_http value must be known when configuring the provider.")
	}
	if diags.HasError() {
		return resolvedProviderConfig{}, diags
	}
	values := make([]string, len(attributes))
	for i, attribute := range attributes {
		if attribute.value.IsNull() {
			values[i] = getenv("TWENTY_" + strings.ToUpper(attribute.name))
		} else {
			values[i] = attribute.value.ValueString()
		}
		if strings.TrimSpace(values[i]) == "" {
			diags.AddAttributeError(path.Root(attribute.name), "Missing Twenty Provider Configuration", "Set a nonempty "+attribute.name+" value or its TWENTY_"+strings.ToUpper(attribute.name)+" environment variable. Explicit empty values do not use environment fallback.")
		}
	}
	if strings.TrimSpace(values[1]) != "" {
		if err := client.ValidateEmail(values[1]); err != nil {
			diags.AddAttributeError(path.Root("email"), "Invalid Twenty Provider Configuration", client.DiagnosticMessage(err))
		}
	}
	return resolvedProviderConfig{
		endpoint: strings.TrimSpace(values[0]), email: strings.TrimSpace(values[1]), password: values[2],
		allowHTTP: config.AllowInsecureHTTP.ValueBool(),
	}, diags
}
