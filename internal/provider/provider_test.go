// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
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
		t.Fatal("unexpected provider metadata")
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
		t.Fatal(response.Diagnostics)
	}
	if len(response.Schema.Attributes) != 4 || len(response.Schema.Blocks) != 0 {
		t.Fatal("expected four provider attributes and no blocks")
	}
	for name, sensitive := range map[string]bool{"endpoint": false, "email": false, "password": true} {
		attribute, ok := response.Schema.Attributes[name].(providerschema.StringAttribute)
		if !ok || !attribute.Optional || attribute.Required || attribute.Sensitive != sensitive || !strings.Contains(attribute.MarkdownDescription, "TWENTY_"+strings.ToUpper(name)) {
			t.Fatalf("incorrect schema or environment documentation for %s", name)
		}
	}
	if !strings.Contains(response.Schema.Attributes["email"].GetMarkdownDescription(), "bare ASCII mailbox address") {
		t.Fatal("email schema must document the local mailbox validation contract")
	}
	allow, ok := response.Schema.Attributes["allow_insecure_http"].(providerschema.BoolAttribute)
	if !ok || !allow.Optional || allow.Required || !strings.Contains(allow.MarkdownDescription, "Defaults to false") {
		t.Fatal("local HTTP must be an optional deliberate opt-in with a false default")
	}
	if len(p.Resources(t.Context())) != 1 || len(p.DataSources(t.Context())) != 2 {
		t.Fatal("stage 4B must register only the role resource and the role/workspace data sources")
	}
}

func TestProviderProtocolSchema(t *testing.T) {
	t.Parallel()
	server, err := providerserver.NewProtocol6WithError(New("test")())()
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.GetProviderSchema(t.Context(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Diagnostics) != 0 || response.Provider == nil || response.Provider.Block == nil || len(response.Provider.Block.Attributes) != 4 || len(response.ResourceSchemas) != 1 || response.ResourceSchemas["twenty_role"] == nil || len(response.DataSourceSchemas) != 2 || response.DataSourceSchemas["twenty_role"] == nil || response.DataSourceSchemas["twenty_workspace"] == nil {
		t.Fatal("protocol server must export schema without credentials")
	}
}

func TestProviderConfigureWithoutConfig(t *testing.T) {
	// Even credentials in the environment must not affect schema-only requests.
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("schema-only Configure must not make network requests")
	}))
	defer server.Close()
	t.Setenv("TWENTY_ENDPOINT", server.URL)
	t.Setenv("TWENTY_EMAIL", "schema-only@example.invalid")
	t.Setenv("TWENTY_PASSWORD", "test-schema-only-password")
	p := New("test")()
	for name, config := range map[string]tfsdk.Config{"absent": {}, "raw null": testConfig(t, p, nil)} {
		t.Run(name, func(t *testing.T) {
			response := &provider.ConfigureResponse{}
			p.Configure(t.Context(), provider.ConfigureRequest{Config: config}, response)
			if len(response.Diagnostics) != 0 || response.ResourceData != nil || response.DataSourceData != nil {
				t.Fatal("absent or raw-null configuration must return before reading credentials")
			}
		})
	}
}

func TestProviderConfigureAuthenticatedHandoff(t *testing.T) {
	t.Parallel()
	server, calls := providerAuthFixture(t)
	p := New("test")()
	response := &provider.ConfigureResponse{}
	p.Configure(t.Context(), provider.ConfigureRequest{Config: testConfig(t, p, map[string]any{
		"endpoint": " " + server.URL + "/ ", "email": " automation@example.invalid ", "password": " test-provider-password ", "allow_insecure_http": true,
	})}, response)
	data, ok := response.ResourceData.(*ClientData)
	if response.Diagnostics.HasError() || !ok || data.Client == nil || response.DataSourceData != response.ResourceData || *calls != 4 {
		t.Fatal("configuration must authenticate once and hand off one shared typed session")
	}
	if data.Client.Identity().WorkspaceMemberID != "00000000-0000-4000-8000-000000000203" {
		t.Fatal("provider handoff must contain a validated member identity")
	}
	second := &provider.ConfigureResponse{}
	p.Configure(t.Context(), provider.ConfigureRequest{Config: testConfig(t, p, map[string]any{
		"endpoint": server.URL, "email": "automation@example.invalid", "password": " test-provider-password ", "allow_insecure_http": true,
	})}, second)
	if second.Diagnostics.HasError() || second.ResourceData.(*ClientData).Client == data.Client || *calls != 8 {
		t.Fatal("each provider configuration must start a new session")
	}
}

func TestProviderConfigureEnvironmentCredentials(t *testing.T) {
	server, calls := providerAuthFixture(t)
	t.Setenv("TWENTY_ENDPOINT", " "+server.URL+" ")
	t.Setenv("TWENTY_EMAIL", " automation@example.invalid ")
	t.Setenv("TWENTY_PASSWORD", " test-provider-password ")
	p := New("test")()
	response := &provider.ConfigureResponse{}
	p.Configure(t.Context(), provider.ConfigureRequest{Config: testConfig(t, p, map[string]any{"allow_insecure_http": true})}, response)
	if response.Diagnostics.HasError() || response.ResourceData == nil || *calls != 4 {
		t.Fatal("all-null credential attributes must authenticate from environment values")
	}
}

func TestProviderAllNullObjectIsNotSchemaOnly(t *testing.T) {
	t.Setenv("TWENTY_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("TWENTY_EMAIL", "automation@example.invalid")
	t.Setenv("TWENTY_PASSWORD", "test-provider-password")
	p := New("test")()
	response := &provider.ConfigureResponse{}
	p.Configure(t.Context(), provider.ConfigureRequest{Config: testConfig(t, p, map[string]any{})}, response)
	if !response.Diagnostics.HasError() || len(response.Diagnostics) != 1 || !strings.Contains(response.Diagnostics[0].Detail(), "allow_insecure_http") {
		t.Fatal("all-null object must resolve credentials and reach endpoint validation with secure defaults")
	}
}

func TestProviderConfigureUnknownValues(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"endpoint", "email", "password", "allow_insecure_http"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := New("test")()
			response := &provider.ConfigureResponse{}
			p.Configure(t.Context(), provider.ConfigureRequest{Config: testConfig(t, p, map[string]any{name: tftypes.UnknownValue})}, response)
			if len(response.Diagnostics) != 1 || !response.Diagnostics.HasError() || response.ResourceData != nil || response.DataSourceData != nil || !strings.Contains(response.Diagnostics[0].Detail(), "The "+name+" value") {
				t.Fatal("unknown configuration must fail before fallback, authentication, or data handoff")
			}
		})
	}
}

func TestProviderConfigureMissingOrEmptyValues(t *testing.T) {
	for _, name := range []string{"endpoint", "email", "password"} {
		t.Setenv("TWENTY_"+strings.ToUpper(name), "")
	}
	p := New("test")()
	response := &provider.ConfigureResponse{}
	p.Configure(t.Context(), provider.ConfigureRequest{Config: testConfig(t, p, map[string]any{})}, response)
	if len(response.Diagnostics) != 3 || !response.Diagnostics.HasError() || response.ResourceData != nil {
		t.Fatal("empty config without credential environment must report all missing attributes")
	}
	for _, name := range []string{"endpoint", "email", "password"} {
		t.Setenv("TWENTY_"+strings.ToUpper(name), "test-only-env")
	}
	response = &provider.ConfigureResponse{}
	p.Configure(t.Context(), provider.ConfigureRequest{Config: testConfig(t, p, map[string]any{"endpoint": "", "email": "", "password": ""})}, response)
	if len(response.Diagnostics) != 3 || response.ResourceData != nil {
		t.Fatal("explicit empty credentials must never fall back to environment")
	}
}

func TestProviderConfigureInvalidConfig(t *testing.T) {
	t.Parallel()
	p := New("test")()
	config := testConfig(t, p, map[string]any{})
	config.Raw = tftypes.NewValue(tftypes.String, "test-only-secret-config")
	response := &provider.ConfigureResponse{}
	p.Configure(t.Context(), provider.ConfigureRequest{Config: config}, response)
	if !response.Diagnostics.HasError() || response.ResourceData != nil || response.DataSourceData != nil || strings.Contains(response.Diagnostics[0].Detail(), "test-only-secret") {
		t.Fatal("malformed configuration must fail without leaking its value")
	}
}

func TestProviderConfigureUnsafeEndpoint(t *testing.T) {
	t.Parallel()
	for _, endpoint := range []string{"http://remote.example.invalid", "http://localhost:1", "https://user:test-only-secret@remote.example.invalid", "https://remote.example.invalid/metadata"} {
		t.Run(endpoint, func(t *testing.T) {
			t.Parallel()
			p := New("test")()
			response := &provider.ConfigureResponse{}
			p.Configure(t.Context(), provider.ConfigureRequest{Config: testConfig(t, p, map[string]any{"endpoint": endpoint, "email": "automation@example.invalid", "password": "test-only-secret"})}, response)
			if !response.Diagnostics.HasError() || response.ResourceData != nil || strings.Contains(response.Diagnostics[0].Detail(), "test-only-secret") {
				t.Fatal("unsafe endpoint must fail with a sanitized diagnostic before network access")
			}
		})
	}
}

func TestProviderConfigureInvalidEmail(t *testing.T) {
	const wantDetail = "twenty email must be a bare ASCII mailbox address without a display name, comments, or control characters"
	for _, source := range []string{"explicit", "environment"} {
		for name, email := range map[string]string{
			"missing at":   "test-only-invalid-email",
			"display name": "Automation <automation@example.invalid>",
			"angle form":   "<automation@example.invalid>",
			"comment":      "automation@example.invalid (test-only-invalid-email)",
			"CRLF":         "automation@example.invalid\r\n",
			"control":      "auto\x1fmation@example.invalid",
			"tab":          "\tautomation@example.invalid",
			"Unicode":      "automatiön@example.invalid",
		} {
			t.Run(source+"/"+name, func(t *testing.T) {
				server, calls := providerAuthFixture(t)
				p := New("test")()
				values := map[string]any{"endpoint": server.URL, "password": " test-provider-password ", "allow_insecure_http": true}
				if source == "explicit" {
					values["email"] = email
					t.Setenv("TWENTY_EMAIL", "automation@example.invalid")
				} else {
					t.Setenv("TWENTY_EMAIL", email)
				}
				response := &provider.ConfigureResponse{}
				p.Configure(t.Context(), provider.ConfigureRequest{Config: testConfig(t, p, values)}, response)
				if len(response.Diagnostics) != 1 || !response.Diagnostics.HasError() || response.ResourceData != nil || response.DataSourceData != nil || *calls != 0 {
					t.Fatal("invalid explicit or environment email must fail before authentication or data handoff")
				}
				diagnostic, ok := response.Diagnostics[0].(diag.DiagnosticWithPath)
				if !ok || !diagnostic.Path().Equal(path.Root("email")) || diagnostic.Summary() != "Invalid Twenty Provider Configuration" || diagnostic.Detail() != wantDetail {
					t.Fatal("invalid email must produce the exact fixed attribute diagnostic")
				}
			})
		}
	}
}

func TestProviderConfigureSanitizedAuthFailure(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"errors":[{"message":"test-only-secret","extensions":{"subCode":"EMAIL_NOT_VERIFIED"}}]}`)
	}))
	defer server.Close()
	p := New("test")()
	response := &provider.ConfigureResponse{}
	p.Configure(t.Context(), provider.ConfigureRequest{Config: testConfig(t, p, map[string]any{"endpoint": server.URL, "email": "automation@example.invalid", "password": "test-only-secret", "allow_insecure_http": true})}, response)
	if !response.Diagnostics.HasError() || response.ResourceData != nil || response.DataSourceData != nil || strings.Contains(response.Diagnostics[0].Detail(), "test-only-secret") || !strings.Contains(response.Diagnostics[0].Detail(), "verified") {
		t.Fatal("authentication failures must return fixed redacted diagnostics without provider data")
	}
}

func TestResolveProviderConfig(t *testing.T) {
	t.Parallel()
	null := TwentyProviderModel{Endpoint: types.StringNull(), Email: types.StringNull(), Password: types.StringNull(), AllowInsecureHTTP: types.BoolNull()}
	env := map[string]string{"TWENTY_ENDPOINT": " https://env.example.invalid ", "TWENTY_EMAIL": " automation@example.invalid ", "TWENTY_PASSWORD": " env-test-password "}
	for name, test := range map[string]struct {
		model    TwentyProviderModel
		want     resolvedProviderConfig
		errors   int
		envReads int
	}{
		"env":            {null, resolvedProviderConfig{endpoint: "https://env.example.invalid", email: "automation@example.invalid", password: " env-test-password "}, 0, 3},
		"explicit":       {TwentyProviderModel{Endpoint: types.StringValue(" https://explicit.example.invalid "), Email: types.StringValue(" explicit@example.invalid "), Password: types.StringValue(" explicit-test-password "), AllowInsecureHTTP: types.BoolValue(true)}, resolvedProviderConfig{endpoint: "https://explicit.example.invalid", email: "explicit@example.invalid", password: " explicit-test-password ", allowHTTP: true}, 0, 0},
		"explicit empty": {TwentyProviderModel{Endpoint: types.StringValue(""), Email: types.StringValue(""), Password: types.StringValue("")}, resolvedProviderConfig{}, 3, 0},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			got, diags := resolveProviderConfig(test.model, func(key string) string { calls++; return env[key] })
			if got != test.want || len(diags) != test.errors || calls != test.envReads {
				t.Fatal("incorrect fallback, precedence, or credential normalization")
			}
		})
	}
	for _, name := range []string{"endpoint", "email", "password", "allow_insecure_http"} {
		t.Run("unknown "+name, func(t *testing.T) {
			t.Parallel()
			model := null
			switch name {
			case "endpoint":
				model.Endpoint = types.StringUnknown()
			case "email":
				model.Email = types.StringUnknown()
			case "password":
				model.Password = types.StringUnknown()
			case "allow_insecure_http":
				model.AllowInsecureHTTP = types.BoolUnknown()
			}
			_, diags := resolveProviderConfig(model, func(string) string { t.Error("unknown values must fail before any environment access"); return "" })
			if len(diags) != 1 || !diags.HasError() {
				t.Fatal("unknown value must produce one attribute diagnostic")
			}
		})
	}
}

func testConfig(t *testing.T, p provider.Provider, values map[string]any) tfsdk.Config {
	t.Helper()
	schemaResponse := &provider.SchemaResponse{}
	p.Schema(t.Context(), provider.SchemaRequest{}, schemaResponse)
	attributeTypes := map[string]tftypes.Type{"endpoint": tftypes.String, "email": tftypes.String, "password": tftypes.String, "allow_insecure_http": tftypes.Bool}
	objectType := tftypes.Object{AttributeTypes: attributeTypes}
	raw := tftypes.NewValue(objectType, nil)
	if values != nil {
		rawValues := make(map[string]tftypes.Value, len(attributeTypes))
		for name, typ := range attributeTypes {
			rawValues[name] = tftypes.NewValue(typ, values[name])
		}
		raw = tftypes.NewValue(objectType, rawValues)
	}
	return tfsdk.Config{Raw: raw, Schema: schemaResponse.Schema}
}

func providerAuthFixture(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	calls := new(int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		var req struct {
			OpName    string         `json:"operationName"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error("invalid auth fixture request")
			return
		}
		if r.URL.Path != "/metadata" || r.Method != http.MethodPost {
			t.Error("provider must only send Metadata POSTs")
		}
		expiry := time.Now().Add(time.Hour).Format(time.RFC3339Nano)
		var value any
		switch req.OpName {
		case "GetLoginTokenFromCredentials":
			if req.Variables["email"] != "automation@example.invalid" || req.Variables["password"] != " test-provider-password " {
				t.Error("configuration must trim email but preserve password exactly")
			}
			value = map[string]any{"loginToken": map[string]any{"token": "test-login", "expiresAt": expiry}}
		case "GetAuthTokensFromLoginToken":
			value = map[string]any{"tokens": map[string]any{"accessOrWorkspaceAgnosticToken": map[string]any{"token": "test-access", "expiresAt": expiry}, "refreshToken": map[string]any{"token": "test-refresh", "expiresAt": expiry}}}
		case "CurrentUser":
			member := map[string]any{"id": "00000000-0000-4000-8000-000000000203", "userId": "00000000-0000-4000-8000-000000000201", "userWorkspaceId": "00000000-0000-4000-8000-000000000204", "userEmail": "automation@example.invalid", "roles": []any{map[string]any{"id": "00000000-0000-4000-8000-000000000205"}}}
			value = map[string]any{"id": "00000000-0000-4000-8000-000000000201", "email": "automation@example.invalid", "isEmailVerified": true, "disabled": false, "hasPassword": true,
				"currentWorkspace":     map[string]any{"id": "00000000-0000-4000-8000-000000000202", "activationStatus": "ACTIVE"},
				"currentUserWorkspace": map[string]any{"id": "00000000-0000-4000-8000-000000000204", "userId": "00000000-0000-4000-8000-000000000201", "permissionFlags": []string{"ROLES"}}, "workspaceMember": member, "workspaceMembers": []any{member}}
		case "CurrentWorkspace":
			value = map[string]any{"id": "00000000-0000-4000-8000-000000000202", "activationStatus": "ACTIVE"}
		case "GetRoles":
			_ = json.NewEncoder(w).Encode(map[string]any{"errors": []any{map[string]any{"message": "private-password private-token", "extensions": map[string]any{"code": "FORBIDDEN"}}}})
			return
		default:
			t.Error("unexpected provider auth operation")
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{strings.ToLower(req.OpName[:1]) + req.OpName[1:]: value}}); err != nil {
			t.Error("encode provider auth fixture")
		}
	}))
	t.Cleanup(server.Close)
	return server, calls
}
