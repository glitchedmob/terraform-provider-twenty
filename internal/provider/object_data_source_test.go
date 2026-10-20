// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func objectDataSourceConfig(t *testing.T, id, name types.String) tfsdk.Config {
	t.Helper()
	var schema datasource.SchemaResponse
	NewObjectDataSource().Schema(t.Context(), datasource.SchemaRequest{}, &schema)
	state := tfsdk.State{Schema: schema.Schema}
	model := objectDataSourceModel{ObjectID: id, NameSingular: name}
	if d := state.Set(t.Context(), &model); d.HasError() {
		t.Fatal(d)
	}
	return tfsdk.Config(state)
}
func objectDataSourceRead(t *testing.T, d *objectDataSource, id, name types.String) datasource.ReadResponse {
	t.Helper()
	config := objectDataSourceConfig(t, id, name)
	resp := datasource.ReadResponse{State: tfsdk.State{Schema: config.Schema}}
	d.Read(t.Context(), datasource.ReadRequest{Config: config}, &resp)
	return resp
}
func TestObjectDataSourceSchemaConfigure(t *testing.T) {
	d := NewObjectDataSource().(*objectDataSource)
	var metadata datasource.MetadataResponse
	d.Metadata(t.Context(), datasource.MetadataRequest{ProviderTypeName: "twenty"}, &metadata)
	var schema datasource.SchemaResponse
	d.Schema(t.Context(), datasource.SchemaRequest{}, &schema)
	if metadata.TypeName != "twenty_object" || len(schema.Schema.Attributes) != 11 {
		t.Fatal("unexpected data source schema")
	}
	for name, a := range schema.Schema.Attributes {
		if a.GetMarkdownDescription() == "" || a.IsRequired() {
			t.Fatalf("unexpected attribute %s", name)
		}
		if name == "object_id" {
			if !a.IsOptional() || a.IsComputed() {
				t.Fatal("object_id must be optional only")
			}
		} else if !a.IsComputed() {
			t.Fatalf("%s must be computed", name)
		}
	}
	if !schema.Schema.Attributes["name_singular"].IsOptional() {
		t.Fatal("name_singular must be optional+computed")
	}
	session := &client.Session{}
	var configured datasource.ConfigureResponse
	d.Configure(t.Context(), datasource.ConfigureRequest{ProviderData: &ClientData{Client: session}}, &configured)
	if configured.Diagnostics.HasError() || d.client != session.Client() {
		t.Fatal("must share authenticated client")
	}
	d.Configure(t.Context(), datasource.ConfigureRequest{}, &configured)
	if d.client != nil {
		t.Fatal("configure must reset")
	}
	for _, data := range []any{"private-secret", (*ClientData)(nil), &ClientData{}} {
		configured = datasource.ConfigureResponse{}
		d.Configure(t.Context(), datasource.ConfigureRequest{ProviderData: data}, &configured)
		if !configured.Diagnostics.HasError() || strings.Contains(configured.Diagnostics[0].Detail(), "private-secret") {
			t.Fatal("unsafe configure")
		}
	}
}
func TestObjectDataSourceValidation(t *testing.T) {
	for name, test := range map[string]struct {
		id, name types.String
		bad      bool
	}{
		"id":           {types.StringValue(metadataTestObjectID), types.StringNull(), false},
		"upper id":     {types.StringValue("ABCDEFAB-1234-4234-8234-123456789ABC"), types.StringNull(), false},
		"name":         {types.StringNull(), types.StringValue("subscription"), false},
		"exact name":   {types.StringNull(), types.StringValue(" Subscription "), false},
		"unknown id":   {types.StringUnknown(), types.StringNull(), false},
		"unknown name": {types.StringNull(), types.StringUnknown(), false},
		"neither":      {types.StringNull(), types.StringNull(), true},
		"both":         {types.StringValue(metadataTestObjectID), types.StringValue("subscription"), true},
		"both unknown": {types.StringUnknown(), types.StringUnknown(), true},
		"zero id":      {types.StringValue("00000000-0000-0000-0000-000000000000"), types.StringNull(), true},
		"malformed":    {types.StringValue("not-an-id"), types.StringNull(), true},
		"compact":      {types.StringValue(strings.ReplaceAll(metadataTestObjectID, "-", "")), types.StringNull(), true},
		"padded":       {types.StringValue(" " + metadataTestObjectID), types.StringNull(), true},
		"blank":        {types.StringNull(), types.StringValue(" \t\n"), true},
	} {
		t.Run(name, func(t *testing.T) {
			d := &objectDataSource{}
			config := objectDataSourceConfig(t, test.id, test.name)
			var validated datasource.ValidateConfigResponse
			d.ValidateConfig(t.Context(), datasource.ValidateConfigRequest{Config: config}, &validated)
			if validated.Diagnostics.HasError() != test.bad {
				t.Fatal("unexpected validation", validated.Diagnostics)
			}
			if test.id.IsUnknown() || test.name.IsUnknown() {
				if !validateObjectSelector(objectDataSourceModel{ObjectID: test.id, NameSingular: test.name}, true).HasError() {
					t.Fatal("unknown selector accepted at read")
				}
			}
		})
	}
}
func TestObjectDataSourceRead(t *testing.T) {
	for _, byID := range []bool{false, true} {
		for _, builtIn := range []bool{false, true} {
			mock := newMetadataMock()
			mock.pageSize = 1
			index := 1
			if builtIn {
				index = 0
			}
			object := mock.objects[index]
			object["description"], object["icon"] = " description as returned ", ""
			id, name := types.StringNull(), types.StringValue(object["nameSingular"].(string))
			if byID {
				// Exercise case-insensitive matching with alphabetic UUID digits.
				object["id"] = "abcdefab-1234-4234-8234-123456789abc"
				id, name = types.StringValue(strings.ToUpper(object["id"].(string))), types.StringNull()
			}
			resp := objectDataSourceRead(t, &objectDataSource{client: mock}, id, name)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			var model objectDataSourceModel
			if d := resp.State.Get(t.Context(), &model); d.HasError() {
				t.Fatal(d)
			}
			if !model.ObjectID.Equal(id) || model.ID.ValueString() != object["id"] || model.NameSingular.ValueString() != object["nameSingular"] || model.ApplicationID.ValueString() != object["applicationId"] {
				t.Fatal("selectors/metadata not preserved")
			}
			if model.Description.ValueString() != " description as returned " || model.Icon.IsNull() || model.Icon.ValueString() != "" || model.IsSystem.ValueBool() || !model.IsActive.ValueBool() {
				t.Fatal("computed values lost")
			}
			if slices.Contains(mock.calls, "CurrentWorkspace") {
				t.Fatal("read-only built-in lookup should not require ownership")
			}
		}
	}
	mock := newMetadataMock()
	resp := objectDataSourceRead(t, &objectDataSource{client: mock}, types.StringNull(), types.StringValue("subscription"))
	var model objectDataSourceModel
	if d := resp.State.Get(t.Context(), &model); d.HasError() {
		t.Fatal(d)
	}
	if resp.Diagnostics.HasError() || !model.Description.IsNull() || !model.Icon.IsNull() {
		t.Fatal("null strings lost")
	}
}
func TestObjectDataSourceLookupFailures(t *testing.T) {
	for _, failure := range []string{"absent", "case-sensitive", "exact whitespace", "duplicate names", "duplicate IDs", "malformed", "later page", "upstream", "unknown", "unconfigured", "cancelled"} {
		t.Run(failure, func(t *testing.T) {
			mock := newMetadataMock()
			d := &objectDataSource{client: mock}
			id, name := types.StringNull(), types.StringValue("subscription")
			switch failure {
			case "absent":
				name = types.StringValue("private-secret")
			case "case-sensitive":
				name = types.StringValue("Subscription")
			case "exact whitespace":
				name = types.StringValue(" subscription ")
			case "duplicate names":
				mock.objects = append(mock.objects, metadataObjectFixture("00000000-0000-4000-8000-000000000599", "subscription"))
				mock.pageSize = 1
			case "duplicate IDs":
				mock.objects = append(mock.objects, metadataObjectFixture(metadataTestObjectID, "other"))
				mock.pageSize = 1
			case "malformed":
				delete(mock.objects[1], "isActive")
			case "later page":
				mock.pageSize = 1
				mock.change = func(op string, data map[string]any) {
					if op == "ListMetadataObjects" {
						mock.fail[op] = errors.New("private-secret")
					}
				}
			case "upstream":
				mock.fail["ListMetadataObjects"] = errors.New("private-secret")
			case "unknown":
				name = types.StringUnknown()
			case "unconfigured":
				d.client = nil
			case "cancelled":
				mock.fail["ListMetadataObjects"] = context.Canceled
			}
			resp := objectDataSourceRead(t, d, id, name)
			if !resp.Diagnostics.HasError() || !resp.State.Raw.IsNull() {
				t.Fatal("lookup failure returned state", resp.Diagnostics)
			}
			for _, diagnostic := range resp.Diagnostics {
				if strings.Contains(diagnostic.Detail(), "private-secret") {
					t.Fatal("unsafe diagnostic")
				}
			}
			if failure == "duplicate names" && !strings.Contains(resp.Diagnostics[0].Detail(), "more than one") {
				t.Fatal("missing ambiguity diagnostic")
			}
			for _, call := range mock.calls {
				if call != "ListMetadataObjects" {
					t.Fatalf("data source performed unexpected operation %s", call)
				}
			}
		})
	}
}
