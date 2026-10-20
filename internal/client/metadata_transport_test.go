// SPDX-License-Identifier: MPL-2.0

package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/oapi-codegen/nullable"
)

// Exercise generated operations through the actual session transport, not only
// the in-memory GraphQL fixtures. Operation names must match their root fields.
func TestMetadataSessionTransport(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000501"
	roots := map[string]string{
		"Objects": "objects", "Fields": "fields",
		"CreateOneObject": "createOneObject", "UpdateOneObject": "updateOneObject", "DeleteOneObject": "deleteOneObject",
		"CreateOneField": "createOneField", "UpdateOneField": "updateOneField", "DeleteOneField": "deleteOneField",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request graphql.Request
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		root, ok := roots[request.OpName]
		if !ok {
			t.Errorf("unexpected operation %q", request.OpName)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var value any = map[string]any{"id": id}
		if root == "objects" || root == "fields" {
			value = map[string]any{"edges": []any{}, "pageInfo": map[string]any{"hasNextPage": false, "endCursor": nil}}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{root: value}}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	wire, err := newSessionWireClient(server.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	paging := CursorPaging{First: nullable.NewNullableWithValue(100)}
	tests := map[string]func() error{
		"objects": func() error { _, err := Objects(t.Context(), wire, paging); return err },
		"fields":  func() error { _, err := Fields(t.Context(), wire, FieldFilter{}, paging); return err },
		"create_object": func() error {
			_, err := CreateOneObject(t.Context(), wire, CreateOneObjectInput{Object: CreateObjectInput{NameSingular: "subscription", NamePlural: "subscriptions", LabelSingular: "Subscription", LabelPlural: "Subscriptions"}})
			return err
		},
		"update_object": func() error { _, err := UpdateOneObject(t.Context(), wire, UpdateOneObjectInput{Id: id}); return err },
		"delete_object": func() error { _, err := DeleteOneObject(t.Context(), wire, DeleteOneObjectInput{Id: id}); return err },
		"create_field": func() error {
			_, err := CreateOneField(t.Context(), wire, CreateOneFieldMetadataInput{Field: CreateFieldInput{ObjectMetadataId: id, Name: "tier", Label: "Tier", Type: FieldMetadataType("TEXT")}})
			return err
		},
		"update_field": func() error {
			_, err := UpdateOneField(t.Context(), wire, UpdateOneFieldMetadataInput{Id: id})
			return err
		},
		"delete_field": func() error { _, err := DeleteOneField(t.Context(), wire, DeleteOneFieldInput{Id: id}); return err },
	}
	for name, run := range tests {
		t.Run(name, func(t *testing.T) {
			if err := run(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
