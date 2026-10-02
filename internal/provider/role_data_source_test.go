// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

const (
	roleTestID    = "11111111-1111-4111-8111-111111111111"
	roleTestOther = "22222222-2222-4222-8222-222222222222"
	roleTestFlag  = "33333333-3333-4333-8333-333333333333"
)

func TestRoleDataSourceMetadataAndSchema(t *testing.T) {
	t.Parallel()
	d := NewRoleDataSource()
	var metadata datasource.MetadataResponse
	d.Metadata(t.Context(), datasource.MetadataRequest{ProviderTypeName: "twenty"}, &metadata)
	if metadata.TypeName != "twenty_role" {
		t.Fatalf("unexpected type name %q", metadata.TypeName)
	}
	var response datasource.SchemaResponse
	d.Schema(t.Context(), datasource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() || len(response.Schema.Attributes) != 16 {
		t.Fatal("expected the role schema and no diagnostics")
	}
	for name, attribute := range response.Schema.Attributes {
		if attribute.GetMarkdownDescription() == "" || attribute.IsRequired() {
			t.Errorf("unexpected schema for %s", name)
		}
		if name == "role_id" {
			if !attribute.IsOptional() || attribute.IsComputed() {
				t.Error("role_id must be an optional selector")
			}
		} else if !attribute.IsComputed() {
			t.Errorf("%s must be computed", name)
		}
	}
	if !response.Schema.Attributes["label"].IsOptional() {
		t.Error("label must also be an optional selector")
	}
}

func TestRoleDataSourceConfigure(t *testing.T) {
	t.Parallel()

	session := &client.Session{}
	d := &roleDataSource{}
	var configured datasource.ConfigureResponse
	d.Configure(t.Context(), datasource.ConfigureRequest{ProviderData: &ClientData{Client: session}}, &configured)
	if configured.Diagnostics.HasError() || d.client != session.Client() {
		t.Fatal("expected the shared session client without a login request")
	}
	d.Configure(t.Context(), datasource.ConfigureRequest{}, &configured)
	if configured.Diagnostics.HasError() || d.client != nil {
		t.Fatal("schema-only reconfiguration must discard a previous client")
	}
	for name, data := range map[string]any{
		"schema only":     nil,
		"wrong type":      "private-value-must-not-appear",
		"typed nil":       (*ClientData)(nil),
		"missing session": &ClientData{},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := &roleDataSource{}
			var response datasource.ConfigureResponse
			d.Configure(t.Context(), datasource.ConfigureRequest{ProviderData: data}, &response)
			if response.Diagnostics.HasError() != (data != nil) || d.client != nil {
				t.Fatal("unexpected configure result")
			}
			for _, diagnostic := range response.Diagnostics {
				if strings.Contains(diagnostic.Detail(), "private-value") {
					t.Fatal("configure diagnostic leaked provider data")
				}
			}
		})
	}
}

func TestRoleDataSourceValidateConfig(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		id, label types.String
		wantError bool
	}{
		"UUID":                   {id: types.StringValue(roleTestID), label: types.StringNull()},
		"label":                  {id: types.StringNull(), label: types.StringValue(" Admin ")},
		"unknown UUID deferred":  {id: types.StringUnknown(), label: types.StringNull()},
		"unknown label deferred": {id: types.StringNull(), label: types.StringUnknown()},
		"neither":                {id: types.StringNull(), label: types.StringNull(), wantError: true},
		"both":                   {id: types.StringValue(roleTestID), label: types.StringValue("Admin"), wantError: true},
		"both unknown":           {id: types.StringUnknown(), label: types.StringUnknown(), wantError: true},
		"malformed UUID":         {id: types.StringValue("not-a-uuid"), label: types.StringNull(), wantError: true},
		"compact UUID":           {id: types.StringValue("11111111111141118111111111111111"), label: types.StringNull(), wantError: true},
		"padded UUID":            {id: types.StringValue(" " + roleTestID), label: types.StringNull(), wantError: true},
		"blank label":            {id: types.StringNull(), label: types.StringValue("\t \n"), wantError: true},
		"empty label":            {id: types.StringNull(), label: types.StringValue(""), wantError: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := &roleDataSource{}
			config := roleTestConfig(t, d, test.id, test.label)
			var response datasource.ValidateConfigResponse
			d.ValidateConfig(t.Context(), datasource.ValidateConfigRequest{Config: config}, &response)
			if response.Diagnostics.HasError() != test.wantError {
				t.Fatalf("unexpected validation result: %v", response.Diagnostics)
			}
		})
	}
}

func TestRoleDataSourceRead(t *testing.T) {
	t.Parallel()
	for _, byID := range []bool{false, true} {
		t.Run(map[bool]string{false: "exact label", true: "UUID"}[byID], func(t *testing.T) {
			t.Parallel()
			role := roleTestFixture(roleTestID, " Admin ")
			role["description"] = " description with spaces "
			role["icon"] = ""
			role["canBeAssignedToUsers"] = true
			role["canReadAllObjectRecords"] = true
			role["permissionFlags"] = []any{roleTestPermissionFlag(roleTestFlag, roleTestID, "ROLES")}
			d := roleTestDataSource(t, map[string]any{"data": map[string]any{"getRoles": []any{role}}})
			id, label := types.StringNull(), types.StringValue(" Admin ")
			if byID {
				id, label = types.StringValue(roleTestID), types.StringNull()
			}
			response := roleTestRead(t, d, id, label)
			if response.Diagnostics.HasError() {
				t.Fatal(response.Diagnostics)
			}
			var state roleModel
			if diagnostics := response.State.Get(t.Context(), &state); diagnostics.HasError() {
				t.Fatal(diagnostics)
			}
			if state.ID.ValueString() != roleTestID || state.Label.ValueString() != " Admin " || !state.RoleID.Equal(id) {
				t.Fatal("role identity or exact label was changed")
			}
			if state.Description.ValueString() != " description with spaces " || state.Icon.IsNull() || state.Icon.ValueString() != "" {
				t.Fatal("nullable strings were not preserved")
			}
			if !state.CanBeAssignedToUsers.ValueBool() || !state.CanReadAllObjectRecords.ValueBool() || state.CanUpdateAllSettings.IsNull() || state.CanUpdateAllSettings.ValueBool() {
				t.Fatal("required booleans lost true or explicit false")
			}
			var flags []string
			if diagnostics := state.PermissionFlags.ElementsAs(t.Context(), &flags, false); diagnostics.HasError() || len(flags) != 1 || flags[0] != "ROLES" {
				t.Fatal("permission flags were not mapped")
			}
		})
	}
}

func TestRoleDataSourceReadNullableValues(t *testing.T) {
	t.Parallel()
	for _, nullFlags := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty flags", true: "null flags"}[nullFlags], func(t *testing.T) {
			t.Parallel()
			role := roleTestFixture(roleTestID, "Admin")
			if nullFlags {
				role["permissionFlags"] = nil
			}
			d := roleTestDataSource(t, map[string]any{"data": map[string]any{"getRoles": []any{role}}})
			response := roleTestRead(t, d, types.StringValue(roleTestID), types.StringNull())
			if response.Diagnostics.HasError() {
				t.Fatal(response.Diagnostics)
			}
			var state roleModel
			if diagnostics := response.State.Get(t.Context(), &state); diagnostics.HasError() {
				t.Fatal(diagnostics)
			}
			if !state.Description.IsNull() || !state.Icon.IsNull() || state.PermissionFlags.IsNull() != nullFlags || len(state.PermissionFlags.Elements()) != 0 {
				t.Fatal("null and empty values were conflated")
			}
		})
	}
}

func TestRoleDataSourceReadLookupErrors(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		roles     []any
		id, label types.String
		summary   string
	}{
		"missing":                {roles: []any{}, id: types.StringNull(), label: types.StringValue("private-role-label"), summary: "Twenty Role Not Found"},
		"no case normalization":  {roles: []any{roleTestFixture(roleTestID, "Admin")}, id: types.StringNull(), label: types.StringValue("admin"), summary: "Twenty Role Not Found"},
		"no whitespace trimming": {roles: []any{roleTestFixture(roleTestID, "Admin")}, id: types.StringNull(), label: types.StringValue(" Admin "), summary: "Twenty Role Not Found"},
		"duplicate label":        {roles: []any{roleTestFixture(roleTestID, "Admin"), roleTestFixture(roleTestOther, "Admin")}, id: types.StringNull(), label: types.StringValue("Admin"), summary: "Ambiguous Twenty Role"},
		"duplicate ID":           {roles: []any{roleTestFixture(roleTestID, "Admin"), roleTestFixture(roleTestID, "Other")}, id: types.StringValue(roleTestID), label: types.StringNull(), summary: "Ambiguous Twenty Role"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := roleTestDataSource(t, map[string]any{"data": map[string]any{"getRoles": test.roles}})
			response := roleTestRead(t, d, test.id, test.label)
			if !response.Diagnostics.HasError() || response.Diagnostics[0].Summary() != test.summary || !response.State.Raw.IsNull() {
				t.Fatal("lookup failure must return a diagnostic and no state")
			}
			if strings.Contains(response.Diagnostics[0].Detail(), "private-role-label") {
				t.Fatal("diagnostic leaked the selector")
			}
		})
	}
}

func TestRoleDataSourceReadFailures(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]any{
		"denied permission":    map[string]any{"errors": []any{map[string]any{"message": "ROLES denied private-password private-token private-role-label"}}, "data": nil},
		"partial GraphQL data": map[string]any{"errors": []any{map[string]any{"message": "private-password"}}, "data": map[string]any{"getRoles": []any{roleTestFixture(roleTestID, "Admin")}}},
		"missing data":         map[string]any{},
		"null role list":       map[string]any{"data": map[string]any{"getRoles": nil}},
		"null role":            map[string]any{"data": map[string]any{"getRoles": []any{nil}}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := roleTestDataSource(t, body)
			response := roleTestRead(t, d, types.StringValue(roleTestID), types.StringNull())
			if !response.Diagnostics.HasError() || !response.State.Raw.IsNull() {
				t.Fatal("request failure must not set state")
			}
			if strings.Contains(response.Diagnostics[0].Detail(), "private-") {
				t.Fatal("diagnostic leaked server response details")
			}
			if (name == "null role list" || name == "null role") && !strings.Contains(response.Diagnostics[0].Detail(), "malformed or incomplete role data") {
				t.Fatal("malformed role data must retain its safe classification")
			}
		})
	}
}

func TestRoleDataSourcePreservesSafePermissionDiagnostic(t *testing.T) {
	t.Parallel()
	server, calls := providerAuthFixture(t)
	session, err := client.NewSession(t.Context(), server.URL, "automation@example.invalid", " test-provider-password ", true)
	if err != nil {
		t.Fatal("authenticate synthetic role session")
	}
	d := &roleDataSource{}
	var configured datasource.ConfigureResponse
	d.Configure(t.Context(), datasource.ConfigureRequest{ProviderData: &ClientData{Client: session}}, &configured)
	response := roleTestRead(t, d, types.StringValue(roleTestID), types.StringNull())
	if !response.Diagnostics.HasError() || *calls != 5 || !response.State.Raw.IsNull() {
		t.Fatal("permission denial must fail once without setting state")
	}
	detail := response.Diagnostics[0].Detail()
	if !strings.Contains(detail, "denied permission") || !strings.Contains(detail, "ROLES") || strings.Contains(detail, "private-") {
		t.Fatal("permission diagnostic must preserve the safe classification and required permission")
	}
}

func TestRoleDataSourceReadInvalidConfig(t *testing.T) {
	t.Parallel()
	d := &roleDataSource{}
	for _, selectors := range [][2]types.String{
		{types.StringNull(), types.StringNull()},
		{types.StringUnknown(), types.StringNull()},
		{types.StringNull(), types.StringUnknown()},
		{types.StringValue(roleTestID), types.StringNull()},
	} {
		response := roleTestRead(t, d, selectors[0], selectors[1])
		if !response.Diagnostics.HasError() {
			t.Fatal("invalid configuration or unconfigured client must fail")
		}
	}
	config := roleTestConfig(t, d, types.StringValue(roleTestID), types.StringNull())
	config.Raw = tftypes.NewValue(tftypes.String, "not-an-object")
	var validation datasource.ValidateConfigResponse
	d.ValidateConfig(t.Context(), datasource.ValidateConfigRequest{Config: config}, &validation)
	var read datasource.ReadResponse
	d.Read(t.Context(), datasource.ReadRequest{Config: config}, &read)
	if !validation.Diagnostics.HasError() || !read.Diagnostics.HasError() {
		t.Fatal("invalid config decoding must fail")
	}
}

func roleTestConfig(t *testing.T, d datasource.DataSource, id, label types.String) tfsdk.Config {
	t.Helper()
	var schema datasource.SchemaResponse
	d.Schema(t.Context(), datasource.SchemaRequest{}, &schema)
	state := tfsdk.State{Schema: schema.Schema}
	model := roleModel{RoleID: id, Label: label, PermissionFlags: types.SetNull(types.StringType)}
	if diagnostics := state.Set(t.Context(), &model); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	return tfsdk.Config{Schema: schema.Schema, Raw: state.Raw}
}

func roleTestRead(t *testing.T, d *roleDataSource, id, label types.String) datasource.ReadResponse {
	t.Helper()
	config := roleTestConfig(t, d, id, label)
	response := datasource.ReadResponse{State: tfsdk.State{Schema: config.Schema}}
	d.Read(t.Context(), datasource.ReadRequest{Config: config}, &response)
	return response
}

func roleTestDataSource(t *testing.T, body any) *roleDataSource {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metadata" || r.Method != http.MethodPost {
			t.Error("role lookup must only POST to /metadata")
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var request graphql.Request
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode GraphQL request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if request.OpName != "GetRoles" {
			t.Errorf("unexpected GraphQL operation %q", request.OpName)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Errorf("encode fixture: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	return &roleDataSource{client: graphql.NewClient(server.URL+"/metadata", server.Client())}
}

func roleTestFixture(id, label string) map[string]any {
	return map[string]any{
		"id": id, "label": label, "universalIdentifier": nil, "description": nil, "icon": nil,
		"isEditable": false, "canBeAssignedToUsers": false, "canBeAssignedToAgents": false, "canBeAssignedToApiKeys": false,
		"canUpdateAllSettings": false, "canAccessAllTools": false, "canReadAllObjectRecords": false, "canUpdateAllObjectRecords": false,
		"canSoftDeleteAllObjectRecords": false, "canDestroyAllObjectRecords": false,
		"permissionFlags": []any{}, "objectPermissions": []any{}, "fieldPermissions": []any{},
		"rowLevelPermissionPredicates": []any{}, "rowLevelPermissionPredicateGroups": []any{},
		"workspaceMembers": []any{}, "agents": []any{}, "apiKeys": []any{},
	}
}

func roleTestPermissionFlag(id, owner, key string) map[string]any {
	return map[string]any{"id": id, "roleId": owner, "flag": key}
}
