// SPDX-License-Identifier: MPL-2.0

package client_test

import (
	"testing"

	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
	"github.com/oapi-codegen/nullable"
)

// Sparse metadata writes must not send null for unrelated schema properties.
// In particular, updating a label must not clear options, defaults, or settings.
func TestMetadataSparseInputs(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000501"
	t.Run("create_object", func(t *testing.T) {
		c := newMemoryClient(t, "CreateMetadataObject", `{"createOneObject":{"id":"`+id+`"}}`)
		_, err := client.CreateMetadataObject(t.Context(), c, client.CreateOneObjectInput{Object: client.CreateObjectInput{NameSingular: "subscription", NamePlural: "subscriptions", LabelSingular: "Subscription", LabelPlural: "Subscriptions", Description: nullable.NewNullNullable[string](), Icon: nullable.NewNullableWithValue(""), IsRemote: nullable.NewNullableWithValue(false), IsLabelSyncedWithName: nullable.NewNullableWithValue(false)}})
		if err != nil {
			t.Fatal(err)
		}
		assertJSON(t, c.variables, `{"input":{"object":{"nameSingular":"subscription","namePlural":"subscriptions","labelSingular":"Subscription","labelPlural":"Subscriptions","description":null,"icon":"","isRemote":false,"isLabelSyncedWithName":false}}}`)
	})
	t.Run("update_object", func(t *testing.T) {
		c := newMemoryClient(t, "UpdateMetadataObject", `{"updateOneObject":{"id":"`+id+`"}}`)
		_, err := client.UpdateMetadataObject(t.Context(), c, client.UpdateOneObjectInput{Id: id, Update: client.UpdateObjectPayload{LabelSingular: nullable.NewNullableWithValue("Plan"), Description: nullable.NewNullNullable[string](), IsLabelSyncedWithName: nullable.NewNullableWithValue(false)}})
		if err != nil {
			t.Fatal(err)
		}
		assertJSON(t, c.variables, `{"input":{"id":"`+id+`","update":{"labelSingular":"Plan","description":null,"isLabelSyncedWithName":false}}}`)
	})
	t.Run("create_field", func(t *testing.T) {
		c := newMemoryClient(t, "CreateMetadataField", `{"createOneField":{"id":"`+id+`"}}`)
		_, err := client.CreateMetadataField(t.Context(), c, client.CreateOneFieldMetadataInput{Field: client.CreateFieldInput{Name: "tier", Label: "Tier", ObjectMetadataId: id, Type: client.FieldMetadataType("TEXT"), IsNullable: nullable.NewNullableWithValue(true), IsSystem: nullable.NewNullableWithValue(false), IsActive: nullable.NewNullableWithValue(true), IsLabelSyncedWithName: nullable.NewNullableWithValue(false)}})
		if err != nil {
			t.Fatal(err)
		}
		assertJSON(t, c.variables, `{"input":{"field":{"name":"tier","label":"Tier","objectMetadataId":"`+id+`","type":"TEXT","isNullable":true,"isSystem":false,"isActive":true,"isLabelSyncedWithName":false}}}`)
	})
	t.Run("update_field", func(t *testing.T) {
		c := newMemoryClient(t, "UpdateMetadataField", `{"updateOneField":{"id":"`+id+`"}}`)
		_, err := client.UpdateMetadataField(t.Context(), c, client.UpdateOneFieldMetadataInput{Id: id, Update: client.UpdateFieldInput{Label: nullable.NewNullableWithValue("Plan tier"), Description: nullable.NewNullNullable[string](), Icon: nullable.NewNullableWithValue("")}})
		if err != nil {
			t.Fatal(err)
		}
		assertJSON(t, c.variables, `{"input":{"id":"`+id+`","update":{"label":"Plan tier","description":null,"icon":""}}}`)
	})
	t.Run("paging_and_filter", func(t *testing.T) {
		c := newMemoryClient(t, "ListMetadataFields", `{"fields":{"edges":[],"pageInfo":{"hasNextPage":false,"endCursor":null}}}`)
		_, err := client.ListMetadataFields(t.Context(), c, client.FieldFilter{ObjectMetadataId: nullable.NewNullableWithValue(client.UUIDFilterComparison{Eq: nullable.NewNullableWithValue(id)})}, client.CursorPaging{First: nullable.NewNullableWithValue(100), After: nullable.NewNullableWithValue("opaque-cursor")})
		if err != nil {
			t.Fatal(err)
		}
		assertJSON(t, c.variables, `{"filter":{"objectMetadataId":{"eq":"`+id+`"}},"paging":{"first":100,"after":"opaque-cursor"}}`)
	})
}
