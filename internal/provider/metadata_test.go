// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/google/uuid"
)

const (
	metadataTestObjectID         = "00000000-0000-4000-8000-000000000501"
	metadataTestFieldID          = "00000000-0000-4000-8000-000000000502"
	metadataTestStandardObjectID = "00000000-0000-4000-8000-000000000503"
	metadataTestStandardFieldID  = "00000000-0000-4000-8000-000000000504"
	metadataTestCustomApp        = "00000000-0000-4000-8000-000000000505"
	metadataTestStandardApp      = "00000000-0000-4000-8000-000000000506"
)

func metadataObjectFixture(id, name string) map[string]any {
	return map[string]any{"id": id, "applicationId": metadataTestCustomApp, "universalIdentifier": id, "nameSingular": name, "namePlural": name + "s", "labelSingular": "Subscription", "labelPlural": "Subscriptions", "description": nil, "icon": nil, "isActive": true, "isSystem": false, "isRemote": false, "isLabelSyncedWithName": false, "writability": "OPEN", "labelIdentifierFieldMetadataId": nil}
}

func metadataFieldFixture(id, objectID, name string) map[string]any {
	return map[string]any{"id": id, "applicationId": metadataTestCustomApp, "universalIdentifier": id, "objectMetadataId": objectID, "name": name, "label": "Tier", "type": "TEXT", "description": nil, "icon": nil, "isActive": true, "isSystem": false, "isLabelSyncedWithName": false, "isNullable": true, "writability": "OPEN"}
}

// Concrete Metadata wire fixture shared by object/field lifecycle and lookup
// tests. Writes take effect before failAfter, modelling ambiguous responses.
type metadataMock struct {
	workspace       *workspaceMock
	objects, fields []map[string]any
	calls           []string
	inputs          map[string]json.RawMessage
	fail, failAfter map[string]error
	change          func(string, map[string]any)
	pageSize        int
}

func newMetadataMock() *metadataMock {
	workspace := newWorkspaceMock()
	workspace.workspace["workspaceCustomApplicationId"] = metadataTestCustomApp
	standard := metadataObjectFixture(metadataTestStandardObjectID, "company")
	standard["applicationId"] = metadataTestStandardApp
	standard["labelIdentifierFieldMetadataId"] = metadataTestStandardFieldID
	field := metadataFieldFixture(metadataTestStandardFieldID, metadataTestStandardObjectID, "name")
	field["applicationId"] = metadataTestStandardApp
	return &metadataMock{workspace: workspace, objects: []map[string]any{standard, metadataObjectFixture(metadataTestObjectID, "subscription")}, fields: []map[string]any{field, metadataFieldFixture(metadataTestFieldID, metadataTestObjectID, "tier")}, inputs: map[string]json.RawMessage{}, fail: map[string]error{}, failAfter: map[string]error{}, pageSize: 100}
}

func (m *metadataMock) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	m.calls = append(m.calls, req.OpName)
	raw, err := json.Marshal(req.Variables)
	if err != nil {
		return err
	}
	m.inputs[req.OpName] = raw
	if err := m.fail[req.OpName]; err != nil {
		return err
	}
	if req.OpName == "CurrentUser" || req.OpName == "CurrentWorkspace" {
		return m.workspace.MakeRequest(ctx, req, resp)
	}
	var vars struct {
		Paging struct {
			First int
			After string
		}
		Filter struct {
			Id               struct{ Eq string }
			ObjectMetadataId struct{ Eq string }
		}
		Input struct {
			Id                    string
			Object, Field, Update map[string]any
		}
	}
	if err := json.Unmarshal(raw, &vars); err != nil {
		return err
	}
	data := map[string]any{}
	switch req.OpName {
	case "ListMetadataObjects", "ListMetadataFields":
		root, items := "objects", m.objects
		if req.OpName == "ListMetadataFields" {
			root, items = "fields", []map[string]any{}
			for _, field := range m.fields {
				if vars.Filter.Id.Eq != "" && !strings.EqualFold(field["id"].(string), vars.Filter.Id.Eq) {
					continue
				}
				if vars.Filter.ObjectMetadataId.Eq != "" && !strings.EqualFold(field["objectMetadataId"].(string), vars.Filter.ObjectMetadataId.Eq) {
					continue
				}
				items = append(items, field)
			}
		}
		start := 0
		if vars.Paging.After != "" {
			start, err = strconv.Atoi(vars.Paging.After)
			if err != nil {
				return err
			}
		}
		if start < 0 || start > len(items) {
			return fmt.Errorf("invalid fixture cursor")
		}
		size := min(m.pageSize, vars.Paging.First)
		end := min(start+size, len(items))
		edges := []map[string]any{}
		for _, item := range items[start:end] {
			edges = append(edges, map[string]any{"node": item})
		}
		var cursor any
		if end > 0 {
			cursor = strconv.Itoa(end)
		}
		data[root] = map[string]any{"edges": edges, "pageInfo": map[string]any{"hasNextPage": end < len(items), "endCursor": cursor}}
	case "CreateMetadataObject":
		object := metadataObjectFixture(uuid.NewString(), vars.Input.Object["nameSingular"].(string))
		for k, v := range vars.Input.Object {
			object[k] = v
		}
		m.objects = append(m.objects, object)
		data["createOneObject"] = map[string]any{"id": object["id"]}
	case "CreateMetadataField":
		field := metadataFieldFixture(uuid.NewString(), vars.Input.Field["objectMetadataId"].(string), vars.Input.Field["name"].(string))
		for k, v := range vars.Input.Field {
			field[k] = v
		}
		m.fields = append(m.fields, field)
		data["createOneField"] = map[string]any{"id": field["id"]}
	case "UpdateMetadataObject", "UpdateMetadataField":
		root, items := "updateOneObject", m.objects
		if req.OpName == "UpdateMetadataField" {
			root, items = "updateOneField", m.fields
		}
		for _, item := range items {
			if strings.EqualFold(item["id"].(string), vars.Input.Id) {
				for k, v := range vars.Input.Update {
					item[k] = v
				}
				data[root] = map[string]any{"id": item["id"]}
			}
		}
	case "DeleteMetadataObject":
		data["deleteOneObject"] = map[string]any{"id": vars.Input.Id}
		m.objects = slices.DeleteFunc(m.objects, func(item map[string]any) bool { return strings.EqualFold(item["id"].(string), vars.Input.Id) })
		m.fields = slices.DeleteFunc(m.fields, func(item map[string]any) bool {
			return strings.EqualFold(item["objectMetadataId"].(string), vars.Input.Id)
		})
	case "DeleteMetadataField":
		data["deleteOneField"] = map[string]any{"id": vars.Input.Id}
		m.fields = slices.DeleteFunc(m.fields, func(item map[string]any) bool { return strings.EqualFold(item["id"].(string), vars.Input.Id) })
	default:
		return fmt.Errorf("unexpected metadata fixture operation %s", req.OpName)
	}
	if m.change != nil {
		m.change(req.OpName, data)
	}
	if err := m.failAfter[req.OpName]; err != nil {
		return err
	}
	result, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return json.Unmarshal(result, resp.Data)
}

func TestMetadataPaginationAndScope(t *testing.T) {
	mock := newMetadataMock()
	mock.pageSize = 1
	objects, err := readMetadataObjects(t.Context(), mock)
	if err != nil || len(objects) != 2 || slices.Index(mock.calls, "ListMetadataObjects") < 0 {
		t.Fatalf("paginated object read: %v", err)
	}
	fields, err := readMetadataFields(t.Context(), mock, metadataTestObjectID)
	if err != nil || len(fields) != 1 || fields[0].Id != metadataTestFieldID {
		t.Fatalf("scoped field read: %v", err)
	}
	field, err := readMetadataField(t.Context(), mock, metadataTestFieldID)
	if err != nil || field == nil || field.ObjectMetadataId != metadataTestObjectID {
		t.Fatalf("native field lookup: %v", err)
	}
	missing, err := readMetadataField(t.Context(), mock, "00000000-0000-4000-8000-000000000599")
	if err != nil || missing != nil {
		t.Fatalf("missing field: %v", err)
	}
}
