// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func fieldResourceState(t *testing.T, model fieldResourceModel) tfsdk.State {
	t.Helper()
	var schema resource.SchemaResponse
	NewFieldResource().Schema(t.Context(), resource.SchemaRequest{}, &schema)
	state := tfsdk.State{Schema: schema.Schema}
	if d := state.Set(t.Context(), &model); d.HasError() {
		t.Fatal(d)
	}
	return state
}
func fieldResourceTestModel() fieldResourceModel {
	return fieldResourceModel{ID: types.StringValue(metadataTestFieldID), ObjectID: types.StringValue(metadataTestObjectID), Name: types.StringValue("tier"), Type: types.StringValue("TEXT"), Label: types.StringValue("Tier"), Description: types.StringNull(), Icon: types.StringNull(), DeletionProtection: types.BoolValue(true)}
}
func mockFieldResource(mock *metadataMock) *fieldResource {
	return &fieldResource{client: mock, identity: workspaceTestIdentity(), mutationLock: &sync.Mutex{}}
}
func fieldNoMutation(t *testing.T, mock *metadataMock) {
	t.Helper()
	for _, op := range mock.calls {
		if strings.HasPrefix(op, "CreateMetadata") || strings.HasPrefix(op, "UpdateMetadata") || strings.HasPrefix(op, "DeleteMetadata") {
			t.Fatalf("unexpected write: %s", op)
		}
	}
}
func TestFieldResourceSchemaConfigureImport(t *testing.T) {
	r := NewFieldResource().(*fieldResource)
	var md resource.MetadataResponse
	r.Metadata(t.Context(), resource.MetadataRequest{ProviderTypeName: "twenty"}, &md)
	var schema resource.SchemaResponse
	r.Schema(t.Context(), resource.SchemaRequest{}, &schema)
	if md.TypeName != "twenty_field" || len(schema.Schema.Attributes) != 8 {
		t.Fatal("unexpected field schema")
	}
	for name, attr := range schema.Schema.Attributes {
		if attr.GetMarkdownDescription() == "" {
			t.Errorf("missing description %s", name)
		}
	}
	data := &ClientData{Client: &client.Session{}}
	var configured resource.ConfigureResponse
	r.Configure(t.Context(), resource.ConfigureRequest{ProviderData: data}, &configured)
	if configured.Diagnostics.HasError() || r.mutationLock != &data.MutationLock || r.client != data.Client.Client() {
		t.Fatal("must share client and lock")
	}
	r.Configure(t.Context(), resource.ConfigureRequest{}, &configured)
	if r.client != nil || r.mutationLock != nil || r.identity.WorkspaceID != "" {
		t.Fatal("nil configure did not reset")
	}
	for _, data := range []any{"private-secret", (*ClientData)(nil), &ClientData{}} {
		var resp resource.ConfigureResponse
		r.Configure(t.Context(), resource.ConfigureRequest{ProviderData: data}, &resp)
		if !resp.Diagnostics.HasError() || strings.Contains(resp.Diagnostics[0].Detail(), "private-secret") {
			t.Fatal("unsafe configure")
		}
	}
	for _, id := range []string{"bad", metadataTestObjectID + "/" + metadataTestFieldID, "00000000-0000-0000-0000-000000000000", " " + metadataTestFieldID} {
		resp := resource.ImportStateResponse{State: fieldResourceState(t, fieldResourceModel{})}
		r.ImportState(t.Context(), resource.ImportStateRequest{ID: id}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("invalid import accepted", id)
		}
	}
	resp := resource.ImportStateResponse{State: fieldResourceState(t, fieldResourceModel{})}
	id := "ABCDEFAB-ABCD-4ABC-8ABC-ABCDEFABCDEF"
	r.ImportState(t.Context(), resource.ImportStateRequest{ID: id}, &resp)
	var model fieldResourceModel
	resp.Diagnostics.Append(resp.State.Get(t.Context(), &model)...)
	if resp.Diagnostics.HasError() || model.ID.ValueString() != strings.ToLower(id) {
		t.Fatal(resp.Diagnostics)
	}
}
func TestFieldResourceValidation(t *testing.T) {
	for name, change := range map[string]func(*fieldResourceModel){
		"bad uuid":               func(m *fieldResourceModel) { m.ObjectID = types.StringValue("bad") },
		"zero uuid":              func(m *fieldResourceModel) { m.ObjectID = types.StringValue("00000000-0000-0000-0000-000000000000") },
		"name upper":             func(m *fieldResourceModel) { m.Name = types.StringValue("Tier") },
		"name underscore":        func(m *fieldResourceModel) { m.Name = types.StringValue("a_b") },
		"name unicode":           func(m *fieldResourceModel) { m.Name = types.StringValue("aé") },
		"name long":              func(m *fieldResourceModel) { m.Name = types.StringValue(strings.Repeat("a", 64)) },
		"complex type":           func(m *fieldResourceModel) { m.Type = types.StringValue("SELECT") },
		"blank label":            func(m *fieldResourceModel) { m.Label = types.StringValue("") },
		"label whitespace":       func(m *fieldResourceModel) { m.Label = types.StringValue("Bad  label") },
		"description whitespace": func(m *fieldResourceModel) { m.Description = types.StringValue(" bad") },
		"icon whitespace":        func(m *fieldResourceModel) { m.Icon = types.StringValue("bad\ticon") },
		"null required":          func(m *fieldResourceModel) { m.Type = types.StringNull() },
	} {
		t.Run(name, func(t *testing.T) {
			model := fieldResourceTestModel()
			change(&model)
			r := mockFieldResource(newMetadataMock())
			var resp resource.ValidateConfigResponse
			r.ValidateConfig(t.Context(), resource.ValidateConfigRequest{Config: tfsdk.Config(fieldResourceState(t, model))}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("invalid input accepted")
			}
		})
	}
	for _, kind := range []string{"TEXT", "NUMBER", "BOOLEAN", "DATE", "DATE_TIME"} {
		m := fieldResourceTestModel()
		m.Type = types.StringValue(kind)
		if d := m.validate(true); d.HasError() {
			t.Fatal(d)
		}
	}
	model := fieldResourceTestModel()
	model.ObjectID, model.Name, model.Type, model.Label, model.Description, model.Icon = types.StringUnknown(), types.StringUnknown(), types.StringUnknown(), types.StringUnknown(), types.StringUnknown(), types.StringUnknown()
	model.DeletionProtection = types.BoolUnknown()
	if d := model.validate(false); d.HasError() {
		t.Fatal("unknown dependencies must be deferred", d)
	}
	mock := newMetadataMock()
	r := mockFieldResource(mock)
	create := resource.CreateResponse{State: fieldResourceState(t, fieldResourceModel{})}
	r.Create(t.Context(), resource.CreateRequest{Plan: tfsdk.Plan(fieldResourceState(t, model))}, &create)
	if !create.Diagnostics.HasError() {
		t.Fatal("unknown mutation inputs accepted")
	}
	fieldNoMutation(t, mock)
}
func TestFieldResourceCRUD(t *testing.T) {
	for _, objectID := range []string{metadataTestObjectID, metadataTestStandardObjectID} {
		t.Run(objectID, func(t *testing.T) {
			mock := newMetadataMock()
			r := mockFieldResource(mock)
			model := fieldResourceTestModel()
			model.ID = types.StringUnknown()
			model.ObjectID = types.StringValue(objectID)
			model.Name = types.StringValue("customerTier")
			model.Description = types.StringValue("A description")
			model.Icon = types.StringValue("")
			create := resource.CreateResponse{State: fieldResourceState(t, fieldResourceModel{})}
			r.Create(t.Context(), resource.CreateRequest{Plan: tfsdk.Plan(fieldResourceState(t, model))}, &create)
			if create.Diagnostics.HasError() {
				t.Fatal(create.Diagnostics)
			}
			var state fieldResourceModel
			if d := create.State.Get(t.Context(), &state); d.HasError() {
				t.Fatal(d)
			}
			if !metadataUUID(state.ID.ValueString()) || state.ID.ValueString() == metadataTestFieldID {
				t.Fatal("missing native server-assigned UUID")
			}
			var input struct {
				Input struct{ Field map[string]any }
			}
			if err := json.Unmarshal(mock.inputs["CreateMetadataField"], &input); err != nil {
				t.Fatal(err)
			}
			f := input.Input.Field
			if f["isNullable"] != true || f["isActive"] != true || f["isSystem"] != false || f["isLabelSyncedWithName"] != false {
				t.Fatal("unsafe create flags", f)
			}
			for _, key := range []string{"id", "options", "settings", "isUnique", "relationCreationPayload"} {
				if _, ok := f[key]; ok {
					t.Fatal("unsupported create attribute", key)
				}
			}
			if v, ok := f["defaultValue"]; !ok || v != nil {
				t.Fatal("expected explicit null create default")
			}
			plan := state
			plan.Label = types.StringValue("Customer tier")
			plan.Description = types.StringNull()
			plan.Icon = types.StringNull()
			plan.DeletionProtection = types.BoolValue(false)
			before := fieldResourceState(t, state)
			update := resource.UpdateResponse{State: before}
			r.Update(t.Context(), resource.UpdateRequest{State: before, Plan: tfsdk.Plan(fieldResourceState(t, plan))}, &update)
			if update.Diagnostics.HasError() {
				t.Fatal(update.Diagnostics)
			}
			var updated fieldResourceModel
			if d := update.State.Get(t.Context(), &updated); d.HasError() {
				t.Fatal(d)
			}
			if !updated.Description.IsNull() || !updated.Icon.IsNull() || updated.DeletionProtection.ValueBool() {
				t.Fatal("nullable clears/protection not applied")
			}
			var updateInput struct {
				Input struct{ Update map[string]any }
			}
			_ = json.Unmarshal(mock.inputs["UpdateMetadataField"], &updateInput)
			if len(updateInput.Input.Update) != 4 || updateInput.Input.Update["description"] != nil || updateInput.Input.Update["icon"] != nil {
				t.Fatal("update must only manage label, strings, label sync", updateInput)
			}
			read := resource.ReadResponse{State: update.State}
			r.Read(t.Context(), resource.ReadRequest{State: update.State}, &read)
			if read.Diagnostics.HasError() {
				t.Fatal(read.Diagnostics)
			}
			del := resource.DeleteResponse{State: read.State}
			mock.calls = nil
			r.Delete(t.Context(), resource.DeleteRequest{State: read.State}, &del)
			if del.Diagnostics.HasError() || !del.State.Raw.IsNull() {
				t.Fatal("delete failed", del.Diagnostics)
			}
			if slices.Contains(mock.calls, "UpdateMetadataField") || !slices.Contains(mock.calls, "DeleteMetadataField") {
				t.Fatal("delete must not deactivate first")
			}
		})
	}
}
func TestFieldResourceImportReadAndAbsence(t *testing.T) {
	mock := newMetadataMock()
	r := mockFieldResource(mock)
	imported := fieldResourceState(t, fieldResourceModel{ID: types.StringValue(metadataTestFieldID)})
	resp := resource.ReadResponse{State: imported}
	r.Read(t.Context(), resource.ReadRequest{State: imported}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	var model fieldResourceModel
	_ = resp.State.Get(t.Context(), &model)
	if model.ObjectID.ValueString() != metadataTestObjectID || !model.DeletionProtection.ValueBool() {
		t.Fatal("import must derive parent and default protection")
	}
	mock.fields = mock.fields[:1]
	absent := resource.ReadResponse{State: resp.State}
	r.Read(t.Context(), resource.ReadRequest{State: resp.State}, &absent)
	if absent.Diagnostics.HasError() || !absent.State.Raw.IsNull() {
		t.Fatal("authoritative absence must remove state", absent.Diagnostics)
	}
}
func TestFieldResourceProtection(t *testing.T) {
	for name, change := range map[string]func(*metadataMock){
		"built in":       func(m *metadataMock) { m.fields[1]["applicationId"] = metadataTestStandardApp },
		"system field":   func(m *metadataMock) { m.fields[1]["isSystem"] = true },
		"required field": func(m *metadataMock) { m.fields[1]["isNullable"] = false },
		"complex field":  func(m *metadataMock) { m.fields[1]["type"] = "SELECT" },
		"closed field":   func(m *metadataMock) { m.fields[1]["writability"] = "FULLY_RESTRICTED" },
		"identifier":     func(m *metadataMock) { m.objects[1]["labelIdentifierFieldMetadataId"] = metadataTestFieldID },
		"system parent":  func(m *metadataMock) { m.objects[1]["isSystem"] = true },
		"remote parent":  func(m *metadataMock) { m.objects[1]["isRemote"] = true },
		"closed parent":  func(m *metadataMock) { m.objects[1]["writability"] = "FULLY_RESTRICTED" },
		"missing parent": func(m *metadataMock) { m.objects = m.objects[:1] },
	} {
		t.Run(name, func(t *testing.T) {
			for _, op := range []string{"read", "update", "delete"} {
				t.Run(op, func(t *testing.T) {
					mock := newMetadataMock()
					change(mock)
					r := mockFieldResource(mock)
					model := fieldResourceTestModel()
					model.DeletionProtection = types.BoolValue(false)
					state := fieldResourceState(t, model)
					switch op {
					case "read":
						resp := resource.ReadResponse{State: state}
						r.Read(t.Context(), resource.ReadRequest{State: state}, &resp)
						if !resp.Diagnostics.HasError() || resp.State.Raw.IsNull() {
							t.Fatal("protected read accepted")
						}
					case "update":
						resp := resource.UpdateResponse{State: state}
						r.Update(t.Context(), resource.UpdateRequest{State: state, Plan: tfsdk.Plan(state)}, &resp)
						if !resp.Diagnostics.HasError() {
							t.Fatal("protected update accepted")
						}
					case "delete":
						resp := resource.DeleteResponse{State: state}
						r.Delete(t.Context(), resource.DeleteRequest{State: state}, &resp)
						if !resp.Diagnostics.HasError() || resp.State.Raw.IsNull() {
							t.Fatal("protected delete accepted")
						}
					}
					fieldNoMutation(t, mock)
				})
			}
		})
	}
}
func TestFieldResourceCreatePreflight(t *testing.T) {
	for name, change := range map[string]func(*metadataMock){
		"name conflict": func(m *metadataMock) {
			m.pageSize = 1
			m.fields = append(m.fields, metadataFieldFixture("00000000-0000-4000-8000-000000000599", metadataTestObjectID, "other"))
		},
		"remote":   func(m *metadataMock) { m.objects[1]["isRemote"] = true },
		"system":   func(m *metadataMock) { m.objects[1]["isSystem"] = true },
		"missing":  func(m *metadataMock) { m.objects = m.objects[:1] },
		"identity": func(m *metadataMock) { m.workspace.workspace["id"] = metadataTestObjectID },
	} {
		t.Run(name, func(t *testing.T) {
			mock := newMetadataMock()
			change(mock)
			r := mockFieldResource(mock)
			model := fieldResourceTestModel()
			if name != "name conflict" {
				model.Name = types.StringValue("newField")
			}
			resp := resource.CreateResponse{State: fieldResourceState(t, fieldResourceModel{})}
			r.Create(t.Context(), resource.CreateRequest{Plan: tfsdk.Plan(fieldResourceState(t, model))}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("unsafe create preflight passed")
			}
			fieldNoMutation(t, mock)
		})
	}
}
func TestFieldResourceDeletionProtection(t *testing.T) {
	for _, protection := range []types.Bool{types.BoolValue(true), types.BoolNull(), types.BoolUnknown()} {
		mock := newMetadataMock()
		r := mockFieldResource(mock)
		model := fieldResourceTestModel()
		model.DeletionProtection = protection
		state := fieldResourceState(t, model)
		resp := resource.DeleteResponse{State: state}
		r.Delete(t.Context(), resource.DeleteRequest{State: state}, &resp)
		if !resp.Diagnostics.HasError() || resp.State.Raw.IsNull() || len(mock.calls) != 0 {
			t.Fatal("protection must refuse without any API calls")
		}
	}
}
func TestFieldResourceImmutableMismatch(t *testing.T) {
	for _, attr := range []string{"object", "name", "type", "id"} {
		t.Run(attr, func(t *testing.T) {
			mock := newMetadataMock()
			r := mockFieldResource(mock)
			model := fieldResourceTestModel()
			state := fieldResourceState(t, model)
			plan := model
			switch attr {
			case "object":
				plan.ObjectID = types.StringValue(metadataTestStandardObjectID)
			case "name":
				plan.Name = types.StringValue("other")
			case "type":
				plan.Type = types.StringValue("NUMBER")
			case "id":
				plan.ID = types.StringValue(metadataTestStandardFieldID)
			}
			resp := resource.UpdateResponse{State: state}
			r.Update(t.Context(), resource.UpdateRequest{State: state, Plan: tfsdk.Plan(fieldResourceState(t, plan))}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("replacement must not update")
			}
			fieldNoMutation(t, mock)
		})
	}
	for _, attr := range []string{"objectMetadataId", "name", "type"} {
		t.Run("fresh "+attr, func(t *testing.T) {
			for _, op := range []string{"update", "delete"} {
				mock := newMetadataMock()
				switch attr {
				case "objectMetadataId":
					mock.fields[1][attr] = metadataTestStandardObjectID
				case "name":
					mock.fields[1][attr] = "other"
				case "type":
					mock.fields[1][attr] = "NUMBER"
				}
				r := mockFieldResource(mock)
				model := fieldResourceTestModel()
				model.DeletionProtection = types.BoolValue(false)
				state := fieldResourceState(t, model)
				if op == "update" {
					resp := resource.UpdateResponse{State: state}
					r.Update(t.Context(), resource.UpdateRequest{State: state, Plan: tfsdk.Plan(state)}, &resp)
					if !resp.Diagnostics.HasError() {
						t.Fatal("fresh mismatch updated")
					}
				} else {
					resp := resource.DeleteResponse{State: state}
					r.Delete(t.Context(), resource.DeleteRequest{State: state}, &resp)
					if !resp.Diagnostics.HasError() {
						t.Fatal("fresh mismatch deleted")
					}
				}
				fieldNoMutation(t, mock)
			}
		})
	}
}
func TestFieldResourceAmbiguousCreateNeverAdopts(t *testing.T) {
	for _, mode := range []string{"error after write", "no id"} {
		t.Run(mode, func(t *testing.T) {
			mock := newMetadataMock()
			if mode == "error after write" {
				mock.failAfter["CreateMetadataField"] = errors.New("private-secret")
			} else {
				mock.change = func(op string, data map[string]any) {
					if op == "CreateMetadataField" {
						data["createOneField"] = map[string]any{}
					}
				}
			}
			r := mockFieldResource(mock)
			model := fieldResourceTestModel()
			model.ID = types.StringUnknown()
			model.Name = types.StringValue("createdField")
			resp := resource.CreateResponse{State: fieldResourceState(t, fieldResourceModel{})}
			r.Create(t.Context(), resource.CreateRequest{Plan: tfsdk.Plan(fieldResourceState(t, model))}, &resp)
			if !resp.Diagnostics.HasError() || len(mock.fields) != 3 {
				t.Fatal("fixture must create despite ambiguity")
			}
			details := ""
			for _, d := range resp.Diagnostics {
				details += d.Detail()
			}
			if strings.Contains(details, "private-secret") || !strings.Contains(details, "explicitly import") {
				t.Fatal("unsafe recovery diagnostic", details)
			}
			if !resp.State.Raw.IsNull() && resp.State.Raw.IsKnown() {
				var state fieldResourceModel
				_ = resp.State.Get(t.Context(), &state)
				if metadataUUID(state.ID.ValueString()) {
					t.Fatal("ambiguous create adopted field")
				}
			}
			// Only the scoped conflict preflight reads fields. There is no name adoption read.
			count := 0
			for _, op := range mock.calls {
				if op == "ListMetadataFields" {
					count++
				}
			}
			if count != 1 {
				t.Fatal("unexpected adoption lookup", mock.calls)
			}
		})
	}
}
func TestFieldResourcePostWriteFailuresRetainID(t *testing.T) {
	for _, op := range []string{"create", "update", "delete"} {
		for _, failure := range []string{"read error", "mismatch", "mutation error"} {
			t.Run(op+" "+failure, func(t *testing.T) {
				mock := newMetadataMock()
				r := mockFieldResource(mock)
				model := fieldResourceTestModel()
				model.DeletionProtection = types.BoolValue(false)
				mutation := map[string]string{"create": "CreateMetadataField", "update": "UpdateMetadataField", "delete": "DeleteMetadataField"}[op]
				if failure == "mutation error" {
					mock.failAfter[mutation] = errors.New("private-secret")
				}
				mock.change = func(current string, data map[string]any) {
					if current != mutation {
						return
					}
					if failure == "read error" {
						mock.fail["ListMetadataFields"] = errors.New("private-secret")
					}
					if failure == "mismatch" {
						switch op {
						case "create", "update":
							mock.fields[len(mock.fields)-1]["label"] = "Wrong label"
						case "delete":
							mock.fields = append(mock.fields, metadataFieldFixture(metadataTestFieldID, metadataTestObjectID, "tier"))
						}
					}
				}
				state := fieldResourceState(t, model)
				var result tfsdk.State
				hasError := false
				switch op {
				case "create":
					model.ID = types.StringUnknown()
					model.Name = types.StringValue("newField")
					resp := resource.CreateResponse{State: fieldResourceState(t, fieldResourceModel{})}
					r.Create(t.Context(), resource.CreateRequest{Plan: tfsdk.Plan(fieldResourceState(t, model))}, &resp)
					result = resp.State
					hasError = resp.Diagnostics.HasError()
					for _, d := range resp.Diagnostics {
						if strings.Contains(d.Detail(), "private-secret") {
							t.Fatal("leaked upstream error")
						}
					}
				case "update":
					model.Label = types.StringValue("New label")
					resp := resource.UpdateResponse{State: state}
					r.Update(t.Context(), resource.UpdateRequest{State: state, Plan: tfsdk.Plan(fieldResourceState(t, model))}, &resp)
					result = resp.State
					hasError = resp.Diagnostics.HasError()
				case "delete":
					resp := resource.DeleteResponse{State: state}
					r.Delete(t.Context(), resource.DeleteRequest{State: state}, &resp)
					result = resp.State
					hasError = resp.Diagnostics.HasError()
				}
				if !hasError {
					t.Fatal("post-write failure not reported")
				}
				if op == "create" && failure == "mutation error" {
					return
				}
				if result.Raw.IsNull() {
					t.Fatal("partial write lost state")
				}
				var got fieldResourceModel
				if d := result.Get(t.Context(), &got); d.HasError() {
					t.Fatal(d)
				}
				if !metadataUUID(got.ID.ValueString()) {
					t.Fatal("partial write lost native UUID")
				}
			})
		}
	}
}
func TestFieldResourceIncompleteReadRetainsState(t *testing.T) {
	mock := newMetadataMock()
	mock.change = func(op string, data map[string]any) {
		if op == "ListMetadataFields" {
			delete(data["fields"].(map[string]any), "pageInfo")
		}
	}
	r := mockFieldResource(mock)
	state := fieldResourceState(t, fieldResourceTestModel())
	resp := resource.ReadResponse{State: state}
	r.Read(t.Context(), resource.ReadRequest{State: state}, &resp)
	if !resp.Diagnostics.HasError() || !resp.State.Raw.Equal(state.Raw) {
		t.Fatal("incomplete response removed state")
	}
}
func TestFieldResourceCancellationAndIdentity(t *testing.T) {
	for _, op := range []string{"create", "update", "delete"} {
		for _, mode := range []string{"cancel", "identity"} {
			t.Run(op+" "+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				mock := newMetadataMock()
				if mode == "identity" {
					mock.workspace.workspace["id"] = metadataTestObjectID
				} else {
					mock.change = func(current string, _ map[string]any) {
						if current == "ListMetadataFields" {
							cancel()
						}
					}
				}
				r := mockFieldResource(mock)
				model := fieldResourceTestModel()
				model.DeletionProtection = types.BoolValue(false)
				state := fieldResourceState(t, model)
				switch op {
				case "create":
					model.Name = types.StringValue("newField")
					resp := resource.CreateResponse{State: fieldResourceState(t, fieldResourceModel{})}
					r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan(fieldResourceState(t, model))}, &resp)
					if !resp.Diagnostics.HasError() {
						t.Fatal("unsafe create")
					}
				case "update":
					resp := resource.UpdateResponse{State: state}
					r.Update(ctx, resource.UpdateRequest{State: state, Plan: tfsdk.Plan(state)}, &resp)
					if !resp.Diagnostics.HasError() {
						t.Fatal("unsafe update")
					}
				case "delete":
					resp := resource.DeleteResponse{State: state}
					r.Delete(ctx, resource.DeleteRequest{State: state}, &resp)
					if !resp.Diagnostics.HasError() {
						t.Fatal("unsafe delete")
					}
				}
				fieldNoMutation(t, mock)
			})
		}
	}
}

func TestFieldResourceDefaultNameRemainsProtected(t *testing.T) {
	mock := newMetadataMock()
	// A custom object's default name is protected even after its label identifier
	// has been moved elsewhere.
	mock.fields[1]["name"] = "name"
	r := mockFieldResource(mock)
	state := fieldResourceState(t, fieldResourceModel{ID: types.StringValue(metadataTestFieldID)})
	resp := resource.ReadResponse{State: state}
	r.Read(t.Context(), resource.ReadRequest{State: state}, &resp)
	if !resp.Diagnostics.HasError() || resp.State.Raw.IsNull() {
		t.Fatal("default name field import accepted")
	}
	model := fieldResourceTestModel()
	model.Name = types.StringValue("name")
	if d := model.validate(true); !d.HasError() {
		t.Fatal("default name field create accepted")
	}
	fieldNoMutation(t, mock)
}

func TestFieldResourceWrongMutationIDRetainsState(t *testing.T) {
	for _, op := range []string{"update", "delete"} {
		t.Run(op, func(t *testing.T) {
			mock := newMetadataMock()
			mock.change = func(current string, data map[string]any) {
				if current == "UpdateMetadataField" {
					data["updateOneField"] = map[string]any{"id": metadataTestStandardFieldID}
				}
				if current == "DeleteMetadataField" {
					data["deleteOneField"] = map[string]any{"id": metadataTestStandardFieldID}
				}
			}
			r := mockFieldResource(mock)
			model := fieldResourceTestModel()
			model.DeletionProtection = types.BoolValue(false)
			state := fieldResourceState(t, model)
			if op == "update" {
				resp := resource.UpdateResponse{State: state}
				r.Update(t.Context(), resource.UpdateRequest{State: state, Plan: tfsdk.Plan(state)}, &resp)
				if !resp.Diagnostics.HasError() || !resp.State.Raw.Equal(state.Raw) {
					t.Fatal("mismatched update ID lost state")
				}
			} else {
				resp := resource.DeleteResponse{State: state}
				r.Delete(t.Context(), resource.DeleteRequest{State: state}, &resp)
				if !resp.Diagnostics.HasError() || !resp.State.Raw.Equal(state.Raw) {
					t.Fatal("mismatched deletion ID lost state")
				}
			}
		})
	}
}

func TestFieldResourceNameConflictOnLaterPage(t *testing.T) {
	mock := newMetadataMock()
	mock.pageSize = 1
	mock.fields = append(mock.fields, metadataFieldFixture("00000000-0000-4000-8000-000000000599", metadataTestObjectID, "existingField"))
	r := mockFieldResource(mock)
	model := fieldResourceTestModel()
	model.Name = types.StringValue("existingField")
	resp := resource.CreateResponse{State: fieldResourceState(t, fieldResourceModel{})}
	r.Create(t.Context(), resource.CreateRequest{Plan: tfsdk.Plan(fieldResourceState(t, model))}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("later-page name conflict was missed")
	}
	fieldNoMutation(t, mock)
	// Updates also refuse a fresh duplicate, even when the name itself is unchanged.
	mock.fields[len(mock.fields)-1]["name"] = "tier"
	model = fieldResourceTestModel()
	state := fieldResourceState(t, model)
	updated := resource.UpdateResponse{State: state}
	r.Update(t.Context(), resource.UpdateRequest{State: state, Plan: tfsdk.Plan(state)}, &updated)
	if !updated.Diagnostics.HasError() {
		t.Fatal("duplicate name update was allowed")
	}
	fieldNoMutation(t, mock)
}
