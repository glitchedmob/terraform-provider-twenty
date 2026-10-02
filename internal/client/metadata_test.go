// SPDX-License-Identifier: MPL-2.0

package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
	"github.com/oapi-codegen/nullable"
)

const (
	roleID      = "00000000-0000-4000-8000-000000000001"
	objectID    = "00000000-0000-4000-8000-000000000002"
	fieldID     = "00000000-0000-4000-8000-000000000003"
	workspaceID = "00000000-0000-4000-8000-000000000100"
	userID      = "00000000-0000-4000-8000-000000000200"
	memberID    = "00000000-0000-4000-8000-000000000300"
)

// All fixtures are synthetic. This client only marshals variables and decodes
// response JSON, including the generated fragment unmarshallers.
type memoryClient struct {
	t         *testing.T
	operation string
	response  string
	err       error
	variables []byte
	ctx       context.Context
	calls     int
}

var _ graphql.Client = (*memoryClient)(nil)

func newMemoryClient(t *testing.T, operation, data string) *memoryClient {
	t.Helper()
	c := &memoryClient{t: t, operation: operation, response: `{"data":` + data + `}`}
	t.Cleanup(func() {
		if c.calls != 1 {
			t.Errorf("MakeRequest calls = %d, want 1", c.calls)
		}
	})
	return c
}

func (c *memoryClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	c.t.Helper()
	c.calls++
	c.ctx = ctx
	if req.OpName != c.operation || !strings.Contains(req.Query, c.operation) {
		c.t.Fatalf("unexpected operation: %q, query %q", req.OpName, req.Query)
	}
	var err error
	c.variables, err = json.Marshal(req.Variables)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(c.response), resp); err != nil {
		return err
	}
	if c.err != nil {
		return c.err
	}
	if len(resp.Errors) != 0 {
		return resp.Errors
	}
	return nil
}

func assertJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("decode actual JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("decode expected JSON: %v", err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("JSON = %s, want %s", got, want)
	}
}

func assertVariables(t *testing.T, c *memoryClient, want any) {
	t.Helper()
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal expected variables: %v", err)
	}
	assertJSON(t, c.variables, string(encoded))
}

func nullableValue[T any](t *testing.T, value nullable.Nullable[T]) T {
	t.Helper()
	got, err := value.Get()
	if err != nil {
		t.Fatalf("expected specified non-null value: %v", err)
	}
	return got
}

func assertNull[T any](t *testing.T, value nullable.Nullable[T]) {
	t.Helper()
	if !value.IsSpecified() || !value.IsNull() {
		t.Fatalf("expected explicit null, got %#v", value)
	}
	if _, err := value.Get(); err == nil {
		t.Fatal("Get must reject explicit null")
	}
}

type optionalCase[T any] struct {
	name    string
	value   nullable.Nullable[T]
	wire    any
	present bool
}

func boolCases() []optionalCase[bool] {
	return []optionalCase[bool]{
		{name: "omitted"},
		{name: "false", value: nullable.NewNullableWithValue(false), wire: false, present: true},
		{name: "true", value: nullable.NewNullableWithValue(true), wire: true, present: true},
		{name: "null", value: nullable.NewNullNullable[bool](), present: true},
	}
}

func stringCases(value string) []optionalCase[string] {
	return []optionalCase[string]{
		{name: "omitted"},
		{name: "empty", value: nullable.NewNullableWithValue(""), wire: "", present: true},
		{name: "value", value: nullable.NewNullableWithValue(value), wire: value, present: true},
		{name: "null", value: nullable.NewNullNullable[string](), present: true},
	}
}

func addOptional[T any](want map[string]any, tc optionalCase[T], keys ...string) {
	if tc.present {
		for _, key := range keys {
			want[key] = tc.wire
		}
	}
}

const rolePropertiesJSON = `{
	"id":"00000000-0000-4000-8000-000000000001",
	"universalIdentifier":"00000000-0000-4000-8000-000000000010",
	"label":"Review role", "description":"", "icon":null,
	"isEditable":true,
	"canBeAssignedToUsers":true, "canBeAssignedToAgents":false, "canBeAssignedToApiKeys":true,
	"canUpdateAllSettings":false, "canAccessAllTools":true,
	"canReadAllObjectRecords":false, "canUpdateAllObjectRecords":true,
	"canSoftDeleteAllObjectRecords":false, "canDestroyAllObjectRecords":true
}`

func assertRoleProperties(t *testing.T, role client.RoleProperties) {
	t.Helper()
	if role.Id != roleID || role.Label != "Review role" || !role.IsEditable {
		t.Fatalf("role identity did not decode: %#v", role)
	}
	if nullableValue(t, role.UniversalIdentifier) != "00000000-0000-4000-8000-000000000010" || nullableValue(t, role.Description) != "" {
		t.Fatal("nullable role strings did not decode")
	}
	assertNull(t, role.Icon)
	if !role.CanBeAssignedToUsers || role.CanBeAssignedToAgents || !role.CanBeAssignedToApiKeys ||
		role.CanUpdateAllSettings || !role.CanAccessAllTools || role.CanReadAllObjectRecords ||
		!role.CanUpdateAllObjectRecords || role.CanSoftDeleteAllObjectRecords || !role.CanDestroyAllObjectRecords {
		t.Fatalf("role permission booleans did not decode: %#v", role)
	}
}

func TestRoleOptionalBooleans(t *testing.T) {
	t.Parallel()
	keys := []string{
		"canUpdateAllSettings", "canAccessAllTools", "canReadAllObjectRecords",
		"canUpdateAllObjectRecords", "canSoftDeleteAllObjectRecords", "canDestroyAllObjectRecords",
		"canBeAssignedToUsers", "canBeAssignedToAgents", "canBeAssignedToApiKeys",
	}
	for _, tc := range boolCases() {
		t.Run("create/"+tc.name, func(t *testing.T) {
			c := newMemoryClient(t, "CreateOneRole", `{"createOneRole":`+rolePropertiesJSON+`}`)
			response, err := client.CreateOneRole(t.Context(), c, client.CreateRoleInput{
				Label:                "Review role",
				CanUpdateAllSettings: tc.value, CanAccessAllTools: tc.value,
				CanReadAllObjectRecords: tc.value, CanUpdateAllObjectRecords: tc.value,
				CanSoftDeleteAllObjectRecords: tc.value, CanDestroyAllObjectRecords: tc.value,
				CanBeAssignedToUsers: tc.value, CanBeAssignedToAgents: tc.value, CanBeAssignedToApiKeys: tc.value,
			})
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"label": "Review role"}
			addOptional(want, tc, keys...)
			assertVariables(t, c, map[string]any{"input": want})
			assertRoleProperties(t, response.CreateOneRole)
		})
		t.Run("update/"+tc.name, func(t *testing.T) {
			c := newMemoryClient(t, "UpdateOneRole", `{"updateOneRole":`+rolePropertiesJSON+`}`)
			response, err := client.UpdateOneRole(t.Context(), c, client.UpdateRoleInput{
				Id: roleID,
				Update: client.UpdateRolePayload{
					CanUpdateAllSettings: tc.value, CanAccessAllTools: tc.value,
					CanReadAllObjectRecords: tc.value, CanUpdateAllObjectRecords: tc.value,
					CanSoftDeleteAllObjectRecords: tc.value, CanDestroyAllObjectRecords: tc.value,
					CanBeAssignedToUsers: tc.value, CanBeAssignedToAgents: tc.value, CanBeAssignedToApiKeys: tc.value,
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]any{}
			addOptional(want, tc, keys...)
			assertVariables(t, c, map[string]any{"input": map[string]any{"id": roleID, "update": want}})
			assertRoleProperties(t, response.UpdateOneRole)
		})
	}
}

func TestRoleOptionalStrings(t *testing.T) {
	t.Parallel()
	for _, tc := range stringCases(roleID) {
		t.Run("create/"+tc.name, func(t *testing.T) {
			c := newMemoryClient(t, "CreateOneRole", `{"createOneRole":`+rolePropertiesJSON+`}`)
			_, err := client.CreateOneRole(t.Context(), c, client.CreateRoleInput{
				Label: "Review role", Id: tc.value, Description: tc.value, Icon: tc.value,
			})
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"label": "Review role"}
			addOptional(want, tc, "id", "description", "icon")
			assertVariables(t, c, map[string]any{"input": want})
		})
		t.Run("update/"+tc.name, func(t *testing.T) {
			c := newMemoryClient(t, "UpdateOneRole", `{"updateOneRole":`+rolePropertiesJSON+`}`)
			_, err := client.UpdateOneRole(t.Context(), c, client.UpdateRoleInput{
				Id: roleID, Update: client.UpdateRolePayload{Label: tc.value, Description: tc.value, Icon: tc.value},
			})
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]any{}
			addOptional(want, tc, "label", "description", "icon")
			assertVariables(t, c, map[string]any{"input": map[string]any{"id": roleID, "update": want}})
		})
	}
}

func TestDeleteRoleUUID(t *testing.T) {
	t.Parallel()
	c := newMemoryClient(t, "DeleteOneRole", `{"deleteOneRole":"`+roleID+`"}`)
	response, err := client.DeleteOneRole(t.Context(), c, roleID)
	if err != nil {
		t.Fatal(err)
	}
	assertVariables(t, c, map[string]any{"roleId": roleID})
	if response.DeleteOneRole != roleID {
		t.Fatalf("deleted role UUID = %q, want %q", response.DeleteOneRole, roleID)
	}
}

const objectPermissionJSON = `{
	"objectMetadataId":"00000000-0000-4000-8000-000000000002",
	"canReadObjectRecords":false, "canUpdateObjectRecords":true,
	"canSoftDeleteObjectRecords":null, "canDestroyObjectRecords":false,
	"restrictedFields":{"hidden":[false,null,""],"count":0},
	"rowLevelPermissionPredicates":[{
		"id":"00000000-0000-4000-8000-000000000020",
		"fieldMetadataId":"00000000-0000-4000-8000-000000000003",
		"objectMetadataId":"00000000-0000-4000-8000-000000000002",
		"operand":"IS", "subFieldName":"", "workspaceMemberFieldMetadataId":null,
		"workspaceMemberSubFieldName":null, "rowLevelPermissionPredicateGroupId":null,
		"positionInRowLevelPermissionPredicateGroup":0,
		"roleId":"00000000-0000-4000-8000-000000000001", "value":false
	}],
	"rowLevelPermissionPredicateGroups":[{
		"id":"00000000-0000-4000-8000-000000000021",
		"parentRowLevelPermissionPredicateGroupId":null, "logicalOperator":"AND",
		"positionInRowLevelPermissionPredicateGroup":0,
		"roleId":"00000000-0000-4000-8000-000000000001",
		"objectMetadataId":"00000000-0000-4000-8000-000000000002"
	}]
}`

const fieldPermissionJSON = `{
	"id":"00000000-0000-4000-8000-000000000030",
	"objectMetadataId":"00000000-0000-4000-8000-000000000002",
	"fieldMetadataId":"00000000-0000-4000-8000-000000000003",
	"roleId":"00000000-0000-4000-8000-000000000001",
	"canReadFieldValue":false, "canUpdateFieldValue":null
}`

func assertObjectPermission(t *testing.T, permission client.ObjectPermissionDetails) {
	t.Helper()
	if permission.ObjectMetadataId != objectID || nullableValue(t, permission.CanReadObjectRecords) ||
		!nullableValue(t, permission.CanUpdateObjectRecords) || nullableValue(t, permission.CanDestroyObjectRecords) {
		t.Fatalf("object permission did not decode: %#v", permission)
	}
	assertNull(t, permission.CanSoftDeleteObjectRecords)
	assertJSON(t, nullableValue(t, permission.RestrictedFields), `{"hidden":[false,null,""],"count":0}`)
	if len(permission.RowLevelPermissionPredicates) != 1 || len(permission.RowLevelPermissionPredicateGroups) != 1 {
		t.Fatal("object permission predicates or groups missing")
	}
	predicate := permission.RowLevelPermissionPredicates[0]
	if predicate.FieldMetadataId != fieldID || predicate.ObjectMetadataId != objectID || predicate.RoleId != roleID ||
		predicate.Operand != client.RowLevelPermissionPredicateOperandIs || nullableValue(t, predicate.SubFieldName) != "" ||
		nullableValue(t, predicate.PositionInRowLevelPermissionPredicateGroup) != 0 {
		t.Fatalf("predicate did not decode: %#v", predicate)
	}
	assertNull(t, predicate.WorkspaceMemberFieldMetadataId)
	assertNull(t, predicate.RowLevelPermissionPredicateGroupId)
	assertJSON(t, nullableValue(t, predicate.Value), `false`)
	group := permission.RowLevelPermissionPredicateGroups[0]
	if group.ObjectMetadataId != objectID || group.RoleId != roleID || group.LogicalOperator != client.RowLevelPermissionPredicateGroupLogicalOperatorAnd {
		t.Fatalf("predicate group did not decode: %#v", group)
	}
	assertNull(t, group.ParentRowLevelPermissionPredicateGroupId)
}

func assertFieldPermission(t *testing.T, permission client.FieldPermissionDetails) {
	t.Helper()
	if permission.Id != "00000000-0000-4000-8000-000000000030" || permission.RoleId != roleID ||
		permission.ObjectMetadataId != objectID || permission.FieldMetadataId != fieldID || nullableValue(t, permission.CanReadFieldValue) {
		t.Fatalf("field permission did not decode: %#v", permission)
	}
	assertNull(t, permission.CanUpdateFieldValue)
}

func TestPermissionOptionalBooleans(t *testing.T) {
	t.Parallel()
	for _, tc := range boolCases() {
		t.Run("object/"+tc.name, func(t *testing.T) {
			c := newMemoryClient(t, "UpsertObjectPermissions", `{"upsertObjectPermissions":[`+objectPermissionJSON+`]}`)
			response, err := client.UpsertObjectPermissions(t.Context(), c, client.UpsertObjectPermissionsInput{
				RoleId: roleID,
				ObjectPermissions: []client.ObjectPermissionInput{{
					ObjectMetadataId:     objectID,
					CanReadObjectRecords: tc.value, CanUpdateObjectRecords: tc.value,
					CanSoftDeleteObjectRecords: tc.value, CanDestroyObjectRecords: tc.value,
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			permission := map[string]any{"objectMetadataId": objectID}
			addOptional(permission, tc, "canReadObjectRecords", "canUpdateObjectRecords", "canSoftDeleteObjectRecords", "canDestroyObjectRecords")
			assertVariables(t, c, map[string]any{"input": map[string]any{"roleId": roleID, "objectPermissions": []any{permission}}})
			if len(response.UpsertObjectPermissions) != 1 {
				t.Fatal("object permission response missing")
			}
			assertObjectPermission(t, response.UpsertObjectPermissions[0])
		})
		t.Run("field/"+tc.name, func(t *testing.T) {
			c := newMemoryClient(t, "UpsertFieldPermissions", `{"upsertFieldPermissions":[`+fieldPermissionJSON+`]}`)
			response, err := client.UpsertFieldPermissions(t.Context(), c, client.UpsertFieldPermissionsInput{
				RoleId: roleID,
				FieldPermissions: []client.FieldPermissionInput{{
					ObjectMetadataId: objectID, FieldMetadataId: fieldID,
					CanReadFieldValue: tc.value, CanUpdateFieldValue: tc.value,
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			permission := map[string]any{"objectMetadataId": objectID, "fieldMetadataId": fieldID}
			addOptional(permission, tc, "canReadFieldValue", "canUpdateFieldValue")
			assertVariables(t, c, map[string]any{"input": map[string]any{"roleId": roleID, "fieldPermissions": []any{permission}}})
			if len(response.UpsertFieldPermissions) != 1 {
				t.Fatal("field permission response missing")
			}
			assertFieldPermission(t, response.UpsertFieldPermissions[0])
		})
	}
}

func TestRequiredEmptyLists(t *testing.T) {
	t.Parallel()
	// Allocate empty slices: generated inputs do not convert nil slices to [].
	t.Run("flags", func(t *testing.T) {
		c := newMemoryClient(t, "UpsertPermissionFlags", `{"upsertPermissionFlags":[]}`)
		response, err := client.UpsertPermissionFlags(t.Context(), c, client.UpsertPermissionFlagsInput{
			RoleId: roleID, PermissionFlagKeys: []string{},
		})
		if err != nil {
			t.Fatal(err)
		}
		assertVariables(t, c, map[string]any{"input": map[string]any{"roleId": roleID, "permissionFlagKeys": []any{}}})
		if response.UpsertPermissionFlags == nil || len(response.UpsertPermissionFlags) != 0 {
			t.Fatal("empty flag result must decode as a non-nil empty slice")
		}
	})
	t.Run("objects", func(t *testing.T) {
		c := newMemoryClient(t, "UpsertObjectPermissions", `{"upsertObjectPermissions":[]}`)
		_, err := client.UpsertObjectPermissions(t.Context(), c, client.UpsertObjectPermissionsInput{
			RoleId: roleID, ObjectPermissions: []client.ObjectPermissionInput{},
		})
		if err != nil {
			t.Fatal(err)
		}
		assertVariables(t, c, map[string]any{"input": map[string]any{"roleId": roleID, "objectPermissions": []any{}}})
	})
	t.Run("fields", func(t *testing.T) {
		c := newMemoryClient(t, "UpsertFieldPermissions", `{"upsertFieldPermissions":[]}`)
		_, err := client.UpsertFieldPermissions(t.Context(), c, client.UpsertFieldPermissionsInput{
			RoleId: roleID, FieldPermissions: []client.FieldPermissionInput{},
		})
		if err != nil {
			t.Fatal(err)
		}
		assertVariables(t, c, map[string]any{"input": map[string]any{"roleId": roleID, "fieldPermissions": []any{}}})
	})
	t.Run("emails", func(t *testing.T) {
		c := newMemoryClient(t, "SendInvitations", `{"sendInvitations":{"success":true,"errors":[],"result":[]}}`)
		_, err := client.SendInvitations(t.Context(), c, []string{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		assertVariables(t, c, map[string]any{"emails": []any{}})
	})
}

func TestPermissionFlagKeys(t *testing.T) {
	t.Parallel()
	c := newMemoryClient(t, "UpsertPermissionFlags", `{"upsertPermissionFlags":[
		{"id":"00000000-0000-4000-8000-000000000040","roleId":"`+roleID+`","flag":"ROLES"},
		{"id":"00000000-0000-4000-8000-000000000041","roleId":"`+roleID+`","flag":"WORKSPACE_MEMBERS"}
	]}`)
	response, err := client.UpsertPermissionFlags(t.Context(), c, client.UpsertPermissionFlagsInput{
		RoleId: roleID, PermissionFlagKeys: []string{"ROLES", "WORKSPACE_MEMBERS"},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertVariables(t, c, map[string]any{"input": map[string]any{"roleId": roleID, "permissionFlagKeys": []string{"ROLES", "WORKSPACE_MEMBERS"}}})
	if len(response.UpsertPermissionFlags) != 2 || response.UpsertPermissionFlags[0].Flag != "ROLES" ||
		response.UpsertPermissionFlags[1].Flag != "WORKSPACE_MEMBERS" || response.UpsertPermissionFlags[1].RoleId != roleID {
		t.Fatalf("permission flags did not decode: %#v", response.UpsertPermissionFlags)
	}
}

func assertToken(t *testing.T, token client.Token, wantToken, wantExpiry string) {
	t.Helper()
	want, err := time.Parse(time.RFC3339Nano, wantExpiry)
	if err != nil {
		t.Fatal(err)
	}
	if token.Token != wantToken || !token.ExpiresAt.Equal(want) {
		t.Fatalf("token = %#v, want %q expiring %s", token, wantToken, wantExpiry)
	}
}

func TestLoginOptionalVariablesAndExpiry(t *testing.T) {
	t.Parallel()
	for _, tc := range stringCases("synthetic-option") {
		t.Run(tc.name, func(t *testing.T) {
			c := newMemoryClient(t, "GetLoginTokenFromCredentials", `{"getLoginTokenFromCredentials":{"loginToken":{
				"token":"synthetic-login-token","expiresAt":"2030-01-02T03:04:05.123456Z"
			}}}`)
			response, err := client.GetLoginTokenFromCredentials(t.Context(), c,
				"synthetic@example.invalid", "not-a-password", "https://twenty.example.invalid", tc.value, tc.value, tc.value)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"email": "synthetic@example.invalid", "password": "not-a-password", "origin": "https://twenty.example.invalid"}
			addOptional(want, tc, "captchaToken", "locale", "verifyEmailRedirectPath")
			assertVariables(t, c, want)
			assertToken(t, response.GetLoginTokenFromCredentials.LoginToken, "synthetic-login-token", "2030-01-02T03:04:05.123456Z")
		})
	}
}

func TestLoginMixedOptionalVariables(t *testing.T) {
	t.Parallel()
	c := newMemoryClient(t, "GetLoginTokenFromCredentials", `{"getLoginTokenFromCredentials":{"loginToken":{
		"token":"synthetic-login-token","expiresAt":"2030-01-02T03:04:05Z"
	}}}`)
	_, err := client.GetLoginTokenFromCredentials(t.Context(), c,
		"synthetic@example.invalid", "not-a-password", "https://twenty.example.invalid",
		nullable.NewNullNullable[string](), nullable.NewNullableWithValue("en-US"), nil)
	if err != nil {
		t.Fatal(err)
	}
	assertVariables(t, c, map[string]any{
		"email": "synthetic@example.invalid", "password": "not-a-password", "origin": "https://twenty.example.invalid",
		"captchaToken": nil, "locale": "en-US",
	})
}

func TestExchangeAndRenewExpiry(t *testing.T) {
	t.Parallel()
	const tokens = `{"tokens":{
		"accessOrWorkspaceAgnosticToken":{"token":"synthetic-access-token","expiresAt":"2030-01-02T03:04:05.123456789+02:30"},
		"refreshToken":{"token":"synthetic-refresh-token","expiresAt":"2030-02-02T03:04:05Z"}
	}}`
	t.Run("exchange", func(t *testing.T) {
		c := newMemoryClient(t, "GetAuthTokensFromLoginToken", `{"getAuthTokensFromLoginToken":`+tokens+`}`)
		response, err := client.GetAuthTokensFromLoginToken(t.Context(), c, "synthetic-login-token", "https://twenty.example.invalid")
		if err != nil {
			t.Fatal(err)
		}
		assertVariables(t, c, map[string]any{"loginToken": "synthetic-login-token", "origin": "https://twenty.example.invalid"})
		pair := response.GetAuthTokensFromLoginToken.Tokens
		assertToken(t, pair.AccessOrWorkspaceAgnosticToken, "synthetic-access-token", "2030-01-02T03:04:05.123456789+02:30")
		assertToken(t, pair.RefreshToken, "synthetic-refresh-token", "2030-02-02T03:04:05Z")
	})
	t.Run("renew", func(t *testing.T) {
		c := newMemoryClient(t, "RenewToken", `{"renewToken":`+tokens+`}`)
		response, err := client.RenewToken(t.Context(), c, "synthetic-old-refresh-token")
		if err != nil {
			t.Fatal(err)
		}
		assertVariables(t, c, map[string]any{"appToken": "synthetic-old-refresh-token"})
		pair := response.RenewToken.Tokens
		assertToken(t, pair.AccessOrWorkspaceAgnosticToken, "synthetic-access-token", "2030-01-02T03:04:05.123456789+02:30")
		assertToken(t, pair.RefreshToken, "synthetic-refresh-token", "2030-02-02T03:04:05Z")
	})
}

const invitationsJSON = `[
	{"id":"00000000-0000-4000-8000-000000000050","email":"invited@example.invalid","expiresAt":"2030-01-02T03:04:05Z","roleId":"00000000-0000-4000-8000-000000000001"},
	{"id":"00000000-0000-4000-8000-000000000051","email":"default-role@example.invalid","expiresAt":"2030-01-03T03:04:05Z","roleId":null}
]`

func assertInvitations(t *testing.T, invitations []client.Invitation) {
	t.Helper()
	if len(invitations) != 2 || invitations[0].Id != "00000000-0000-4000-8000-000000000050" ||
		invitations[0].Email != "invited@example.invalid" || nullableValue(t, invitations[0].RoleId) != roleID {
		t.Fatalf("invitations did not decode: %#v", invitations)
	}
	if !invitations[0].ExpiresAt.Equal(time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatal("invitation expiry did not decode as time.Time")
	}
	assertNull(t, invitations[1].RoleId)
}

func TestInvitationOptionalRoleID(t *testing.T) {
	t.Parallel()
	for _, tc := range stringCases(roleID) {
		t.Run(tc.name, func(t *testing.T) {
			c := newMemoryClient(t, "SendInvitations", `{"sendInvitations":{"success":true,"errors":[],"result":`+invitationsJSON+`}}`)
			response, err := client.SendInvitations(t.Context(), c, []string{"invited@example.invalid", "default-role@example.invalid"}, tc.value)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"emails": []string{"invited@example.invalid", "default-role@example.invalid"}}
			addOptional(want, tc, "roleId")
			assertVariables(t, c, want)
			if !response.SendInvitations.Success || response.SendInvitations.Errors == nil || len(response.SendInvitations.Errors) != 0 {
				t.Fatalf("unexpected invitation result: %#v", response.SendInvitations)
			}
			assertInvitations(t, response.SendInvitations.Result)
		})
	}
}

func TestFindWorkspaceInvitations(t *testing.T) {
	t.Parallel()
	c := newMemoryClient(t, "FindWorkspaceInvitations", `{"findWorkspaceInvitations":`+invitationsJSON+`}`)
	response, err := client.FindWorkspaceInvitations(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	assertVariables(t, c, nil)
	assertInvitations(t, response.FindWorkspaceInvitations)
}

func roleDetailsJSON() string {
	return strings.TrimSuffix(rolePropertiesJSON, "}") + `,
		"permissionFlags":[{"id":"00000000-0000-4000-8000-000000000040","roleId":"` + roleID + `","flag":"ROLES"}],
		"objectPermissions":[` + objectPermissionJSON + `],
		"fieldPermissions":[` + fieldPermissionJSON + `],
		"rowLevelPermissionPredicates":[{
			"id":"00000000-0000-4000-8000-000000000022",
			"fieldMetadataId":"` + fieldID + `","objectMetadataId":"` + objectID + `",
			"operand":"IS_NOT_NULL","subFieldName":null,"workspaceMemberFieldMetadataId":null,
			"workspaceMemberSubFieldName":null,"rowLevelPermissionPredicateGroupId":null,
			"positionInRowLevelPermissionPredicateGroup":null,"roleId":"` + roleID + `","value":null
		}],
		"rowLevelPermissionPredicateGroups":[]
	}`
}

func assertRoleDetails(t *testing.T, role client.RoleDetails) {
	t.Helper()
	assertRoleProperties(t, role.RoleProperties)
	if len(role.PermissionFlags) != 1 || role.PermissionFlags[0].Flag != "ROLES" || role.PermissionFlags[0].RoleId != roleID ||
		len(role.ObjectPermissions) != 1 || len(role.FieldPermissions) != 1 || len(role.RowLevelPermissionPredicates) != 1 {
		t.Fatalf("role relations missing: %#v", role)
	}
	assertObjectPermission(t, role.ObjectPermissions[0])
	assertFieldPermission(t, role.FieldPermissions[0])
	predicate := role.RowLevelPermissionPredicates[0]
	if predicate.RoleId != roleID || predicate.Operand != client.RowLevelPermissionPredicateOperandIsNotNull {
		t.Fatalf("role predicate did not decode: %#v", predicate)
	}
	assertNull(t, predicate.Value)
	assertNull(t, predicate.PositionInRowLevelPermissionPredicateGroup)
}

func TestGetRolesDecodesRelations(t *testing.T) {
	t.Parallel()
	role := strings.TrimSuffix(roleDetailsJSON(), "}") + `,
		"workspaceMembers":[{"id":"` + memberID + `","userId":"` + userID + `","userWorkspaceId":null,"userEmail":"bootstrap@example.invalid","name":{"firstName":"Test","lastName":"Bootstrap"}}],
		"agents":[{"id":"00000000-0000-4000-8000-000000000060"}],
		"apiKeys":[{"id":"00000000-0000-4000-8000-000000000061","name":"synthetic-key","expiresAt":"2030-01-02T03:04:05Z","revokedAt":null}]
	}`
	c := newMemoryClient(t, "GetRoles", `{"getRoles":[`+role+`]}`)
	response, err := client.GetRoles(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	assertVariables(t, c, nil)
	if len(response.GetRoles) != 1 {
		t.Fatal("role result missing")
	}
	got := response.GetRoles[0]
	assertRoleDetails(t, got.RoleDetails)
	if len(got.WorkspaceMembers) != 1 || got.WorkspaceMembers[0].Id != memberID || got.WorkspaceMembers[0].UserId != userID ||
		len(got.Agents) != 1 || got.Agents[0].Id != "00000000-0000-4000-8000-000000000060" || len(got.ApiKeys) != 1 {
		t.Fatal("role assignments missing")
	}
	assertNull(t, got.WorkspaceMembers[0].UserWorkspaceId)
	assertNull(t, got.ApiKeys[0].RevokedAt)
	if !got.ApiKeys[0].ExpiresAt.Equal(time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatal("API key expiry did not decode")
	}
}

func TestJSONScalarValues(t *testing.T) {
	t.Parallel()
	for _, value := range []string{`{"flag":false,"nested":[0,null,""]}`, `[false,null,""]`, `false`, `true`, `0`, `""`, `null`} {
		t.Run(value, func(t *testing.T) {
			data := fmt.Sprintf(`{"upsertObjectPermissions":[{
				"objectMetadataId":%q,"canReadObjectRecords":false,"canUpdateObjectRecords":null,
				"canSoftDeleteObjectRecords":true,"canDestroyObjectRecords":null,"restrictedFields":%s,
				"rowLevelPermissionPredicates":[{"id":%q,"fieldMetadataId":%q,"objectMetadataId":%q,
					"operand":"IS","subFieldName":null,"workspaceMemberFieldMetadataId":null,
					"workspaceMemberSubFieldName":null,"rowLevelPermissionPredicateGroupId":null,
					"positionInRowLevelPermissionPredicateGroup":null,"roleId":%q,"value":%s}],"rowLevelPermissionPredicateGroups":[]
			}]}`, objectID, value, fieldID, fieldID, objectID, roleID, value)
			c := newMemoryClient(t, "UpsertObjectPermissions", data)
			response, err := client.UpsertObjectPermissions(t.Context(), c, client.UpsertObjectPermissionsInput{
				RoleId: roleID, ObjectPermissions: []client.ObjectPermissionInput{{ObjectMetadataId: objectID}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(response.UpsertObjectPermissions) != 1 || len(response.UpsertObjectPermissions[0].RowLevelPermissionPredicates) != 1 {
				t.Fatal("JSON scalar response missing")
			}
			permission := response.UpsertObjectPermissions[0]
			if nullableValue(t, permission.CanReadObjectRecords) || !nullableValue(t, permission.CanSoftDeleteObjectRecords) {
				t.Fatal("nullable false/true lost")
			}
			assertNull(t, permission.CanUpdateObjectRecords)
			assertNull(t, permission.CanDestroyObjectRecords)
			for _, raw := range []nullable.Nullable[json.RawMessage]{permission.RestrictedFields, permission.RowLevelPermissionPredicates[0].Value} {
				if value == "null" {
					assertNull(t, raw)
				} else {
					assertJSON(t, nullableValue(t, raw), value)
				}
			}
		})
	}
}

const workspaceJSON = `{
	"id":"00000000-0000-4000-8000-000000000100","displayName":"Synthetic workspace",
	"activationStatus":"ACTIVE","subdomain":"synthetic","customDomain":null,
	"createdAt":"2029-01-02T03:04:05Z","updatedAt":"2029-02-02T03:04:05Z",
	"workspaceMembersCount":3,"workspaceUrls":{"customUrl":null,"subdomainUrl":"https://synthetic.example.invalid"},
	"defaultRole":` + rolePropertiesJSON + `
}`

func assertWorkspace(t *testing.T, workspace client.WorkspaceIdentity) {
	t.Helper()
	if workspace.Id != workspaceID || nullableValue(t, workspace.DisplayName) != "Synthetic workspace" ||
		workspace.ActivationStatus != client.WorkspaceActivationStatusActive || workspace.Subdomain != "synthetic" ||
		nullableValue(t, workspace.WorkspaceMembersCount) != 3 || workspace.WorkspaceUrls.SubdomainUrl != "https://synthetic.example.invalid" {
		t.Fatalf("workspace identity did not decode: %#v", workspace)
	}
	assertNull(t, workspace.CustomDomain)
	assertNull(t, workspace.WorkspaceUrls.CustomUrl)
	assertRoleProperties(t, nullableValue(t, workspace.DefaultRole))
	if !workspace.CreatedAt.Equal(time.Date(2029, time.January, 2, 3, 4, 5, 0, time.UTC)) ||
		!workspace.UpdatedAt.Equal(time.Date(2029, time.February, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatal("workspace timestamps did not decode")
	}
}

func TestCurrentUserWorkspaceAndCompleteMembers(t *testing.T) {
	t.Parallel()
	bootstrap := `{"id":"` + memberID + `","userId":"` + userID + `","userWorkspaceId":"00000000-0000-4000-8000-000000000400",
		"userEmail":"bootstrap@example.invalid","name":{"firstName":"Test","lastName":"Bootstrap"},"roles":[` + roleDetailsJSON() + `]}`
	recovery := `{"id":"00000000-0000-4000-8000-000000000301","userId":"00000000-0000-4000-8000-000000000201",
		"userWorkspaceId":"00000000-0000-4000-8000-000000000401","userEmail":"recovery@example.invalid",
		"name":{"firstName":"Test","lastName":"Recovery"},"roles":[` + roleDetailsJSON() + `]}`
	other := `{"id":"00000000-0000-4000-8000-000000000302","userId":"00000000-0000-4000-8000-000000000202",
		"userWorkspaceId":null,"userEmail":"other@example.invalid","name":{"firstName":"Test","lastName":"Other"},"roles":[]}`
	c := newMemoryClient(t, "CurrentUser", `{"currentUser":{
		"id":"`+userID+`","email":"bootstrap@example.invalid","firstName":"Test","lastName":"Bootstrap",
		"isEmailVerified":true,"disabled":false,"hasPassword":true,"onboardingStatus":"COMPLETED",
		"currentWorkspace":`+workspaceJSON+`,
		"currentUserWorkspace":{"id":"00000000-0000-4000-8000-000000000400","userId":"`+userID+`",
			"permissionFlags":["ROLES","WORKSPACE_MEMBERS"],"objectPermissions":[`+objectPermissionJSON+`]},
		"workspaceMember":`+bootstrap+`,"workspaceMembers":[`+bootstrap+`,`+recovery+`,`+other+`],
		"availableWorkspaces":{
			"availableWorkspacesForSignIn":[{"id":"`+workspaceID+`","displayName":null,"workspaceUrls":{"customUrl":null,"subdomainUrl":"https://synthetic.example.invalid"}}],
			"availableWorkspacesForSignUp":[]
		}
	}}`)
	response, err := client.CurrentUser(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	assertVariables(t, c, nil)
	user := response.CurrentUser
	if user.Id != userID || user.Email != "bootstrap@example.invalid" || user.FirstName != "Test" || user.LastName != "Bootstrap" ||
		!user.IsEmailVerified || nullableValue(t, user.Disabled) || !user.HasPassword || nullableValue(t, user.OnboardingStatus) != client.OnboardingStatusCompleted {
		t.Fatalf("current user did not decode: %#v", user)
	}
	assertWorkspace(t, nullableValue(t, user.CurrentWorkspace))
	userWorkspace := nullableValue(t, user.CurrentUserWorkspace)
	if userWorkspace.Id != "00000000-0000-4000-8000-000000000400" || userWorkspace.UserId != userID ||
		!reflect.DeepEqual(userWorkspace.PermissionFlags, []client.PermissionFlagType{client.PermissionFlagTypeRoles, client.PermissionFlagTypeWorkspaceMembers}) ||
		len(userWorkspace.ObjectPermissions) != 1 {
		t.Fatalf("current user workspace permissions missing: %#v", userWorkspace)
	}
	assertObjectPermission(t, userWorkspace.ObjectPermissions[0])
	member := nullableValue(t, user.WorkspaceMember)
	if member.Id != memberID || member.UserId != userID || nullableValue(t, member.UserWorkspaceId) != userWorkspace.Id ||
		member.UserEmail != user.Email || member.Name.FirstName != "Test" || member.Name.LastName != "Bootstrap" || len(member.Roles) != 1 {
		t.Fatalf("bootstrap member identity missing: %#v", member)
	}
	assertRoleDetails(t, member.Roles[0])
	if len(user.WorkspaceMembers) != 3 || !reflect.DeepEqual(user.WorkspaceMembers[0], member) {
		t.Fatal("expected complete member list, including the current member")
	}
	for i, want := range []struct{ id, userID, email, lastName string }{
		{memberID, userID, "bootstrap@example.invalid", "Bootstrap"},
		{"00000000-0000-4000-8000-000000000301", "00000000-0000-4000-8000-000000000201", "recovery@example.invalid", "Recovery"},
		{"00000000-0000-4000-8000-000000000302", "00000000-0000-4000-8000-000000000202", "other@example.invalid", "Other"},
	} {
		got := user.WorkspaceMembers[i]
		if got.Id != want.id || got.UserId != want.userID || got.UserEmail != want.email || got.Name.LastName != want.lastName {
			t.Fatalf("member %d did not decode: %#v", i, got)
		}
	}
	if len(user.WorkspaceMembers[1].Roles) != 1 || len(user.WorkspaceMembers[2].Roles) != 0 {
		t.Fatal("member role assignments missing")
	}
	assertRoleDetails(t, user.WorkspaceMembers[1].Roles[0])
	if nullableValue(t, user.WorkspaceMembers[1].UserWorkspaceId) != "00000000-0000-4000-8000-000000000401" {
		t.Fatal("recovery member's user workspace ID missing")
	}
	assertNull(t, user.WorkspaceMembers[2].UserWorkspaceId)
	available := user.AvailableWorkspaces
	if len(available.AvailableWorkspacesForSignIn) != 1 || available.AvailableWorkspacesForSignIn[0].Id != workspaceID ||
		available.AvailableWorkspacesForSignUp == nil || len(available.AvailableWorkspacesForSignUp) != 0 {
		t.Fatal("available workspace lists missing")
	}
	assertNull(t, available.AvailableWorkspacesForSignIn[0].DisplayName)
}

func TestCurrentUserNullableIdentity(t *testing.T) {
	t.Parallel()
	c := newMemoryClient(t, "CurrentUser", `{"currentUser":{
		"id":"`+userID+`","email":"synthetic@example.invalid","firstName":"Test","lastName":"Member",
		"isEmailVerified":true,"hasPassword":true,"disabled":null,"onboardingStatus":null,
		"currentWorkspace":null,"currentUserWorkspace":null,"workspaceMember":null,
		"workspaceMembers":[],"availableWorkspaces":{"availableWorkspacesForSignIn":[],"availableWorkspacesForSignUp":[]}
	}}`)
	response, err := client.CurrentUser(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	assertNull(t, response.CurrentUser.Disabled)
	assertNull(t, response.CurrentUser.OnboardingStatus)
	assertNull(t, response.CurrentUser.CurrentWorkspace)
	assertNull(t, response.CurrentUser.CurrentUserWorkspace)
	assertNull(t, response.CurrentUser.WorkspaceMember)
	if response.CurrentUser.WorkspaceMembers == nil || len(response.CurrentUser.WorkspaceMembers) != 0 {
		t.Fatal("empty member list did not decode")
	}
}

func TestCurrentWorkspace(t *testing.T) {
	t.Parallel()
	c := newMemoryClient(t, "CurrentWorkspace", `{"currentWorkspace":`+workspaceJSON+`}`)
	response, err := client.CurrentWorkspace(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	assertVariables(t, c, nil)
	assertWorkspace(t, response.CurrentWorkspace.WorkspaceIdentity)
}

func TestPublicWorkspaceLookup(t *testing.T) {
	t.Parallel()
	t.Run("id", func(t *testing.T) {
		c := newMemoryClient(t, "GetPublicWorkspaceDataById", `{"getPublicWorkspaceDataById":{"id":"`+workspaceID+`","displayName":null}}`)
		response, err := client.GetPublicWorkspaceDataById(t.Context(), c, workspaceID)
		if err != nil {
			t.Fatal(err)
		}
		assertVariables(t, c, map[string]any{"id": workspaceID})
		if response.GetPublicWorkspaceDataById.Id != workspaceID {
			t.Fatal("workspace UUID did not decode")
		}
		assertNull(t, response.GetPublicWorkspaceDataById.DisplayName)
	})
	t.Run("origin", func(t *testing.T) {
		c := newMemoryClient(t, "GetPublicWorkspaceDataByDomain", `{"getPublicWorkspaceDataByDomain":{
			"id":"`+workspaceID+`","displayName":"Synthetic workspace",
			"workspaceUrls":{"customUrl":null,"subdomainUrl":"https://synthetic.example.invalid"},
			"authProviders":{"google":false,"password":true,"microsoft":false,"magicLink":false,"sso":[]},
			"authBypassProviders":null
		}}`)
		response, err := client.GetPublicWorkspaceDataByDomain(t.Context(), c, "https://synthetic.example.invalid")
		if err != nil {
			t.Fatal(err)
		}
		assertVariables(t, c, map[string]any{"origin": "https://synthetic.example.invalid"})
		workspace := response.GetPublicWorkspaceDataByDomain
		if workspace.Id != workspaceID || !workspace.AuthProviders.Password || workspace.AuthProviders.Google ||
			workspace.AuthProviders.Microsoft || workspace.AuthProviders.MagicLink || len(workspace.AuthProviders.Sso) != 0 {
			t.Fatal("public workspace identity or password provider flag missing")
		}
		assertNull(t, workspace.AuthBypassProviders)
	})
}

func TestMembershipMutationResponses(t *testing.T) {
	t.Parallel()
	t.Run("role", func(t *testing.T) {
		c := newMemoryClient(t, "UpdateWorkspaceMemberRole", `{"updateWorkspaceMemberRole":{
			"id":"`+memberID+`","userId":"`+userID+`","userWorkspaceId":null,
			"userEmail":"synthetic@example.invalid","name":{"firstName":"Test","lastName":"Member"},"roles":[`+rolePropertiesJSON+`]
		}}`)
		response, err := client.UpdateWorkspaceMemberRole(t.Context(), c, memberID, roleID)
		if err != nil {
			t.Fatal(err)
		}
		assertVariables(t, c, map[string]any{"workspaceMemberId": memberID, "roleId": roleID})
		member := response.UpdateWorkspaceMemberRole
		if member.Id != memberID || member.UserId != userID || len(member.Roles) != 1 {
			t.Fatal("updated member identity or role missing")
		}
		assertRoleProperties(t, member.Roles[0])
		assertNull(t, member.UserWorkspaceId)
	})
	t.Run("remove", func(t *testing.T) {
		// The resolver returns the membership loaded before deletion.
		c := newMemoryClient(t, "DeleteUserFromWorkspace", `{"deleteUserFromWorkspace":{
			"id":"00000000-0000-4000-8000-000000000400","userId":"`+userID+`","deletedAt":null
		}}`)
		response, err := client.DeleteUserFromWorkspace(t.Context(), c, memberID)
		if err != nil {
			t.Fatal(err)
		}
		assertVariables(t, c, map[string]any{"workspaceMemberIdToDelete": memberID})
		deleted := response.DeleteUserFromWorkspace
		if deleted.UserId != userID {
			t.Fatal("removed membership identity did not decode")
		}
		assertNull(t, deleted.DeletedAt)
	})
	for _, result := range []string{"success", "error"} {
		t.Run("revoke/"+result, func(t *testing.T) {
			c := newMemoryClient(t, "DeleteWorkspaceInvitation", `{"deleteWorkspaceInvitation":"`+result+`"}`)
			response, err := client.DeleteWorkspaceInvitation(t.Context(), c, "synthetic-invitation-id")
			if err != nil {
				t.Fatal(err)
			}
			assertVariables(t, c, map[string]any{"appTokenId": "synthetic-invitation-id"})
			if response.DeleteWorkspaceInvitation != result {
				t.Fatal("invitation revocation result did not decode")
			}
		})
	}
}

func TestGeneratedErrorAndContextPropagation(t *testing.T) {
	t.Parallel()
	operations := []struct {
		name string
		call func(context.Context, graphql.Client) error
	}{
		{"CreateOneRole", func(ctx context.Context, c graphql.Client) error {
			_, err := client.CreateOneRole(ctx, c, client.CreateRoleInput{})
			return err
		}},
		{"CurrentUser", func(ctx context.Context, c graphql.Client) error { _, err := client.CurrentUser(ctx, c); return err }},
		{"CurrentWorkspace", func(ctx context.Context, c graphql.Client) error {
			_, err := client.CurrentWorkspace(ctx, c)
			return err
		}},
		{"DeleteOneRole", func(ctx context.Context, c graphql.Client) error {
			_, err := client.DeleteOneRole(ctx, c, roleID)
			return err
		}},
		{"DeleteUserFromWorkspace", func(ctx context.Context, c graphql.Client) error {
			_, err := client.DeleteUserFromWorkspace(ctx, c, memberID)
			return err
		}},
		{"DeleteWorkspaceInvitation", func(ctx context.Context, c graphql.Client) error {
			_, err := client.DeleteWorkspaceInvitation(ctx, c, "synthetic-id")
			return err
		}},
		{"FindWorkspaceInvitations", func(ctx context.Context, c graphql.Client) error {
			_, err := client.FindWorkspaceInvitations(ctx, c)
			return err
		}},
		{"GetAuthTokensFromLoginToken", func(ctx context.Context, c graphql.Client) error {
			_, err := client.GetAuthTokensFromLoginToken(ctx, c, "synthetic-token", "https://synthetic.example.invalid")
			return err
		}},
		{"GetLoginTokenFromCredentials", func(ctx context.Context, c graphql.Client) error {
			_, err := client.GetLoginTokenFromCredentials(ctx, c, "synthetic@example.invalid", "not-a-password", "https://synthetic.example.invalid", nil, nil, nil)
			return err
		}},
		{"GetPublicWorkspaceDataByDomain", func(ctx context.Context, c graphql.Client) error {
			_, err := client.GetPublicWorkspaceDataByDomain(ctx, c, "https://synthetic.example.invalid")
			return err
		}},
		{"GetPublicWorkspaceDataById", func(ctx context.Context, c graphql.Client) error {
			_, err := client.GetPublicWorkspaceDataById(ctx, c, workspaceID)
			return err
		}},
		{"GetRoles", func(ctx context.Context, c graphql.Client) error { _, err := client.GetRoles(ctx, c); return err }},
		{"RenewToken", func(ctx context.Context, c graphql.Client) error {
			_, err := client.RenewToken(ctx, c, "synthetic-token")
			return err
		}},
		{"SendInvitations", func(ctx context.Context, c graphql.Client) error {
			_, err := client.SendInvitations(ctx, c, []string{}, nil)
			return err
		}},
		{"UpdateOneRole", func(ctx context.Context, c graphql.Client) error {
			_, err := client.UpdateOneRole(ctx, c, client.UpdateRoleInput{})
			return err
		}},
		{"UpdateWorkspaceMemberRole", func(ctx context.Context, c graphql.Client) error {
			_, err := client.UpdateWorkspaceMemberRole(ctx, c, memberID, roleID)
			return err
		}},
		{"UpsertFieldPermissions", func(ctx context.Context, c graphql.Client) error {
			_, err := client.UpsertFieldPermissions(ctx, c, client.UpsertFieldPermissionsInput{})
			return err
		}},
		{"UpsertObjectPermissions", func(ctx context.Context, c graphql.Client) error {
			_, err := client.UpsertObjectPermissions(ctx, c, client.UpsertObjectPermissionsInput{})
			return err
		}},
		{"UpsertPermissionFlags", func(ctx context.Context, c graphql.Client) error {
			_, err := client.UpsertPermissionFlags(ctx, c, client.UpsertPermissionFlagsInput{})
			return err
		}},
	}
	for _, operation := range operations {
		for _, mode := range []string{"client-error", "cancelled", "deadline"} {
			t.Run(operation.name+"/"+mode, func(t *testing.T) {
				ctx := t.Context()
				want := errors.New("synthetic client failure")
				c := newMemoryClient(t, operation.name, `{}`)
				c.err = want
				switch mode {
				case "cancelled":
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
					want = context.Canceled
				case "deadline":
					var cancel context.CancelFunc
					ctx, cancel = context.WithDeadline(ctx, time.Unix(1, 0))
					defer cancel()
					want = context.DeadlineExceeded
				}
				if err := operation.call(ctx, c); !errors.Is(err, want) {
					t.Fatalf("error = %v, want %v", err, want)
				}
				if c.ctx != ctx {
					t.Fatal("generated operation replaced the caller's context")
				}
			})
		}
	}
}

func TestGeneratedDecodeErrors(t *testing.T) {
	t.Parallel()
	t.Run("date-time", func(t *testing.T) {
		c := newMemoryClient(t, "GetLoginTokenFromCredentials", `{"getLoginTokenFromCredentials":{"loginToken":{"token":"synthetic-token","expiresAt":"not-a-date"}}}`)
		_, err := client.GetLoginTokenFromCredentials(t.Context(), c, "synthetic@example.invalid", "not-a-password", "https://synthetic.example.invalid", nil, nil, nil)
		var parseError *time.ParseError
		if !errors.As(err, &parseError) {
			t.Fatalf("error = %v, want time.ParseError", err)
		}
	})
	t.Run("uuid", func(t *testing.T) {
		c := newMemoryClient(t, "DeleteOneRole", `{"deleteOneRole":123}`)
		_, err := client.DeleteOneRole(t.Context(), c, roleID)
		var typeError *json.UnmarshalTypeError
		if !errors.As(err, &typeError) {
			t.Fatalf("error = %v, want json.UnmarshalTypeError", err)
		}
	})
	t.Run("json", func(t *testing.T) {
		c := newMemoryClient(t, "GetRoles", `{`)
		_, err := client.GetRoles(t.Context(), c)
		var syntaxError *json.SyntaxError
		if !errors.As(err, &syntaxError) {
			t.Fatalf("error = %v, want json.SyntaxError", err)
		}
	})
	t.Run("graphql-partial-data", func(t *testing.T) {
		c := newMemoryClient(t, "GetRoles", `{"getRoles":[]}`)
		c.response = `{"data":{"getRoles":[]},"errors":[{"message":"synthetic permission denial","path":["getRoles"]}]}`
		response, err := client.GetRoles(t.Context(), c)
		if err == nil || !strings.Contains(err.Error(), "synthetic permission denial") || response == nil || response.GetRoles == nil {
			t.Fatalf("partial response or GraphQL error lost: response %#v, error %v", response, err)
		}
	})
}
