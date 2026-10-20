// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

const invitationTestID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"

// Supply the raw data object, not a generated struct that could already have
// lost omitted fields. Every forwarded request must be the single selected read.
type invitationTestClient struct {
	t       *testing.T
	data    string
	err     error
	errors  gqlerror.List
	calls   int
	context context.Context
	before  func()
}

func (c *invitationTestClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	c.calls++
	if req.OpName != "FindWorkspaceInvitations" || req.Query != client.FindWorkspaceInvitations_Operation || req.Variables != nil {
		c.t.Fatal("invitation enumeration issued an unexpected query or mutation")
	}
	if c.context != nil && ctx != c.context {
		c.t.Fatal("invitation read replaced the caller context")
	}
	if c.before != nil {
		c.before()
	}
	if c.err != nil {
		return c.err
	}
	raw, ok := resp.Data.(*json.RawMessage)
	if !ok {
		c.t.Fatal("invitation validation must precede generated decoding")
	}
	*raw = json.RawMessage(c.data)
	resp.Errors = c.errors
	return nil
}

func invitationTestRow() map[string]any {
	return map[string]any{"id": invitationTestID, "email": "pending@example.test", "roleId": roleTestID, "expiresAt": "2099-01-02T03:04:05.123456789+02:00"}
}

func invitationTestData(t *testing.T, rows ...any) string {
	t.Helper()
	if rows == nil {
		rows = []any{}
	}
	raw, err := json.Marshal(map[string]any{"findWorkspaceInvitations": rows})
	if err != nil {
		t.Fatal("encode synthetic invitation data")
	}
	return string(raw)
}

func TestReadWorkspaceInvitationsCompleteAndEmpty(t *testing.T) {
	t.Parallel()
	// The generated selection is a flat list, not edges/nodes or embedded
	// fragments. It contains no tokens/links or pagination/expiration filters.
	wantQuery := "query FindWorkspaceInvitations { findWorkspaceInvitations { ... Invitation } } fragment Invitation on WorkspaceInvitation { id email expiresAt roleId }"
	if strings.Join(strings.Fields(client.FindWorkspaceInvitations_Operation), " ") != wantQuery {
		t.Fatal("review the pinned invitation selection contract")
	}
	for _, empty := range []bool{false, true} {
		t.Run(fmt.Sprint(empty), func(t *testing.T) {
			t.Parallel()
			data := invitationTestData(t)
			if !empty {
				row := invitationTestRow()
				// Preserve upstream email and UUID case. Listing does not manage
				// the mailbox or resolve its explicit role against a role list.
				row["id"], row["email"], row["roleId"] = strings.ToUpper(invitationTestID), "Pending@example.test", strings.ToUpper(memberMembershipID)
				data = invitationTestData(t, row)
			}
			api := &invitationTestClient{t: t, data: data, context: t.Context()}
			invitations, err := readWorkspaceInvitations(t.Context(), api)
			if err != nil || invitations == nil || api.calls != 1 {
				t.Fatal("complete or empty invitation list rejected")
			}
			if empty {
				if len(invitations) != 0 {
					t.Fatal("empty list decoded with entries")
				}
				return
			}
			expires, _ := time.Parse(time.RFC3339Nano, invitationTestRow()["expiresAt"].(string))
			if len(invitations) != 1 || invitations[0].Id != strings.ToUpper(invitationTestID) || invitations[0].Email != "Pending@example.test" || !invitations[0].ExpiresAt.Equal(expires) || invitations[0].RoleId.GetOrEmpty() != strings.ToUpper(memberMembershipID) {
				t.Fatal("generated invitation fields were changed or lost")
			}
		})
	}
}

func TestReadWorkspaceInvitationsRetainsExpiredAndNullableRoles(t *testing.T) {
	t.Parallel()
	pending, expired, defaultRole := invitationTestRow(), invitationTestRow(), invitationTestRow()
	expired["id"], expired["email"], expired["expiresAt"] = memberNativeID, "expired@example.test", "2000-01-02T03:04:05Z"
	defaultRole["id"], defaultRole["email"], defaultRole["roleId"] = memberUserID, "default@example.test", nil
	api := &invitationTestClient{t: t, data: invitationTestData(t, pending, expired, defaultRole)}
	invitations, err := readWorkspaceInvitations(t.Context(), api)
	if err != nil || len(invitations) != 3 || api.calls != 1 {
		t.Fatal("persisted invitation rows were filtered or required role lookup")
	}
	if invitations[1].ExpiresAt.After(time.Now()) || invitations[1].RoleId.GetOrEmpty() != roleTestID || !invitations[2].RoleId.IsSpecified() || !invitations[2].RoleId.IsNull() {
		t.Fatal("expired explicit role reference or nullable default-role semantics lost")
	}
}

func TestReadWorkspaceInvitationsRejectsMalformedFields(t *testing.T) {
	t.Parallel()
	changes := map[string]struct {
		field string
		value any
	}{
		"bad id":             {"id", "private-invalid-id"},
		"compact id":         {"id", strings.ReplaceAll(invitationTestID, "-", "")},
		"nil id":             {"id", "00000000-0000-0000-0000-000000000000"},
		"padded id":          {"id", " " + invitationTestID},
		"numeric id":         {"id", 123},
		"bad role":           {"roleId", "private-invalid-role"},
		"empty role":         {"roleId", ""},
		"nil role":           {"roleId", "00000000-0000-0000-0000-000000000000"},
		"numeric role":       {"roleId", 123},
		"object role":        {"roleId", map[string]any{"id": roleTestID}},
		"empty email":        {"email", ""},
		"bad email":          {"email", "private-not-an-email"},
		"padded email":       {"email", " pending@example.test"},
		"display name email": {"email", "Private Name <pending@example.test>"},
		"non ASCII email":    {"email", "privaté@example.test"},
		"control email":      {"email", "pending@example.test\n"},
		"numeric email":      {"email", 123},
		"bad time":           {"expiresAt", "private-invalid-time"},
		"zero time":          {"expiresAt", "0001-01-01T00:00:00Z"},
		"date only":          {"expiresAt", "2099-01-02"},
		"numeric time":       {"expiresAt", 123},
		"lossy precision":    {"expiresAt", "2099-01-02T03:04:05.1234567891Z"},
		"invalid offset min": {"expiresAt", "2099-01-02T03:04:05+00:60"},
		"invalid offset hr":  {"expiresAt", "2099-01-02T03:04:05+24:00"},
		"single digit hour":  {"expiresAt", "2099-01-02T3:04:05Z"},
		"comma fraction":     {"expiresAt", "2099-01-02T03:04:05,123Z"},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			row := invitationTestRow()
			row[change.field] = change.value
			assertInvalidInvitationRead(t, invitationTestData(t, row))
		})
	}
	for _, field := range []string{"id", "email", "expiresAt", "roleId"} {
		for _, missing := range []bool{false, true} {
			if field == "roleId" && !missing {
				continue // Explicit null is valid, omission is not.
			}
			t.Run(field+fmt.Sprint(missing), func(t *testing.T) {
				t.Parallel()
				row := invitationTestRow()
				if missing {
					delete(row, field)
				} else {
					row[field] = nil
				}
				assertInvalidInvitationRead(t, invitationTestData(t, row))
			})
		}
	}
}

func assertInvalidInvitationRead(t *testing.T, data string) {
	t.Helper()
	api := &invitationTestClient{t: t, data: data}
	invitations, err := readWorkspaceInvitations(t.Context(), api)
	if invitations != nil || err != errInvalidInvitationResponse || err.Error() != errInvalidInvitationResponse.Error() || api.calls != 1 {
		t.Fatal("incomplete invitation list accepted or malformed data escaped")
	}
}

func TestReadWorkspaceInvitationsRejectsIncompleteOrLossyLists(t *testing.T) {
	t.Parallel()
	row := invitationTestRow()
	rowJSON, _ := json.Marshal(row)
	for name, data := range map[string]string{
		"missing list":      `{}`,
		"null list":         `{"findWorkspaceInvitations":null}`,
		"object list":       `{"findWorkspaceInvitations":{}}`,
		"string list":       `{"findWorkspaceInvitations":"private-invalid-list"}`,
		"nested list":       `{"findWorkspaceInvitations":[[]]}`,
		"null row":          `{"findWorkspaceInvitations":[null]}`,
		"string row":        `{"findWorkspaceInvitations":["private-invalid-row"]}`,
		"empty row":         `{"findWorkspaceInvitations":[{}]}`,
		"fragment wrapper":  `{"findWorkspaceInvitations":[{"Invitation":` + string(rowJSON) + `}]}`,
		"null data":         `null`,
		"invalid data":      `private-invalid-json`,
		"truncated data":    `{"findWorkspaceInvitations":[`,
		"trailing data":     invitationTestData(t) + `{}`,
		"duplicate list":    `{"findWorkspaceInvitations":[` + string(rowJSON) + `],"findWorkspaceInvitations":[]}`,
		"case variant list": `{"findWorkspaceInvitations":[` + string(rowJSON) + `],"FindWorkspaceInvitations":[]}`,
		"duplicate role":    `{"findWorkspaceInvitations":[` + strings.TrimSuffix(string(rowJSON), "}") + `,"roleId":null}]}`,
		"case variant role": `{"findWorkspaceInvitations":[` + strings.TrimSuffix(string(rowJSON), "}") + `,"RoleId":null}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assertInvalidInvitationRead(t, data)
		})
	}
	for _, uppercase := range []bool{false, true} {
		t.Run("duplicate UUID"+fmt.Sprint(uppercase), func(t *testing.T) {
			t.Parallel()
			duplicate := invitationTestRow()
			duplicate["email"] = "other@example.test"
			if uppercase {
				duplicate["id"] = strings.ToUpper(invitationTestID)
			}
			assertInvalidInvitationRead(t, invitationTestData(t, row, duplicate))
		})
	}
	for _, badFirst := range []bool{false, true} {
		t.Run("mixed"+fmt.Sprint(badFirst), func(t *testing.T) {
			t.Parallel()
			malformed := invitationTestRow()
			malformed["id"] = memberNativeID
			delete(malformed, "roleId")
			rows := []any{row, malformed}
			if badFirst {
				rows[0], rows[1] = rows[1], rows[0]
			}
			assertInvalidInvitationRead(t, invitationTestData(t, rows...))
		})
	}
}

func TestInvitationReadContextAndRedactedErrors(t *testing.T) {
	t.Parallel()
	for _, cause := range []error{
		errors.New("private-password private-token private-member@example.test"),
		fmt.Errorf("private-token: %w", context.Canceled),
		fmt.Errorf("private-token: %w", context.DeadlineExceeded),
		&graphql.HTTPError{StatusCode: 503, Response: graphql.Response{Errors: gqlerror.List{&gqlerror.Error{Message: "private-token"}}}},
	} {
		api := &invitationTestClient{t: t, err: cause, context: t.Context()}
		invitations, err := readWorkspaceInvitations(t.Context(), api)
		if invitations != nil || err == nil || !errors.Is(err, cause) || err.Error() != client.DiagnosticMessage(cause) || strings.Contains(err.Error(), "private-") || api.calls != 1 {
			t.Fatal("transport error classification or redaction lost")
		}
	}
	for _, during := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		api := &invitationTestClient{t: t, data: invitationTestData(t), context: ctx}
		if during {
			api.before = cancel
		} else {
			cancel()
		}
		invitations, err := readWorkspaceInvitations(ctx, api)
		cancel()
		if invitations != nil || !errors.Is(err, context.Canceled) || api.calls != map[bool]int{false: 0, true: 1}[during] {
			t.Fatal("canceled invitation read accepted data or issued another request")
		}
	}
	ctx, cancel := context.WithDeadline(t.Context(), time.Unix(1, 0))
	defer cancel()
	api := &invitationTestClient{t: t, data: invitationTestData(t)}
	if invitations, err := readWorkspaceInvitations(ctx, api); invitations != nil || !errors.Is(err, context.DeadlineExceeded) || api.calls != 0 {
		t.Fatal("expired context reached the transport")
	}
	api = &invitationTestClient{t: t, data: invitationTestData(t), errors: gqlerror.List{&gqlerror.Error{Message: "private-token"}}}
	if invitations, err := readWorkspaceInvitations(t.Context(), api); invitations != nil || err == nil || strings.Contains(err.Error(), "private-") {
		t.Fatal("partial GraphQL response accepted or exposed upstream messages")
	}
}

type invitationCancelDecode struct{ cancel context.CancelFunc }

func (d *invitationCancelDecode) UnmarshalJSON([]byte) error {
	d.cancel()
	return nil
}

func TestInvitationQueryClientCancellationDuringDecoding(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	api := &invitationTestClient{t: t, data: invitationTestData(t, invitationTestRow()), context: ctx}
	data := &invitationCancelDecode{cancel: cancel}
	req := &graphql.Request{OpName: "FindWorkspaceInvitations", Query: client.FindWorkspaceInvitations_Operation}
	if err := (invitationQueryClient{api}).MakeRequest(ctx, req, &graphql.Response{Data: data}); !errors.Is(err, context.Canceled) || api.calls != 1 {
		t.Fatal("invitation decoding ignored cancellation")
	}
}

func TestInvitationQueryClientDecodeFailureIsRedacted(t *testing.T) {
	t.Parallel()
	api := &invitationTestClient{t: t, data: invitationTestData(t, invitationTestRow())}
	var incompatible int
	req := &graphql.Request{OpName: "FindWorkspaceInvitations", Query: client.FindWorkspaceInvitations_Operation}
	if err := (invitationQueryClient{api}).MakeRequest(t.Context(), req, &graphql.Response{Data: &incompatible}); err != errInvalidInvitationResponse || api.calls != 1 {
		t.Fatal("generated decoding failure was not sanitized")
	}
}

func TestInvitationQueryClientRejectsOtherOperations(t *testing.T) {
	t.Parallel()
	api := &invitationTestClient{t: t}
	guarded := invitationQueryClient{api}
	for _, req := range []*graphql.Request{
		{OpName: "DeleteOneRole", Query: client.DeleteOneRole_Operation},
		{OpName: "DeleteWorkspaceInvitation", Query: client.DeleteWorkspaceInvitation_Operation},
		{OpName: "GetRoles", Query: client.GetRoles_Operation},
		{OpName: "FindWorkspaceInvitations", Query: client.DeleteWorkspaceInvitation_Operation},
	} {
		var data client.FindWorkspaceInvitationsResponse
		if err := guarded.MakeRequest(t.Context(), req, &graphql.Response{Data: &data}); err != errInvalidInvitationResponse || api.calls != 0 {
			t.Fatal("read guard forwarded an unselected query or mutation")
		}
	}
}

func TestInvitationValidationPreservesMembershipErrors(t *testing.T) {
	t.Parallel()
	if validateInvitations(json.RawMessage(`[]`)) != nil {
		t.Fatal("membership rejected a complete empty list")
	}
	for _, data := range []string{`null`, `[{}]`, `["private-token"]`} {
		if err := validateInvitations(json.RawMessage(data)); err != errInvalidMemberResponse {
			t.Fatal("shared validation changed the membership diagnostic")
		}
	}
}
