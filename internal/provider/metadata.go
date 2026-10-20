// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/oapi-codegen/nullable"
)

var (
	errInvalidMetadataResponse = errors.New("twenty returned malformed or incomplete object or field metadata")
	errUnsafeMetadata          = errors.New("only workspace-custom metadata can be managed; built-in, system, remote, and application-owned metadata are protected")
	errMetadataConflict        = errors.New("metadata with this name already exists; import the intended custom metadata by UUID instead of adopting it by name")
	errMetadataNotFound        = errors.New("no metadata matches the selector in the authenticated workspace")
	errAmbiguousMetadata       = errors.New("more than one metadata item matches the selector; use a native UUID")
	errMetadataProtected       = errors.New("deletion_protection is enabled; apply deletion_protection = false before destroying this resource; deletion permanently removes its CRM data")
	metadataNamePattern        = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)
)

func metadataUUID(id string) bool {
	return validRoleUUID(id) && id != "00000000-0000-0000-0000-000000000000"
}

func metadataName(name string) bool {
	return len(name) <= 63 && metadataNamePattern.MatchString(name)
}

func metadataError(d *diag.Diagnostics, action, kind string, err error) {
	detail := client.DiagnosticMessage(err)
	for _, known := range []error{errInvalidMetadataResponse, errUnsafeMetadata, errMetadataConflict, errMetadataNotFound, errAmbiguousMetadata, errMetadataProtected, errWorkspaceIdentityMismatch, errInvalidWorkspaceResponse} {
		if errors.Is(err, known) {
			detail = known.Error()
			break
		}
	}
	d.AddError("Unable to "+action+" Twenty "+kind, detail)
}

// Check consumed fields before generated Go types can collapse absent values to
// zeros. Incomplete pagination must never be mistaken for metadata absence.
type metadataQueryClient struct{ graphql.Client }

func (c metadataQueryClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	var raw json.RawMessage
	wire := &graphql.Response{Data: &raw}
	if err := c.Client.MakeRequest(ctx, req, wire); err != nil {
		return err
	}
	var data map[string]json.RawMessage
	if json.Unmarshal(raw, &data) != nil || data == nil {
		return errInvalidMetadataResponse
	}
	switch req.OpName {
	case "ListMetadataObjects", "ListMetadataFields":
		root, field := "objects", false
		if req.OpName == "ListMetadataFields" {
			root, field = "fields", true
		}
		if err := validateMetadataConnection(data[root], field); err != nil {
			return err
		}
	case "CreateMetadataObject", "UpdateMetadataObject", "DeleteMetadataObject", "CreateMetadataField", "UpdateMetadataField", "DeleteMetadataField":
		root := map[string]string{"CreateMetadataObject": "createOneObject", "UpdateMetadataObject": "updateOneObject", "DeleteMetadataObject": "deleteOneObject", "CreateMetadataField": "createOneField", "UpdateMetadataField": "updateOneField", "DeleteMetadataField": "deleteOneField"}[req.OpName]
		var item map[string]json.RawMessage
		if json.Unmarshal(data[root], &item) != nil || !workspaceWireUUID(item["id"]) {
			return errInvalidMetadataResponse
		}
	default:
		return errInvalidMetadataResponse
	}
	if json.Unmarshal(raw, resp.Data) != nil {
		return errInvalidMetadataResponse
	}
	resp.Extensions, resp.Errors = wire.Extensions, wire.Errors
	return nil
}

func validateMetadataConnection(raw json.RawMessage, field bool) error {
	var connection struct {
		Edges    []struct{ Node json.RawMessage }
		PageInfo json.RawMessage
	}
	if json.Unmarshal(raw, &connection) != nil || connection.Edges == nil {
		return errInvalidMetadataResponse
	}
	var page struct {
		HasNextPage *bool
		EndCursor   *string
	}
	var props map[string]json.RawMessage
	if json.Unmarshal(connection.PageInfo, &page) != nil || page.HasNextPage == nil || json.Unmarshal(connection.PageInfo, &props) != nil || len(props["endCursor"]) == 0 || (*page.HasNextPage && (page.EndCursor == nil || *page.EndCursor == "" || len(connection.Edges) == 0)) {
		return errInvalidMetadataResponse
	}
	seen := map[string]bool{}
	for _, edge := range connection.Edges {
		var item map[string]json.RawMessage
		if json.Unmarshal(edge.Node, &item) != nil {
			return errInvalidMetadataResponse
		}
		for _, key := range []string{"id", "applicationId", "universalIdentifier"} {
			if !workspaceWireUUID(item[key]) {
				return errInvalidMetadataResponse
			}
		}
		var id string
		_ = json.Unmarshal(item["id"], &id)
		if seen[strings.ToLower(id)] {
			return errInvalidMetadataResponse
		}
		seen[strings.ToLower(id)] = true
		names, flags := []string{"nameSingular", "namePlural", "labelSingular", "labelPlural"}, []string{"isActive", "isSystem", "isRemote", "isLabelSyncedWithName"}
		if !field {
			var identifier *string
			if len(item["labelIdentifierFieldMetadataId"]) == 0 || json.Unmarshal(item["labelIdentifierFieldMetadataId"], &identifier) != nil || identifier != nil && !metadataUUID(*identifier) {
				return errInvalidMetadataResponse
			}
		}
		if field {
			if !workspaceWireUUID(item["objectMetadataId"]) {
				return errInvalidMetadataResponse
			}
			names, flags = []string{"name", "label"}, []string{"isActive", "isSystem", "isLabelSyncedWithName", "isNullable"}
			var kind client.FieldMetadataType
			if json.Unmarshal(item["type"], &kind) != nil || !slices.Contains(client.AllFieldMetadataType, kind) {
				return errInvalidMetadataResponse
			}
		}
		for _, key := range names {
			var value string
			if json.Unmarshal(item[key], &value) != nil || strings.TrimSpace(value) == "" {
				return errInvalidMetadataResponse
			}
		}
		for _, key := range flags {
			var value *bool
			if json.Unmarshal(item[key], &value) != nil || value == nil {
				return errInvalidMetadataResponse
			}
		}
		for _, key := range []string{"description", "icon"} {
			var value *string
			if len(item[key]) == 0 || json.Unmarshal(item[key], &value) != nil {
				return errInvalidMetadataResponse
			}
		}
		var writability client.MetadataWritability
		if json.Unmarshal(item["writability"], &writability) != nil || !slices.Contains(client.AllMetadataWritability, writability) {
			return errInvalidMetadataResponse
		}
	}
	return nil
}

func readMetadataObjects(ctx context.Context, api graphql.Client) ([]client.ObjectMetadata, error) {
	result := []client.ObjectMetadata{}
	paging := client.CursorPaging{First: nullable.NewNullableWithValue(100)}
	seen, cursors := map[string]bool{}, map[string]bool{}
	for {
		response, err := client.ListMetadataObjects(ctx, metadataQueryClient{api}, paging)
		if err != nil {
			return nil, err
		}
		for _, edge := range response.Objects.Edges {
			key := strings.ToLower(edge.Node.Id)
			if seen[key] {
				return nil, errInvalidMetadataResponse
			}
			seen[key] = true
			result = append(result, edge.Node)
		}
		if !response.Objects.PageInfo.HasNextPage.GetOrEmpty() {
			return result, nil
		}
		cursor := response.Objects.PageInfo.EndCursor.GetOrEmpty()
		if cursors[cursor] {
			return nil, errInvalidMetadataResponse
		}
		cursors[cursor] = true
		paging.After = nullable.NewNullableWithValue(cursor)
	}
}

func readMetadataObject(ctx context.Context, api graphql.Client, id string) (*client.ObjectMetadata, error) {
	if !metadataUUID(id) {
		return nil, errInvalidMetadataResponse
	}
	objects, err := readMetadataObjects(ctx, api)
	if err != nil {
		return nil, err
	}
	for i := range objects {
		if strings.EqualFold(objects[i].Id, id) {
			return &objects[i], nil
		}
	}
	return nil, nil
}

func listMetadataFields(ctx context.Context, api graphql.Client, filter client.FieldFilter) ([]client.FieldMetadata, error) {
	result := []client.FieldMetadata{}
	paging := client.CursorPaging{First: nullable.NewNullableWithValue(100)}
	seen, cursors := map[string]bool{}, map[string]bool{}
	for {
		response, err := client.ListMetadataFields(ctx, metadataQueryClient{api}, filter, paging)
		if err != nil {
			return nil, err
		}
		for _, edge := range response.Fields.Edges {
			key := strings.ToLower(edge.Node.Id)
			if seen[key] {
				return nil, errInvalidMetadataResponse
			}
			seen[key] = true
			result = append(result, edge.Node)
		}
		if !response.Fields.PageInfo.HasNextPage.GetOrEmpty() {
			return result, nil
		}
		cursor := response.Fields.PageInfo.EndCursor.GetOrEmpty()
		if cursors[cursor] {
			return nil, errInvalidMetadataResponse
		}
		cursors[cursor] = true
		paging.After = nullable.NewNullableWithValue(cursor)
	}
}

func readMetadataFields(ctx context.Context, api graphql.Client, objectID string) ([]client.FieldMetadata, error) {
	if !metadataUUID(objectID) {
		return nil, errInvalidMetadataResponse
	}
	filter := client.FieldFilter{ObjectMetadataId: nullable.NewNullableWithValue(client.UUIDFilterComparison{Eq: nullable.NewNullableWithValue(objectID)})}
	fields, err := listMetadataFields(ctx, api, filter)
	if err != nil {
		return nil, err
	}
	for _, field := range fields {
		if !strings.EqualFold(field.ObjectMetadataId, objectID) {
			return nil, errInvalidMetadataResponse
		}
	}
	return fields, nil
}

func readMetadataField(ctx context.Context, api graphql.Client, id string) (*client.FieldMetadata, error) {
	if !metadataUUID(id) {
		return nil, errInvalidMetadataResponse
	}
	filter := client.FieldFilter{Id: nullable.NewNullableWithValue(client.UUIDFilterComparison{Eq: nullable.NewNullableWithValue(id)})}
	fields, err := listMetadataFields(ctx, api, filter)
	if err != nil {
		return nil, err
	}
	if len(fields) > 1 || len(fields) == 1 && !strings.EqualFold(fields[0].Id, id) {
		return nil, errInvalidMetadataResponse
	}
	if len(fields) == 0 {
		return nil, nil
	}
	return &fields[0], nil
}

// The workspace Custom application owns user-created metadata. Built-in objects
// can be editable and non-system, so those flags alone do not establish ownership.
func metadataCustomApplication(ctx context.Context, api graphql.Client, identity client.Identity) (string, error) {
	workspace, err := readCurrentWorkspace(ctx, api, identity)
	if err != nil {
		return "", err
	}
	if !metadataUUID(workspace.WorkspaceCustomApplicationId) {
		return "", errInvalidMetadataResponse
	}
	return workspace.WorkspaceCustomApplicationId, nil
}

func guardMetadataObject(object *client.ObjectMetadata, customApplicationID string) error {
	if object == nil || !metadataUUID(customApplicationID) {
		return errInvalidMetadataResponse
	}
	if !strings.EqualFold(object.ApplicationId, customApplicationID) || object.IsSystem || object.IsRemote || object.Writability != client.MetadataWritabilityOpen {
		return errUnsafeMetadata
	}
	return nil
}

func supportedMetadataFieldType(kind string) bool {
	return slices.Contains([]string{"TEXT", "NUMBER", "BOOLEAN", "DATE", "DATE_TIME"}, kind)
}

func guardMetadataField(field *client.FieldMetadata, object *client.ObjectMetadata, customApplicationID string) error {
	if field == nil || object == nil || !metadataUUID(customApplicationID) || !strings.EqualFold(field.ObjectMetadataId, object.Id) {
		return errInvalidMetadataResponse
	}
	if !strings.EqualFold(field.ApplicationId, customApplicationID) || field.IsSystem.GetOrEmpty() || !supportedMetadataFieldType(string(field.Type)) || !field.IsNullable.GetOrEmpty() || object.IsSystem || object.IsRemote || object.Writability != client.MetadataWritabilityOpen || field.Writability.GetOrEmpty() != client.MetadataWritabilityOpen || strings.EqualFold(object.LabelIdentifierFieldMetadataId.GetOrEmpty(), field.Id) {
		return errUnsafeMetadata
	}
	return nil
}
