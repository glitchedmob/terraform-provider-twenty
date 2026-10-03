// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
)

const (
	workspaceTestUser       = "00000000-0000-4000-8000-000000000401"
	workspaceTestID         = "00000000-0000-4000-8000-000000000402"
	workspaceTestMember     = "00000000-0000-4000-8000-000000000403"
	workspaceTestMembership = "00000000-0000-4000-8000-000000000404"
)

func workspaceTestIdentity() client.Identity {
	return client.Identity{UserID: workspaceTestUser, WorkspaceID: workspaceTestID, WorkspaceMemberID: workspaceTestMember, UserWorkspaceID: workspaceTestMembership, Email: "automation@example.invalid"}
}

type workspaceMock struct {
	user, workspace map[string]any
	calls           []string
	fail            map[string]error
	raw             json.RawMessage
}

func newWorkspaceMock() *workspaceMock {
	workspace := map[string]any{
		"id": workspaceTestID, "displayName": "Disposable workspace", "activationStatus": "ACTIVE", "subdomain": "disposable",
		"customDomain": nil, "workspaceMembersCount": 2, "workspaceUrls": map[string]any{"subdomainUrl": "http://localhost:3000", "customUrl": nil},
		"defaultRole": roleTestFixture(roleTestOther, "Member"), "createdAt": "2026-01-01T00:00:00Z", "updatedAt": "2026-01-02T03:04:05.123Z",
	}
	user := map[string]any{
		"id": workspaceTestUser, "email": "automation@example.invalid", "isEmailVerified": true, "disabled": false, "hasPassword": true,
		"currentWorkspace":     map[string]any{"id": workspaceTestID, "activationStatus": "ACTIVE", "displayName": "Stale cached name", "workspaceMembersCount": 99},
		"currentUserWorkspace": map[string]any{"id": workspaceTestMembership, "userId": workspaceTestUser, "permissionFlags": []string{}},
		"workspaceMember":      map[string]any{"id": workspaceTestMember, "userId": workspaceTestUser, "userWorkspaceId": workspaceTestMembership, "userEmail": "automation@example.invalid", "roles": []any{roleTestFixture(roleTestOther, "Member")}},
	}
	user["workspaceMembers"] = []any{user["workspaceMember"]}
	return &workspaceMock{user: user, workspace: workspace, fail: map[string]error{}}
}

func (m *workspaceMock) MakeRequest(_ context.Context, req *graphql.Request, resp *graphql.Response) error {
	m.calls = append(m.calls, req.OpName)
	if err := m.fail[req.OpName]; err != nil {
		return err
	}
	if m.raw != nil {
		return json.Unmarshal(m.raw, resp.Data)
	}
	var data map[string]any
	switch req.OpName {
	case "CurrentUser":
		data = map[string]any{"currentUser": m.user}
	case "CurrentWorkspace":
		data = map[string]any{"currentWorkspace": m.workspace}
	default:
		return errors.New("unexpected workspace operation")
	}
	raw, _ := json.Marshal(data)
	return json.Unmarshal(raw, resp.Data)
}

func mockWorkspaceDataSource(m *workspaceMock) *workspaceDataSource {
	return &workspaceDataSource{client: m, identity: workspaceTestIdentity()}
}

func workspaceTestRead(t *testing.T, d *workspaceDataSource) datasource.ReadResponse {
	t.Helper()
	var schema datasource.SchemaResponse
	d.Schema(t.Context(), datasource.SchemaRequest{}, &schema)
	response := datasource.ReadResponse{State: tfsdk.State{Schema: schema.Schema}}
	// No input decoding or selector is needed for this all-computed schema.
	d.Read(t.Context(), datasource.ReadRequest{}, &response)
	return response
}

func workspaceTestState(t *testing.T, response datasource.ReadResponse) workspaceModel {
	t.Helper()
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	var state workspaceModel
	if diagnostics := response.State.Get(t.Context(), &state); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	return state
}

func TestWorkspaceDataSourceSchema(t *testing.T) {
	t.Parallel()
	d := NewWorkspaceDataSource()
	var metadata datasource.MetadataResponse
	d.Metadata(t.Context(), datasource.MetadataRequest{ProviderTypeName: "twenty"}, &metadata)
	if metadata.TypeName != "twenty_workspace" {
		t.Fatal("wrong workspace type name")
	}
	var response datasource.SchemaResponse
	d.Schema(t.Context(), datasource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() || len(response.Schema.Attributes) != 11 || len(response.Schema.Blocks) != 0 {
		t.Fatal("unexpected workspace schema")
	}
	for name, attribute := range response.Schema.Attributes {
		if !attribute.IsComputed() || attribute.IsOptional() || attribute.IsRequired() || attribute.GetMarkdownDescription() == "" {
			t.Errorf("%s must be documented and computed only", name)
		}
	}
}

func TestWorkspaceDataSourceConfigure(t *testing.T) {
	t.Parallel()
	session := &client.Session{}
	d := &workspaceDataSource{}
	var response datasource.ConfigureResponse
	d.Configure(t.Context(), datasource.ConfigureRequest{ProviderData: &ClientData{Client: session}}, &response)
	if response.Diagnostics.HasError() || d.client != session.Client() {
		t.Fatal("must reuse session without login")
	}
	d.identity = workspaceTestIdentity()
	d.Configure(t.Context(), datasource.ConfigureRequest{}, &response)
	if response.Diagnostics.HasError() || d.client != nil || d.identity.WorkspaceID != "" {
		t.Fatal("schema-only configure must clear client and identity")
	}
	for name, data := range map[string]any{"wrong type": "private-value", "typed nil": (*ClientData)(nil), "missing session": &ClientData{}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := &workspaceDataSource{}
			var response datasource.ConfigureResponse
			d.Configure(t.Context(), datasource.ConfigureRequest{ProviderData: data}, &response)
			if !response.Diagnostics.HasError() || d.client != nil {
				t.Fatal("invalid client must fail")
			}
			if strings.Contains(response.Diagnostics[0].Detail(), "private-value") {
				t.Fatal("provider data leaked")
			}
		})
	}
	read := workspaceTestRead(t, d)
	if !read.Diagnostics.HasError() || read.Diagnostics[0].Summary() != "Twenty Client Not Configured" || !read.State.Raw.IsNull() {
		t.Fatal("unconfigured read must fail without state")
	}
}

func TestWorkspaceDataSourceFreshRead(t *testing.T) {
	t.Parallel()
	m := newWorkspaceMock()
	d := mockWorkspaceDataSource(m)
	state := workspaceTestState(t, workspaceTestRead(t, d))
	if state.ID.ValueString() != workspaceTestID || state.DisplayName.ValueString() != "Disposable workspace" || state.DefaultRoleID.ValueString() != roleTestOther || state.ActivationStatus.ValueString() != "ACTIVE" || state.Subdomain.ValueString() != "disposable" || state.SubdomainURL.ValueString() != "http://localhost:3000" || state.WorkspaceMembersCount.ValueInt64() != 2 || !state.CustomDomain.IsNull() || !state.CustomURL.IsNull() || state.CreatedAt.ValueString() != "2026-01-01T00:00:00Z" || state.UpdatedAt.ValueString() != "2026-01-02T03:04:05.123Z" {
		t.Fatal("workspace properties must come from fresh CurrentWorkspace, not cached CurrentUser")
	}
	m.workspace["displayName"], m.workspace["workspaceMembersCount"], m.workspace["subdomain"], m.workspace["customDomain"] = "Changed name", 3, "changed", "workspace.example.invalid"
	m.workspace["workspaceUrls"] = map[string]any{"subdomainUrl": "https://changed.example.invalid", "customUrl": "https://workspace.example.invalid"}
	m.workspace["defaultRole"] = roleTestFixture(roleTestID, "Changed default")
	state = workspaceTestState(t, workspaceTestRead(t, d))
	if state.DisplayName.ValueString() != "Changed name" || state.WorkspaceMembersCount.ValueInt64() != 3 || state.DefaultRoleID.ValueString() != roleTestID || state.Subdomain.ValueString() != "changed" || state.SubdomainURL.ValueString() != "https://changed.example.invalid" || state.CustomDomain.ValueString() != "workspace.example.invalid" || state.CustomURL.ValueString() != "https://workspace.example.invalid" {
		t.Fatal("computed properties failed to refresh")
	}
	if strings.Join(m.calls, ",") != "CurrentUser,CurrentWorkspace,CurrentUser,CurrentWorkspace" {
		t.Fatal("each read must revalidate identity and load workspace without other queries")
	}
}

func TestWorkspaceDataSourceNullableValues(t *testing.T) {
	t.Parallel()
	m := newWorkspaceMock()
	m.workspace["displayName"], m.workspace["defaultRole"], m.workspace["workspaceMembersCount"] = nil, nil, nil
	state := workspaceTestState(t, workspaceTestRead(t, mockWorkspaceDataSource(m)))
	if !state.DisplayName.IsNull() || !state.DefaultRoleID.IsNull() || !state.WorkspaceMembersCount.IsNull() {
		t.Fatal("nullable values must remain null")
	}
	m.workspace["displayName"], m.workspace["customDomain"], m.workspace["workspaceMembersCount"] = "", "", 0
	state = workspaceTestState(t, workspaceTestRead(t, mockWorkspaceDataSource(m)))
	if state.DisplayName.IsNull() || state.CustomDomain.IsNull() || state.WorkspaceMembersCount.IsNull() || state.WorkspaceMembersCount.ValueInt64() != 0 {
		t.Fatal("explicit empty strings and zero must not become null")
	}
}

func TestWorkspaceDataSourceIdentityMismatch(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"user", "email", "verified", "disabled", "password", "cached workspace", "fresh workspace", "member", "member user", "member membership", "membership", "membership user", "invalid pin"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			m := newWorkspaceMock()
			d := mockWorkspaceDataSource(m)
			switch field {
			case "user":
				m.user["id"] = roleTestID
			case "email":
				m.user["email"] = "private-member@example.invalid"
			case "verified":
				m.user["isEmailVerified"] = false
			case "disabled":
				m.user["disabled"] = true
			case "password":
				m.user["hasPassword"] = false
			case "cached workspace":
				m.user["currentWorkspace"].(map[string]any)["id"] = roleTestID
			case "fresh workspace":
				m.workspace["id"] = roleTestID
			case "member":
				m.user["workspaceMember"].(map[string]any)["id"] = roleTestID
			case "member user":
				m.user["workspaceMember"].(map[string]any)["userId"] = roleTestID
			case "member membership":
				m.user["workspaceMember"].(map[string]any)["userWorkspaceId"] = roleTestID
			case "membership":
				m.user["currentUserWorkspace"].(map[string]any)["id"] = roleTestID
			case "membership user":
				m.user["currentUserWorkspace"].(map[string]any)["userId"] = roleTestID
			case "invalid pin":
				d.identity.WorkspaceID = ""
			}
			response := workspaceTestRead(t, d)
			if !response.Diagnostics.HasError() || !strings.Contains(response.Diagnostics[0].Detail(), "configured session identity") || !response.State.Raw.IsNull() {
				t.Fatal("identity mismatch must fail without state")
			}
			if strings.Contains(response.Diagnostics[0].Detail(), roleTestID) || strings.Contains(response.Diagnostics[0].Detail(), "private-member") {
				t.Fatal("identity diagnostic leaked response")
			}
		})
	}
}

func TestWorkspaceDataSourceMalformedWorkspace(t *testing.T) {
	t.Parallel()
	for field, invalid := range map[string][]any{
		"id": {nil, "", "not-a-uuid", "00000000-0000-0000-0000-000000000000"}, "displayName": {42}, "activationStatus": {nil, "", "FUTURE"}, "subdomain": {nil, "", " "}, "customDomain": {false},
		"workspaceUrls":         {nil, false, map[string]any{}, map[string]any{"subdomainUrl": nil, "customUrl": nil}, map[string]any{"subdomainUrl": "https://valid.invalid", "customUrl": "relative"}, map[string]any{"subdomainUrl": "https://user:secret@private.invalid", "customUrl": nil}},
		"defaultRole":           {false, map[string]any{}, map[string]any{"id": nil}, map[string]any{"id": "private-id"}, map[string]any{"id": "00000000-0000-0000-0000-000000000000"}},
		"workspaceMembersCount": {-1, 1.5, "2", 9007199254740992.0}, "createdAt": {nil, "", "bad-time", "0001-01-01T00:00:00Z"}, "updatedAt": {nil, false},
	} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			// Omission is an error even for nullable selected properties.
			m := newWorkspaceMock()
			delete(m.workspace, field)
			assertMalformedWorkspace(t, workspaceTestRead(t, mockWorkspaceDataSource(m)))
			for _, value := range invalid {
				m := newWorkspaceMock()
				m.workspace[field] = value
				assertMalformedWorkspace(t, workspaceTestRead(t, mockWorkspaceDataSource(m)))
			}
		})
	}
}

func TestWorkspaceDataSourceMalformedUser(t *testing.T) {
	t.Parallel()
	for field, value := range map[string]any{"id": "", "email": false, "isEmailVerified": nil, "disabled": nil, "hasPassword": nil, "currentWorkspace": nil, "workspaceMember": nil, "currentUserWorkspace": nil} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			m := newWorkspaceMock()
			m.user[field] = value
			assertMalformedWorkspace(t, workspaceTestRead(t, mockWorkspaceDataSource(m)))
			delete(m.user, field)
			assertMalformedWorkspace(t, workspaceTestRead(t, mockWorkspaceDataSource(m)))
		})
	}
	for _, field := range []string{"currentWorkspace", "workspaceMember", "currentUserWorkspace"} {
		m := newWorkspaceMock()
		m.user[field].(map[string]any)["id"] = nil
		assertMalformedWorkspace(t, workspaceTestRead(t, mockWorkspaceDataSource(m)))
	}
}

func assertMalformedWorkspace(t *testing.T, response datasource.ReadResponse) {
	t.Helper()
	if !response.Diagnostics.HasError() || !strings.Contains(response.Diagnostics[0].Detail(), errInvalidWorkspaceResponse.Error()) || !response.State.Raw.IsNull() {
		t.Fatal("malformed data must fail with no state")
	}
}

func TestWorkspaceDataSourceReadFailure(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"CurrentUser", "CurrentWorkspace"} {
		m := newWorkspaceMock()
		m.fail[operation] = errors.New("private-password private-token private-member")
		response := workspaceTestRead(t, mockWorkspaceDataSource(m))
		if !response.Diagnostics.HasError() || strings.Contains(response.Diagnostics[0].Detail(), "private-") || !response.State.Raw.IsNull() {
			t.Fatal("upstream errors must be redacted without state")
		}
	}
	for _, known := range []error{errInvalidWorkspaceResponse, errWorkspaceIdentityMismatch} {
		m := newWorkspaceMock()
		m.fail["CurrentUser"] = errors.Join(errors.New("private-token"), known)
		response := workspaceTestRead(t, mockWorkspaceDataSource(m))
		if !response.Diagnostics.HasError() || strings.Contains(response.Diagnostics[0].Detail(), "private-") || !strings.Contains(response.Diagnostics[0].Detail(), known.Error()) {
			t.Fatal("wrapped known errors must use fixed redacted diagnostics")
		}
	}
	for _, raw := range []string{`null`, `[]`, `{}`, `{"currentUser":null}`} {
		m := newWorkspaceMock()
		m.raw = json.RawMessage(raw)
		assertMalformedWorkspace(t, workspaceTestRead(t, mockWorkspaceDataSource(m)))
	}
	m := newWorkspaceMock()
	if err := (workspaceQueryClient{m}).MakeRequest(t.Context(), &graphql.Request{OpName: "GetRoles"}, &graphql.Response{Data: new(json.RawMessage)}); !errors.Is(err, errInvalidWorkspaceResponse) {
		t.Fatal("wrapper must reject unrelated operations")
	}
	for _, value := range []string{"relative", "ftp://workspace.invalid", "https://workspace.invalid?secret=1", "https://workspace.invalid#token", " https://workspace.invalid", "https://%"} {
		if validWorkspaceURL(value) {
			t.Fatal("invalid workspace URL accepted")
		}
	}
}

func TestWorkspaceDataSourceSessionDenial(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"FORBIDDEN", "UNAUTHENTICATED"} {
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			m := newWorkspaceMock()
			var deny atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/metadata" || r.Method != http.MethodPost {
					t.Error("workspace data source must only POST Metadata")
					return
				}
				var req graphql.Request
				if json.NewDecoder(r.Body).Decode(&req) != nil {
					t.Error("decode request")
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if deny.Load() {
					_ = json.NewEncoder(w).Encode(map[string]any{"errors": []any{map[string]any{"message": "private-password private-token", "extensions": map[string]any{"code": code}}}})
					return
				}
				expiry := time.Now().Add(time.Hour).Format(time.RFC3339Nano)
				var value any
				switch req.OpName {
				case "GetLoginTokenFromCredentials":
					value = map[string]any{"loginToken": map[string]any{"token": "test-login", "expiresAt": expiry}}
				case "GetAuthTokensFromLoginToken":
					value = map[string]any{"tokens": map[string]any{"accessOrWorkspaceAgnosticToken": map[string]any{"token": "test-access", "expiresAt": expiry}, "refreshToken": map[string]any{"token": "test-refresh", "expiresAt": expiry}}}
				case "CurrentUser":
					value = m.user
				case "CurrentWorkspace":
					value = m.workspace
				default:
					t.Error("unexpected workspace request")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{strings.ToLower(req.OpName[:1]) + req.OpName[1:]: value}})
			}))
			t.Cleanup(server.Close)
			session, err := client.NewSession(t.Context(), server.URL, "automation@example.invalid", "test-password", true)
			if err != nil {
				t.Fatal(err)
			}
			d := &workspaceDataSource{}
			var configured datasource.ConfigureResponse
			d.Configure(t.Context(), datasource.ConfigureRequest{ProviderData: &ClientData{Client: session}}, &configured)
			if configured.Diagnostics.HasError() || d.identity.WorkspaceID != workspaceTestID {
				t.Fatal("configure must pin session identity")
			}
			_ = workspaceTestState(t, workspaceTestRead(t, d))
			deny.Store(true)
			response := workspaceTestRead(t, d)
			if !response.Diagnostics.HasError() || strings.Contains(response.Diagnostics[0].Detail(), "private-") || !response.State.Raw.IsNull() {
				t.Fatal("session denial must be redacted")
			}
			want := "denied permission"
			if code == "UNAUTHENTICATED" {
				want = "session authentication was rejected"
			}
			if !strings.Contains(response.Diagnostics[0].Detail(), want) {
				t.Fatal("session error classification was lost")
			}
		})
	}
}
