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
	"time"

	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func mockObjectResource(m *metadataMock) *objectResource {
	return &objectResource{client: m, identity: workspaceTestIdentity(), mutationLock: &sync.Mutex{}}
}
func objectResourceTestModel() objectResourceModel {
	return objectResourceModel{ID: types.StringValue(metadataTestObjectID), NameSingular: types.StringValue("subscription"), NamePlural: types.StringValue("subscriptions"), LabelSingular: types.StringValue("Subscription"), LabelPlural: types.StringValue("Subscriptions"), Description: types.StringNull(), Icon: types.StringNull(), DeletionProtection: types.BoolValue(true)}
}
func objectResourceState(t *testing.T, model objectResourceModel) tfsdk.State {
	t.Helper()
	var schema resource.SchemaResponse
	NewObjectResource().Schema(t.Context(), resource.SchemaRequest{}, &schema)
	state := tfsdk.State{Schema: schema.Schema}
	if d := state.Set(t.Context(), &model); d.HasError() {
		t.Fatal(d)
	}
	return state
}
func objectResourcePlan(t *testing.T, model objectResourceModel) tfsdk.Plan {
	return tfsdk.Plan(objectResourceState(t, model))
}
func objectStateModel(t *testing.T, state tfsdk.State) objectResourceModel {
	t.Helper()
	var model objectResourceModel
	if d := state.Get(t.Context(), &model); d.HasError() {
		t.Fatal(d)
	}
	return model
}
func TestObjectResourceSchemaConfigureImport(t *testing.T) {
	r := NewObjectResource().(*objectResource)
	var metadata resource.MetadataResponse
	r.Metadata(t.Context(), resource.MetadataRequest{ProviderTypeName: "twenty"}, &metadata)
	var schema resource.SchemaResponse
	r.Schema(t.Context(), resource.SchemaRequest{}, &schema)
	if metadata.TypeName != "twenty_object" || len(schema.Schema.Attributes) != 8 {
		t.Fatal("unexpected schema")
	}
	for name, a := range schema.Schema.Attributes {
		if a.GetMarkdownDescription() == "" {
			t.Fatalf("missing description for %s", name)
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
		t.Fatal("configure must reset")
	}
	for _, data := range []any{"private-secret", (*ClientData)(nil), &ClientData{}} {
		configured = resource.ConfigureResponse{}
		r.Configure(t.Context(), resource.ConfigureRequest{ProviderData: data}, &configured)
		if !configured.Diagnostics.HasError() || strings.Contains(configured.Diagnostics[0].Detail(), "private-secret") {
			t.Fatal("unsafe configure")
		}
	}
	for _, id := range []string{metadataTestObjectID, "ABCDEFAB-1234-4234-8234-123456789ABC", "00000000-0000-0000-0000-000000000000", "subscription", "", " " + metadataTestObjectID, metadataTestObjectID + "/field"} {
		resp := resource.ImportStateResponse{State: objectResourceState(t, objectResourceTestModel())}
		r.ImportState(t.Context(), resource.ImportStateRequest{ID: id}, &resp)
		if resp.Diagnostics.HasError() == metadataUUID(id) {
			t.Fatalf("unexpected import for %q: %v", id, resp.Diagnostics)
		}
		if metadataUUID(id) && objectStateModel(t, resp.State).ID.ValueString() != strings.ToLower(id) {
			t.Fatal("import did not canonicalize UUID")
		}
	}
}
func TestObjectResourceValidation(t *testing.T) {
	for name, change := range map[string]func(*objectResourceModel){
		"null name":              func(m *objectResourceModel) { m.NameSingular = types.StringNull() },
		"bad name":               func(m *objectResourceModel) { m.NameSingular = types.StringValue("Bad_name") },
		"long name":              func(m *objectResourceModel) { m.NameSingular = types.StringValue(strings.Repeat("a", 64)) },
		"unicode name":           func(m *objectResourceModel) { m.NamePlural = types.StringValue("étoiles") },
		"same names":             func(m *objectResourceModel) { m.NamePlural = m.NameSingular },
		"null label":             func(m *objectResourceModel) { m.LabelSingular = types.StringNull() },
		"blank label":            func(m *objectResourceModel) { m.LabelPlural = types.StringValue(" ") },
		"label whitespace":       func(m *objectResourceModel) { m.LabelSingular = types.StringValue("Two  spaces") },
		"description whitespace": func(m *objectResourceModel) { m.Description = types.StringValue(" padded ") },
		"icon whitespace":        func(m *objectResourceModel) { m.Icon = types.StringValue("Icon\tBox") },
		"unknown name":           func(m *objectResourceModel) { m.NameSingular = types.StringUnknown() },
		"unknown label":          func(m *objectResourceModel) { m.LabelPlural = types.StringUnknown() },
		"unknown description":    func(m *objectResourceModel) { m.Description = types.StringUnknown() },
		"unknown icon":           func(m *objectResourceModel) { m.Icon = types.StringUnknown() },
		"unknown protection":     func(m *objectResourceModel) { m.DeletionProtection = types.BoolUnknown() },
		"null protection":        func(m *objectResourceModel) { m.DeletionProtection = types.BoolNull() },
	} {
		t.Run(name, func(t *testing.T) {
			model := objectResourceTestModel()
			change(&model)
			if !model.validate(true).HasError() {
				t.Fatal("invalid input accepted")
			}
		})
	}
	model := objectResourceTestModel()
	model.NameSingular, model.Description, model.DeletionProtection = types.StringUnknown(), types.StringUnknown(), types.BoolUnknown()
	r := &objectResource{}
	var validated resource.ValidateConfigResponse
	r.ValidateConfig(t.Context(), resource.ValidateConfigRequest{Config: tfsdk.Config(objectResourceState(t, model))}, &validated)
	if validated.Diagnostics.HasError() {
		t.Fatal("unknown config should defer", validated.Diagnostics)
	}
}
func TestObjectResourceCRUD(t *testing.T) {
	mock := newMetadataMock()
	mock.pageSize = 1
	r := mockObjectResource(mock)
	model := objectResourceTestModel()
	model.ID, model.NameSingular, model.NamePlural = types.StringUnknown(), types.StringValue("invoice"), types.StringValue("invoices")
	model.LabelSingular, model.LabelPlural = types.StringValue("Bill"), types.StringValue("Bills")
	model.Description, model.Icon = types.StringValue("Invoice details"), types.StringValue("")
	created := resource.CreateResponse{State: objectResourceState(t, model)}
	r.Create(t.Context(), resource.CreateRequest{Plan: objectResourcePlan(t, model)}, &created)
	if created.Diagnostics.HasError() {
		t.Fatal(created.Diagnostics)
	}
	model = objectStateModel(t, created.State)
	if !metadataUUID(model.ID.ValueString()) || model.ID.ValueString() == metadataTestObjectID {
		t.Fatal("must use returned native UUID")
	}
	var wire struct {
		Input struct{ Object map[string]any }
	}
	if err := json.Unmarshal(mock.inputs["CreateOneObject"], &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Input.Object) != 8 || wire.Input.Object["isRemote"] != false || wire.Input.Object["isLabelSyncedWithName"] != false {
		t.Fatalf("unexpected create options: %s", mock.inputs["CreateOneObject"])
	}
	read := resource.ReadResponse{State: created.State}
	r.Read(t.Context(), resource.ReadRequest{State: created.State}, &read)
	if read.Diagnostics.HasError() || !read.State.Raw.Equal(created.State.Raw) {
		t.Fatal("read round trip", read.Diagnostics)
	}
	// No-op updates must preserve independent labels and nullable/empty strings.
	update := resource.UpdateResponse{State: read.State}
	r.Update(t.Context(), resource.UpdateRequest{State: read.State, Plan: objectResourcePlan(t, model)}, &update)
	if update.Diagnostics.HasError() || !update.State.Raw.Equal(read.State.Raw) {
		t.Fatal("no-op update", update.Diagnostics)
	}
	model.LabelSingular, model.LabelPlural = types.StringValue("Charge"), types.StringValue("Charges")
	model.Description, model.Icon, model.DeletionProtection = types.StringNull(), types.StringNull(), types.BoolValue(false)
	updated := resource.UpdateResponse{State: update.State}
	r.Update(t.Context(), resource.UpdateRequest{State: update.State, Plan: objectResourcePlan(t, model)}, &updated)
	if updated.Diagnostics.HasError() {
		t.Fatal(updated.Diagnostics)
	}
	got := objectStateModel(t, updated.State)
	if !got.Description.IsNull() || !got.Icon.IsNull() || got.LabelSingular.ValueString() != "Charge" || got.NameSingular.ValueString() != "invoice" {
		t.Fatal("nullable clear/independent labels failed")
	}
	var updateWire struct {
		Input struct{ Update map[string]any }
	}
	_ = json.Unmarshal(mock.inputs["UpdateOneObject"], &updateWire)
	if len(updateWire.Input.Update) != 5 || updateWire.Input.Update["description"] != nil || updateWire.Input.Update["icon"] != nil || updateWire.Input.Update["isLabelSyncedWithName"] != false {
		t.Fatalf("unexpected update: %s", mock.inputs["UpdateOneObject"])
	}
	deleted := resource.DeleteResponse{State: updated.State}
	beforeDelete := len(mock.calls)
	r.Delete(t.Context(), resource.DeleteRequest{State: updated.State}, &deleted)
	if deleted.Diagnostics.HasError() || !deleted.State.Raw.IsNull() {
		t.Fatal("delete failed", deleted.Diagnostics)
	}
	if slices.Contains(mock.calls[beforeDelete:], "UpdateOneObject") || !slices.Contains(mock.calls[beforeDelete:], "DeleteOneObject") {
		t.Fatal("must delete directly without deactivation")
	}
}
func TestObjectResourceCreateConflicts(t *testing.T) {
	for _, names := range [][2]string{{"subscription", "newThings"}, {"subscriptions", "newThings"}, {"newThing", "subscription"}, {"newThing", "subscriptions"}, {"company", "newThings"}} {
		mock := newMetadataMock()
		mock.pageSize = 1
		model := objectResourceTestModel()
		model.ID, model.NameSingular, model.NamePlural = types.StringUnknown(), types.StringValue(names[0]), types.StringValue(names[1])
		resp := resource.CreateResponse{State: objectResourceState(t, model)}
		mockObjectResource(mock).Create(t.Context(), resource.CreateRequest{Plan: objectResourcePlan(t, model)}, &resp)
		if !resp.Diagnostics.HasError() || slices.Contains(mock.calls, "CreateOneObject") {
			t.Fatalf("name collision not rejected: %v", names)
		}
	}
}
func TestObjectResourceCreateFailureRetention(t *testing.T) {
	for _, failure := range []string{"ambiguous", "read", "wrong values", "wrong ownership", "bad id"} {
		t.Run(failure, func(t *testing.T) {
			mock := newMetadataMock()
			mock.change = func(op string, data map[string]any) {
				if op != "CreateOneObject" {
					return
				}
				switch failure {
				case "read":
					mock.fail["Objects"] = errors.New("private-secret")
				case "wrong values":
					mock.objects[len(mock.objects)-1]["labelSingular"] = "Wrong"
				case "wrong ownership":
					mock.objects[len(mock.objects)-1]["applicationId"] = metadataTestStandardApp
				case "bad id":
					data["createOneObject"] = map[string]any{"id": "bad"}
				}
			}
			if failure == "ambiguous" {
				mock.failAfter["CreateOneObject"] = errors.New("private-secret")
			}
			model := objectResourceTestModel()
			model.ID, model.NameSingular, model.NamePlural = types.StringUnknown(), types.StringValue("invoice"), types.StringValue("invoices")
			state := objectResourceState(t, model)
			resp := resource.CreateResponse{State: state}
			r := mockObjectResource(mock)
			r.Create(t.Context(), resource.CreateRequest{Plan: objectResourcePlan(t, model)}, &resp)
			if !resp.Diagnostics.HasError() || len(mock.objects) != 3 {
				t.Fatal("expected post-mutation failure")
			}
			for _, d := range resp.Diagnostics {
				if strings.Contains(d.Detail(), "private-secret") {
					t.Fatal("leaked upstream error")
				}
			}
			got := objectStateModel(t, resp.State)
			if failure == "ambiguous" || failure == "bad id" {
				if !got.ID.IsUnknown() {
					t.Fatal("must not adopt by name without confirmed UUID")
				}
				if !strings.Contains(resp.Diagnostics[len(resp.Diagnostics)-1].Detail(), "explicitly import") {
					t.Fatal("missing recovery guidance")
				}
			} else if !metadataUUID(got.ID.ValueString()) {
				t.Fatal("confirmed UUID was lost")
			}
			// Retrying an uncertain create must stop on the existing name, not adopt it.
			delete(mock.fail, "Objects")
			again := resource.CreateResponse{State: state}
			before := len(mock.objects)
			r.Create(t.Context(), resource.CreateRequest{Plan: objectResourcePlan(t, model)}, &again)
			if !again.Diagnostics.HasError() || len(mock.objects) != before {
				t.Fatal("retry adopted or recreated object")
			}
		})
	}
}
func TestObjectResourceProtectionAndRead(t *testing.T) {
	for _, protection := range []types.Bool{types.BoolValue(true), types.BoolNull(), types.BoolUnknown()} {
		mock := newMetadataMock()
		model := objectResourceTestModel()
		model.DeletionProtection = protection
		state := objectResourceState(t, model)
		resp := resource.DeleteResponse{State: state}
		mockObjectResource(mock).Delete(t.Context(), resource.DeleteRequest{State: state}, &resp)
		if !resp.Diagnostics.HasError() || len(mock.calls) != 0 || !resp.State.Raw.Equal(state.Raw) {
			t.Fatal("protection must refuse without a write")
		}
	}
	for _, operation := range []string{"Read", "Update", "Delete"} {
		t.Run("built-in "+operation, func(t *testing.T) {
			mock := newMetadataMock()
			r := mockObjectResource(mock)
			model := objectResourceTestModel()
			model.ID = types.StringValue(metadataTestStandardObjectID)
			model.NameSingular, model.NamePlural = types.StringValue("company"), types.StringValue("companys")
			model.DeletionProtection = types.BoolValue(false)
			state := objectResourceState(t, model)
			switch operation {
			case "Read":
				resp := resource.ReadResponse{State: state}
				r.Read(t.Context(), resource.ReadRequest{State: state}, &resp)
				if !resp.Diagnostics.HasError() || !resp.State.Raw.Equal(state.Raw) {
					t.Fatal("built-in read/import accepted")
				}
			case "Update":
				resp := resource.UpdateResponse{State: state}
				r.Update(t.Context(), resource.UpdateRequest{State: state, Plan: objectResourcePlan(t, model)}, &resp)
				if !resp.Diagnostics.HasError() {
					t.Fatal("built-in update accepted")
				}
			case "Delete":
				resp := resource.DeleteResponse{State: state}
				r.Delete(t.Context(), resource.DeleteRequest{State: state}, &resp)
				if !resp.Diagnostics.HasError() {
					t.Fatal("built-in delete accepted")
				}
			}
			if slices.Contains(mock.calls, "UpdateOneObject") || slices.Contains(mock.calls, "DeleteOneObject") {
				t.Fatal("built-in metadata mutated")
			}
		})
	}
	mock := newMetadataMock()
	r := mockObjectResource(mock)
	model := objectResourceTestModel()
	model.DeletionProtection = types.BoolNull()
	state := objectResourceState(t, model)
	read := resource.ReadResponse{State: state}
	r.Read(t.Context(), resource.ReadRequest{State: state}, &read)
	if read.Diagnostics.HasError() || !objectStateModel(t, read.State).DeletionProtection.ValueBool() {
		t.Fatal("import protection must default true")
	}
	mock.objects = mock.objects[:1]
	absent := resource.ReadResponse{State: read.State}
	r.Read(t.Context(), resource.ReadRequest{State: read.State}, &absent)
	if absent.Diagnostics.HasError() || !absent.State.Raw.IsNull() {
		t.Fatal("authoritative absence must remove state")
	}
}
func TestObjectResourceReadErrorsRetainState(t *testing.T) {
	for _, failure := range []string{"invalid id", "null id", "malformed", "later page", "identity", "cancelled"} {
		t.Run(failure, func(t *testing.T) {
			mock := newMetadataMock()
			r := mockObjectResource(mock)
			model := objectResourceTestModel()
			ctx := t.Context()
			switch failure {
			case "invalid id":
				model.ID = types.StringValue("private-secret")
			case "null id":
				model.ID = types.StringNull()
			case "malformed":
				delete(mock.objects[1], "icon")
			case "later page":
				mock.pageSize = 1
				mock.change = func(op string, data map[string]any) {
					if op == "Objects" {
						mock.fail[op] = errors.New("private-secret")
					}
				}
			case "identity":
				r.identity.WorkspaceID = metadataTestStandardApp
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				mock.fail["CurrentUser"] = context.Canceled
			}
			state := objectResourceState(t, model)
			resp := resource.ReadResponse{State: state}
			r.Read(ctx, resource.ReadRequest{State: state}, &resp)
			if !resp.Diagnostics.HasError() || !resp.State.Raw.Equal(state.Raw) {
				t.Fatal("failed read removed state", resp.Diagnostics)
			}
			for _, d := range resp.Diagnostics {
				if strings.Contains(d.Detail(), "private-secret") {
					t.Fatal("unsafe diagnostic")
				}
			}
		})
	}
}
func TestObjectResourceUpdateFailuresRetainState(t *testing.T) {
	for _, failure := range []string{"immutable plan", "immutable server", "invalid id", "identity", "cancelled", "pre-read", "partial", "mutation id", "readback", "values", "ownership"} {
		t.Run(failure, func(t *testing.T) {
			mock := newMetadataMock()
			r := mockObjectResource(mock)
			old := objectResourceTestModel()
			plan := old
			plan.LabelSingular = types.StringValue("New label")
			ctx := t.Context()
			switch failure {
			case "immutable plan":
				plan.NameSingular = types.StringValue("other")
			case "immutable server":
				mock.objects[1]["nameSingular"] = "other"
			case "invalid id":
				old.ID, plan.ID = types.StringValue("invalid"), types.StringValue("invalid")
			case "identity":
				r.identity.WorkspaceID = metadataTestStandardApp
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "pre-read":
				mock.fail["Objects"] = errors.New("private-secret")
			case "partial":
				mock.failAfter["UpdateOneObject"] = errors.New("private-secret")
			}
			mock.change = func(op string, data map[string]any) {
				if op != "UpdateOneObject" {
					return
				}
				switch failure {
				case "mutation id":
					data["updateOneObject"] = map[string]any{"id": metadataTestStandardObjectID}
				case "readback":
					mock.fail["Objects"] = errors.New("private-secret")
				case "values":
					mock.objects[1]["labelSingular"] = "Wrong"
				case "ownership":
					mock.objects[1]["applicationId"] = metadataTestStandardApp
				}
			}
			state := objectResourceState(t, old)
			resp := resource.UpdateResponse{State: state}
			r.Update(ctx, resource.UpdateRequest{State: state, Plan: objectResourcePlan(t, plan)}, &resp)
			if !resp.Diagnostics.HasError() || !resp.State.Raw.Equal(state.Raw) {
				t.Fatal("update failure lost prior state", resp.Diagnostics)
			}
			if slices.Contains([]string{"immutable plan", "immutable server", "invalid id", "identity", "cancelled", "pre-read"}, failure) && slices.Contains(mock.calls, "UpdateOneObject") {
				t.Fatal("unsafe mutation")
			}
		})
	}
}
func TestObjectResourceDeleteFailuresRetainState(t *testing.T) {
	for _, failure := range []string{"invalid id", "identity", "cancelled", "pre-read", "partial", "mutation id", "readback", "still present"} {
		t.Run(failure, func(t *testing.T) {
			mock := newMetadataMock()
			r := mockObjectResource(mock)
			model := objectResourceTestModel()
			model.DeletionProtection = types.BoolValue(false)
			ctx := t.Context()
			switch failure {
			case "invalid id":
				model.ID = types.StringValue("bad")
			case "identity":
				r.identity.WorkspaceID = metadataTestStandardApp
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "pre-read":
				mock.fail["Objects"] = errors.New("private-secret")
			case "partial":
				mock.failAfter["DeleteOneObject"] = errors.New("private-secret")
			}
			mock.change = func(op string, data map[string]any) {
				if op != "DeleteOneObject" {
					return
				}
				switch failure {
				case "mutation id":
					data["deleteOneObject"] = map[string]any{"id": metadataTestStandardObjectID}
				case "readback":
					mock.fail["Objects"] = errors.New("private-secret")
				case "still present":
					mock.objects = append(mock.objects, metadataObjectFixture(metadataTestObjectID, "subscription"))
				}
			}
			state := objectResourceState(t, model)
			resp := resource.DeleteResponse{State: state}
			r.Delete(ctx, resource.DeleteRequest{State: state}, &resp)
			if !resp.Diagnostics.HasError() || !resp.State.Raw.Equal(state.Raw) {
				t.Fatal("delete failure lost state", resp.Diagnostics)
			}
			if slices.Contains([]string{"invalid id", "identity", "cancelled", "pre-read"}, failure) && slices.Contains(mock.calls, "DeleteOneObject") {
				t.Fatal("unsafe mutation")
			}
		})
	}
}
func TestObjectResourceMutationLock(t *testing.T) {
	mock := newMetadataMock()
	r := mockObjectResource(mock)
	model := objectResourceTestModel()
	model.DeletionProtection = types.BoolValue(false)
	state := objectResourceState(t, model)
	ctx, cancel := context.WithCancel(t.Context())
	r.mutationLock.Lock()
	done := make(chan resource.DeleteResponse, 1)
	go func() {
		resp := resource.DeleteResponse{State: state}
		r.Delete(ctx, resource.DeleteRequest{State: state}, &resp)
		done <- resp
	}()
	select {
	case <-done:
		t.Fatal("mutation bypassed shared lock")
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	r.mutationLock.Unlock()
	select {
	case resp := <-done:
		if !resp.Diagnostics.HasError() || slices.Contains(mock.calls, "DeleteOneObject") {
			t.Fatal("cancelled waiter mutated object")
		}
	case <-time.After(time.Second):
		t.Fatal("mutation lock was not released")
	}
}
