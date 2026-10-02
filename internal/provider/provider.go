// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"os"
	"strings"

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
		MarkdownDescription: "Twenty IAM and configuration through Metadata GraphQL with an in-memory password session. Intended target: Twenty v2.44.0. Use a dedicated verified automation account and keep an independent recovery administrator.",
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				MarkdownDescription: "Twenty instance HTTPS base URL, without the `/metadata` suffix, credentials, other paths, query, or fragment. May also be set with `TWENTY_ENDPOINT`. No default. Explicit values, including empty values, override the environment. Redirects are rejected.",
				Optional:            true,
			},
			"email": schema.StringAttribute{
				MarkdownDescription: "Dedicated verified automation account email for password-session authentication. Must be one bare ASCII mailbox address without a display name, comments, or control characters. Surrounding spaces are trimmed. May also be set with `TWENTY_EMAIL`. Explicit values, including empty values, override the environment. MFA and CAPTCHA flows are not supported.",
				Optional:            true,
			},
			"password": schema.StringAttribute{
				MarkdownDescription: "Automation account password for a new in-memory session on each provider configuration. May also be set with `TWENTY_PASSWORD`. Explicit values, including empty values, override the environment. Sensitive values can still appear in saved Terraform plans; inject credentials through the environment.",
				Optional:            true,
				Sensitive:           true,
			},
			"allow_insecure_http": schema.BoolAttribute{
				MarkdownDescription: "Allow unencrypted HTTP only for deliberate local testing with localhost or a literal loopback IP address. Defaults to false. No environment fallback. HTTPS certificate verification is never disabled.",
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
	return nil
}

func (p *TwentyProvider) DataSources(context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{NewRoleDataSource}
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
