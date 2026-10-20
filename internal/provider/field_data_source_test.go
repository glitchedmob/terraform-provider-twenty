// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"errors"
	"strings"
	"testing"

	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func fieldDataSourceConfig(t *testing.T, model fieldDataSourceModel) tfsdk.Config {
	t.Helper()
	var schema datasource.SchemaResponse
	NewFieldDataSource().Schema(t.Context(), datasource.SchemaRequest{}, &schema)
	state := tfsdk.State{Schema: schema.Schema}
	if d := state.Set(t.Context(), &model); d.HasError() {
		t.Fatal(d)
	}
	return tfsdk.Config(state)
}
func fieldDataSourceRead(t *testing.T, mock *metadataMock, model fieldDataSourceModel) datasource.ReadResponse {
	t.Helper()
	config := fieldDataSourceConfig(t, model)
	resp := datasource.ReadResponse{State: tfsdk.State{Schema: config.Schema}}
	(&fieldDataSource{client: mock}).Read(t.Context(), datasource.ReadRequest{Config: config}, &resp)
	fieldNoMutation(t, mock)
	return resp
}
func TestFieldDataSourceSchemaConfigure(t *testing.T) {
	d := NewFieldDataSource().(*fieldDataSource)
	var md datasource.MetadataResponse
	d.Metadata(t.Context(), datasource.MetadataRequest{ProviderTypeName: "twenty"}, &md)
	var schema datasource.SchemaResponse
	d.Schema(t.Context(), datasource.SchemaRequest{}, &schema)
	if md.TypeName != "twenty_field" || len(schema.Schema.Attributes) != 11 {
		t.Fatal("unexpected field data source schema")
	}
	for name, attr := range schema.Schema.Attributes {
		if attr.GetMarkdownDescription() == "" || attr.IsRequired() {
			t.Fatal("invalid schema", name)
		}
		if name != "field_id" && !attr.IsComputed() {
			t.Fatal("missing computed attribute", name)
		}
	}
	data := &ClientData{Client: &client.Session{}}
	var configure datasource.ConfigureResponse
	d.Configure(t.Context(), datasource.ConfigureRequest{ProviderData: data}, &configure)
	if configure.Diagnostics.HasError() || d.client != data.Client.Client() {
		t.Fatal("client not shared")
	}
	d.Configure(t.Context(), datasource.ConfigureRequest{}, &configure)
	if d.client != nil {
		t.Fatal("nil configure did not reset client")
	}
	for _, data := range []any{"private-secret", (*ClientData)(nil), &ClientData{}} {
		var resp datasource.ConfigureResponse
		d.Configure(t.Context(), datasource.ConfigureRequest{ProviderData: data}, &resp)
		if !resp.Diagnostics.HasError() || strings.Contains(resp.Diagnostics[0].Detail(), "private-secret") {
			t.Fatal("unsafe configure")
		}
	}
}
func TestFieldDataSourceSelectors(t *testing.T) {
	for name, tc := range map[string]struct {
		id, object, name types.String
		invalid          bool
	}{
		"UUID":            {id: types.StringValue(metadataTestFieldID)},
		"name":            {object: types.StringValue(metadataTestObjectID), name: types.StringValue("tier")},
		"missing":         {invalid: true},
		"object only":     {object: types.StringValue(metadataTestObjectID), invalid: true},
		"name only":       {name: types.StringValue("tier"), invalid: true},
		"UUID and name":   {id: types.StringValue(metadataTestFieldID), name: types.StringValue("tier"), invalid: true},
		"UUID and object": {id: types.StringValue(metadataTestFieldID), object: types.StringValue(metadataTestObjectID), invalid: true},
		"all":             {id: types.StringValue(metadataTestFieldID), object: types.StringValue(metadataTestObjectID), name: types.StringValue("tier"), invalid: true},
		"bad UUID":        {id: types.StringValue("bad"), invalid: true},
		"zero UUID":       {id: types.StringValue("00000000-0000-0000-0000-000000000000"), invalid: true},
		"bad object":      {object: types.StringValue("bad"), name: types.StringValue("tier"), invalid: true},
		"blank name":      {object: types.StringValue(metadataTestObjectID), name: types.StringValue("\t "), invalid: true},
		"unknown UUID":    {id: types.StringUnknown()},
		"unknown object":  {object: types.StringUnknown(), name: types.StringValue("tier")},
		"unknown name":    {object: types.StringValue(metadataTestObjectID), name: types.StringUnknown()},
	} {
		t.Run(name, func(t *testing.T) {
			model := fieldDataSourceModel{FieldID: tc.id, ObjectID: tc.object, Name: tc.name}
			d := &fieldDataSource{}
			var validate datasource.ValidateConfigResponse
			d.ValidateConfig(t.Context(), datasource.ValidateConfigRequest{Config: fieldDataSourceConfig(t, model)}, &validate)
			if validate.Diagnostics.HasError() != tc.invalid {
				t.Fatal("unexpected selector validation", validate.Diagnostics)
			}
			if tc.invalid || tc.id.IsUnknown() || tc.object.IsUnknown() || tc.name.IsUnknown() {
				mock := newMetadataMock()
				resp := fieldDataSourceRead(t, mock, model)
				if !resp.Diagnostics.HasError() || len(mock.calls) != 0 {
					t.Fatal("invalid/unknown selector reached API")
				}
			}
		})
	}
}
func TestFieldDataSourceRead(t *testing.T) {
	for _, byID := range []bool{true, false} {
		t.Run(map[bool]string{true: "UUID", false: "name"}[byID], func(t *testing.T) {
			mock := newMetadataMock()
			mock.pageSize = 1
			// A same-name field in a different object must not cause ambiguity.
			mock.fields = append([]map[string]any{metadataFieldFixture("00000000-0000-4000-8000-000000000599", metadataTestStandardObjectID, "tier")}, mock.fields...)
			model := fieldDataSourceModel{}
			if byID {
				model.FieldID = types.StringValue(metadataTestFieldID)
			} else {
				model.ObjectID = types.StringValue(metadataTestObjectID)
				model.Name = types.StringValue("tier")
			}
			resp := fieldDataSourceRead(t, mock, model)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			var got fieldDataSourceModel
			if d := resp.State.Get(t.Context(), &got); d.HasError() {
				t.Fatal(d)
			}
			if got.ID.ValueString() != metadataTestFieldID || got.ObjectID.ValueString() != metadataTestObjectID || got.Name.ValueString() != "tier" || got.Type.ValueString() != "TEXT" || got.IsSystem.ValueBool() || !got.IsActive.ValueBool() || got.ApplicationID.ValueString() != metadataTestCustomApp || !got.Description.IsNull() || !got.Icon.IsNull() {
				t.Fatal("incorrect field state", got)
			}
			if !got.FieldID.Equal(model.FieldID) {
				t.Fatal("field selector was not preserved")
			}
		})
	}
}
func TestFieldDataSourceStandardAndComplexFields(t *testing.T) {
	mock := newMetadataMock()
	mock.fields[0]["type"] = "FULL_NAME"
	mock.fields[0]["isSystem"] = true
	mock.fields[0]["isNullable"] = false
	mock.fields[0]["description"] = " Description as returned "
	mock.fields[0]["icon"] = ""
	// Read-only lookups must not impose resource ownership restrictions.
	mock.objects[0]["isRemote"] = true
	mock.objects[0]["isSystem"] = true
	resp := fieldDataSourceRead(t, mock, fieldDataSourceModel{ObjectID: types.StringValue(metadataTestStandardObjectID), Name: types.StringValue("name")})
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	var got fieldDataSourceModel
	_ = resp.State.Get(t.Context(), &got)
	if got.Type.ValueString() != "FULL_NAME" || !got.IsSystem.ValueBool() || got.Description.ValueString() != " Description as returned " || got.Icon.IsNull() || got.Icon.ValueString() != "" {
		t.Fatal("read-only data source changed/protected field values")
	}
}
func TestFieldDataSourcePaginationAndAmbiguity(t *testing.T) {
	mock := newMetadataMock()
	mock.pageSize = 1
	mock.fields = append(mock.fields, metadataFieldFixture("00000000-0000-4000-8000-000000000599", metadataTestObjectID, "secondField"))
	config := fieldDataSourceModel{ObjectID: types.StringValue(metadataTestObjectID), Name: types.StringValue("secondField")}
	resp := fieldDataSourceRead(t, mock, config)
	if resp.Diagnostics.HasError() {
		t.Fatal("did not find later page", resp.Diagnostics)
	}
	mock.fields = append(mock.fields, metadataFieldFixture("00000000-0000-4000-8000-000000000598", metadataTestObjectID, "secondField"))
	resp = fieldDataSourceRead(t, mock, config)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics[0].Detail(), "more than one") {
		t.Fatal("ambiguous name accepted", resp.Diagnostics)
	}
}
func TestFieldDataSourceMissingAndFailures(t *testing.T) {
	for name, change := range map[string]func(*metadataMock, *fieldDataSourceModel){
		"missing UUID": func(m *metadataMock, c *fieldDataSourceModel) {
			c.FieldID = types.StringValue("00000000-0000-4000-8000-000000000599")
			c.ObjectID = types.StringNull()
			c.Name = types.StringNull()
		},
		"missing name":        func(m *metadataMock, c *fieldDataSourceModel) { c.Name = types.StringValue("missing") },
		"case sensitive name": func(m *metadataMock, c *fieldDataSourceModel) { c.Name = types.StringValue("Tier") },
		"missing parent":      func(m *metadataMock, c *fieldDataSourceModel) { m.objects = m.objects[:1] },
		"field list failure": func(m *metadataMock, c *fieldDataSourceModel) {
			m.fail["Fields"] = errors.New("private-secret")
		},
		"parent list failure": func(m *metadataMock, c *fieldDataSourceModel) {
			m.fail["Objects"] = errors.New("private-secret")
		},
		"malformed field": func(m *metadataMock, c *fieldDataSourceModel) { delete(m.fields[1], "description") },
		"incomplete pagination": func(m *metadataMock, c *fieldDataSourceModel) {
			m.change = func(op string, data map[string]any) {
				if op == "Fields" {
					delete(data["fields"].(map[string]any), "pageInfo")
				}
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			mock := newMetadataMock()
			model := fieldDataSourceModel{ObjectID: types.StringValue(metadataTestObjectID), Name: types.StringValue("tier")}
			change(mock, &model)
			resp := fieldDataSourceRead(t, mock, model)
			if !resp.Diagnostics.HasError() {
				t.Fatal("invalid lookup accepted")
			}
			for _, d := range resp.Diagnostics {
				if strings.Contains(d.Detail(), "private-secret") {
					t.Fatal("leaked upstream error")
				}
			}
		})
	}
}
func TestFieldDataSourceUUIDCaseAndConfigSelectors(t *testing.T) {
	mock := newMetadataMock()
	id := "ABCDEFAB-ABCD-4ABC-8ABC-ABCDEFABCDEF"
	objectID := "ABCDEFAB-ABCD-4ABC-8ABC-ABCDEFABCDE0"
	mock.objects[1]["id"] = strings.ToLower(objectID)
	mock.fields[1]["id"] = strings.ToLower(id)
	mock.fields[1]["objectMetadataId"] = strings.ToLower(objectID)
	for _, model := range []fieldDataSourceModel{{FieldID: types.StringValue(id)}, {ObjectID: types.StringValue(objectID), Name: types.StringValue("tier")}} {
		resp := fieldDataSourceRead(t, mock, model)
		if resp.Diagnostics.HasError() {
			t.Fatal(resp.Diagnostics)
		}
		var got fieldDataSourceModel
		_ = resp.State.Get(t.Context(), &got)
		if !got.FieldID.Equal(model.FieldID) || !model.ObjectID.IsNull() && !got.ObjectID.Equal(model.ObjectID) {
			t.Fatal("configured selector spelling changed")
		}
	}
}
func TestFieldDataSourceUnconfigured(t *testing.T) {
	model := fieldDataSourceModel{FieldID: types.StringValue(metadataTestFieldID)}
	config := fieldDataSourceConfig(t, model)
	resp := datasource.ReadResponse{State: tfsdk.State{Schema: config.Schema}}
	(&fieldDataSource{}).Read(t.Context(), datasource.ReadRequest{Config: config}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("unconfigured lookup accepted")
	}
}
