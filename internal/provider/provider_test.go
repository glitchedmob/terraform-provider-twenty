// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	providerschema "github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestProviderMetadata(t *testing.T) {
	t.Parallel()

	p := New("test")()
	response := &provider.MetadataResponse{}
	p.Metadata(t.Context(), provider.MetadataRequest{}, response)
	if response.TypeName != "twenty" || response.Version != "test" {
		t.Fatalf("unexpected provider metadata: %#v", response)
	}
	if New("test")() == p {
		t.Fatal("factory must return independent provider instances")
	}
}

func TestProviderSchema(t *testing.T) {
	t.Parallel()

	p := New("test")()
	response := &provider.SchemaResponse{}
	p.Schema(t.Context(), provider.SchemaRequest{}, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	if len(response.Schema.Attributes) != 3 || len(response.Schema.Blocks) != 0 {
		t.Fatal("expected exactly three provider attributes and no blocks")
	}
	for name, sensitive := range map[string]bool{"endpoint": false, "email": false, "password": true} {
		attribute, ok := response.Schema.Attributes[name].(providerschema.StringAttribute)
		if !ok || !attribute.Optional || attribute.Required || attribute.Sensitive != sensitive {
			t.Fatalf("incorrect schema flags for %s", name)
		}
		if !strings.Contains(attribute.MarkdownDescription, "TWENTY_"+strings.ToUpper(name)) {
			t.Fatalf("missing environment variable documentation for %s", name)
		}
	}
	if len(p.Resources(t.Context())) != 0 || len(p.DataSources(t.Context())) != 0 {
		t.Fatal("the scaffold must not register resources or data sources")
	}
}

func TestProviderProtocolSchema(t *testing.T) {
	t.Parallel()

	server, err := providerserver.NewProtocol6WithError(New("test")())()
	if err != nil {
		t.Fatalf("create protocol server: %v", err)
	}
	response, err := server.GetProviderSchema(t.Context(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatalf("get protocol schema: %v", err)
	}
	if len(response.Diagnostics) != 0 || response.Provider == nil || response.Provider.Block == nil {
		t.Fatal("protocol server must export a provider schema without diagnostics")
	}
	if len(response.Provider.Block.Attributes) != 3 || len(response.ResourceSchemas) != 0 || len(response.DataSourceSchemas) != 0 {
		t.Fatal("protocol server must export only the configuration schema")
	}
}

func TestProviderConfigureWithoutConfig(t *testing.T) {
	// Deliberately set unusable environment values. Schema-only Configure must
	// return before it reads credentials or tries to build an authenticated client.
	t.Setenv("TWENTY_ENDPOINT", "not-an-endpoint")
	t.Setenv("TWENTY_EMAIL", "")
	t.Setenv("TWENTY_PASSWORD", "")

	p := New("test")()
	for name, config := range map[string]tfsdk.Config{
		"absent":   {},
		"null":     testConfig(t, p, nil),
		"all null": testConfig(t, p, map[string]any{}),
	} {
		t.Run(name, func(t *testing.T) {
			response := &provider.ConfigureResponse{}
			p.Configure(t.Context(), provider.ConfigureRequest{Config: config}, response)
			if len(response.Diagnostics) != 0 || response.ResourceData != nil || response.DataSourceData != nil {
				t.Fatal("schema-only configure must return without diagnostics or provider data")
			}
		})
	}
}

func TestProviderConfigureNoNetwork(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the scaffold must not make HTTP requests")
	}))
	defer server.Close()

	p := New("test")()
	config := testConfig(t, p, map[string]any{
		"endpoint": " " + server.URL + " ",
		"email":    " automation@example.com ",
		"password": " test-password ",
	})
	response := &provider.ConfigureResponse{}
	p.Configure(t.Context(), provider.ConfigureRequest{Config: config}, response)
	if response.Diagnostics.HasError() {
		t.Fatal("valid scaffold configuration must not produce errors")
	}
	resolved, ok := response.ResourceData.(*providerConfig)
	if !ok || response.DataSourceData != response.ResourceData {
		t.Fatal("expected shared configuration placeholder, not a network client")
	}
	if resolved.endpoint != server.URL || resolved.email != "automation@example.com" || resolved.password != " test-password " {
		t.Fatal("explicit configuration was not resolved correctly")
	}
}

func TestProviderConfigureEnvironmentFallback(t *testing.T) {
	t.Setenv("TWENTY_ENDPOINT", " http://localhost:3000 ")
	t.Setenv("TWENTY_EMAIL", " automation@example.com ")
	t.Setenv("TWENTY_PASSWORD", " test-password ")

	p := New("test")()
	// One explicit attribute distinguishes this request from schema-only config.
	config := testConfig(t, p, map[string]any{"endpoint": "http://localhost:3001"})
	response := &provider.ConfigureResponse{}
	p.Configure(t.Context(), provider.ConfigureRequest{Config: config}, response)
	resolved, ok := response.ResourceData.(*providerConfig)
	if response.Diagnostics.HasError() || !ok {
		t.Fatal("expected resolved configuration")
	}
	if resolved.endpoint != "http://localhost:3001" || resolved.email != "automation@example.com" || resolved.password != " test-password " {
		t.Fatal("environment fallback or explicit precedence failed")
	}
}

func TestProviderConfigureUnknownValues(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"endpoint", "email", "password"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := New("test")()
			config := testConfig(t, p, map[string]any{name: tftypes.UnknownValue})
			response := &provider.ConfigureResponse{}
			p.Configure(t.Context(), provider.ConfigureRequest{Config: config}, response)
			if !response.Diagnostics.HasError() || len(response.Diagnostics) != 1 {
				t.Fatal("unknown configuration must produce one error")
			}
			if response.ResourceData != nil || response.DataSourceData != nil {
				t.Fatal("unknown configuration must not provide data")
			}
			if !strings.Contains(response.Diagnostics[0].Detail(), "The "+name+" value") {
				t.Fatal("diagnostic must identify the unknown attribute")
			}
		})
	}
}

func TestProviderConfigureInvalidConfig(t *testing.T) {
	t.Parallel()

	p := New("test")()
	config := testConfig(t, p, map[string]any{})
	config.Raw = tftypes.NewValue(tftypes.String, "not-a-config-object")
	response := &provider.ConfigureResponse{}
	p.Configure(t.Context(), provider.ConfigureRequest{Config: config}, response)
	if !response.Diagnostics.HasError() || response.ResourceData != nil || response.DataSourceData != nil {
		t.Fatal("decoding invalid configuration must fail without provider data")
	}
}

func TestResolveProviderConfig(t *testing.T) {
	t.Parallel()

	nullModel := TwentyProviderModel{
		Endpoint: types.StringNull(),
		Email:    types.StringNull(),
		Password: types.StringNull(),
	}
	for name, test := range map[string]struct {
		model TwentyProviderModel
		env   map[string]string
		want  providerConfig
	}{
		"no defaults": {model: nullModel},
		"environment": {
			model: nullModel,
			env: map[string]string{
				"TWENTY_ENDPOINT": " http://localhost:3000 ",
				"TWENTY_EMAIL":    " automation@example.com ",
				"TWENTY_PASSWORD": " test-password ",
			},
			want: providerConfig{endpoint: "http://localhost:3000", email: "automation@example.com", password: " test-password "},
		},
		"explicit overrides": {
			model: TwentyProviderModel{
				Endpoint: types.StringValue(" http://localhost:3001 "),
				Email:    types.StringValue(" explicit@example.com "),
				Password: types.StringValue(" explicit-password "),
			},
			env: map[string]string{
				"TWENTY_ENDPOINT": "http://localhost:3000",
				"TWENTY_EMAIL":    "automation@example.com",
				"TWENTY_PASSWORD": "test-password",
			},
			want: providerConfig{endpoint: "http://localhost:3001", email: "explicit@example.com", password: " explicit-password "},
		},
		"explicit empty overrides": {
			model: TwentyProviderModel{
				Endpoint: types.StringValue(""),
				Email:    types.StringValue(""),
				Password: types.StringValue(""),
			},
			env: map[string]string{
				"TWENTY_ENDPOINT": "http://localhost:3000",
				"TWENTY_EMAIL":    "automation@example.com",
				"TWENTY_PASSWORD": "test-password",
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := resolveProviderConfig(test.model, func(key string) string { return test.env[key] })
			if *got != test.want {
				t.Fatal("resolved configuration differs from expected values")
			}
		})
	}
}

func testConfig(t *testing.T, p provider.Provider, values map[string]any) tfsdk.Config {
	t.Helper()

	schemaResponse := &provider.SchemaResponse{}
	p.Schema(t.Context(), provider.SchemaRequest{}, schemaResponse)
	objectType := tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"endpoint": tftypes.String,
		"email":    tftypes.String,
		"password": tftypes.String,
	}}
	raw := tftypes.NewValue(objectType, nil)
	if values != nil {
		rawValues := make(map[string]tftypes.Value, 3)
		for _, name := range []string{"endpoint", "email", "password"} {
			rawValues[name] = tftypes.NewValue(tftypes.String, values[name])
		}
		raw = tftypes.NewValue(objectType, rawValues)
	}
	return tfsdk.Config{
		Raw:    raw,
		Schema: schemaResponse.Schema,
	}
}
