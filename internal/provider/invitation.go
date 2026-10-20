// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
)

var (
	errInvalidInvitationResponse = errors.New("twenty returned malformed, ambiguous, or incomplete invitation data")
	// time.Time decoding otherwise accepts invalid offsets and truncates
	// fractional seconds beyond its nanosecond precision.
	invitationTimePattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?(Z|[+-]([01][0-9]|2[0-3]):[0-5][0-9])$`)
)

// Preserve error classification without exposing arbitrary transport messages.
type invitationReadError struct{ cause error }

func (e invitationReadError) Error() string { return client.DiagnosticMessage(e.cause) }
func (e invitationReadError) Unwrap() error { return e.cause }

// Validate the complete unpaginated list before generated decoding can lose
// missing/null fields. Only the generated token-free read operation is allowed.
type invitationQueryClient struct{ graphql.Client }

func (c invitationQueryClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	if req.OpName != "FindWorkspaceInvitations" || req.Query != client.FindWorkspaceInvitations_Operation {
		return errInvalidInvitationResponse
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var raw json.RawMessage
	wire := &graphql.Response{Data: &raw}
	if err := c.Client.MakeRequest(ctx, req, wire); err != nil {
		return invitationReadError{err}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Even a client returning partial data without an error must not establish
	// that no persisted role references exist. Do not expose GraphQL messages.
	if len(wire.Errors) != 0 {
		return invitationReadError{wire.Errors}
	}
	data, err := invitationWireObject(raw)
	if err != nil || len(data) != 1 || validateInvitationList(data["findWorkspaceInvitations"]) != nil {
		return errInvalidInvitationResponse
	}
	if json.Unmarshal(raw, resp.Data) != nil {
		return errInvalidInvitationResponse
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	resp.Extensions = wire.Extensions
	return nil
}

// Reject duplicate keys rather than letting encoding/json silently keep the
// last value. Callers also require exactly the selected, case-sensitive keys.
func invitationWireObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, errInvalidInvitationResponse
	}
	object := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || object[key] != nil {
			return nil, errInvalidInvitationResponse
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil, errInvalidInvitationResponse
		}
		object[key] = value
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, errInvalidInvitationResponse
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errInvalidInvitationResponse
	}
	return object, nil
}

func validateInvitationList(raw json.RawMessage) error {
	var invitations []json.RawMessage
	if json.Unmarshal(raw, &invitations) != nil || invitations == nil {
		return errInvalidInvitationResponse
	}
	ids := map[string]bool{}
	for _, rawInvite := range invitations {
		invite, err := invitationWireObject(rawInvite)
		if err != nil || len(invite) != 4 {
			return errInvalidInvitationResponse
		}
		var id, email, expiresText string
		var expires *time.Time
		var roleID *string
		if !workspaceWireUUID(invite["id"]) || json.Unmarshal(invite["id"], &id) != nil || ids[strings.ToLower(id)] ||
			json.Unmarshal(invite["email"], &email) != nil || client.ValidateEmail(email) != nil || email == "" || strings.TrimSpace(email) != email ||
			json.Unmarshal(invite["expiresAt"], &expiresText) != nil || !invitationTimePattern.MatchString(expiresText) ||
			json.Unmarshal(invite["expiresAt"], &expires) != nil || expires == nil || expires.IsZero() ||
			len(invite["roleId"]) == 0 || json.Unmarshal(invite["roleId"], &roleID) != nil || (roleID != nil && !workspaceWireUUID(invite["roleId"])) {
			return errInvalidInvitationResponse
		}
		ids[strings.ToLower(id)] = true
	}
	return nil
}

// readWorkspaceInvitations retains expired rows and explicit nullable role IDs.
// Enumerating persisted references requires neither resolving a default role
// nor proving that a referenced role currently exists.
func readWorkspaceInvitations(ctx context.Context, api graphql.Client) ([]client.Invitation, error) {
	response, err := client.FindWorkspaceInvitations(ctx, invitationQueryClient{api})
	if err != nil {
		return nil, err
	}
	return response.FindWorkspaceInvitations, nil
}
