// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestFieldFailedUpdateCannotDisableDeletionProtection(t *testing.T) {
	mock := newMetadataMock()
	r := mockFieldResource(mock)
	state := fieldResourceState(t, fieldResourceTestModel())
	plan := fieldResourceTestModel()
	plan.Label = types.StringValue("Updated tier")
	plan.DeletionProtection = types.BoolValue(false)
	mock.change = func(op string, _ map[string]any) {
		if op == "UpdateOneField" {
			mock.fail["Fields"] = errors.New("unavailable after write")
		}
	}
	response := resource.UpdateResponse{State: state}
	r.Update(t.Context(), resource.UpdateRequest{State: state, Plan: tfsdk.Plan(fieldResourceState(t, plan))}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("post-write read failure was ignored")
	}
	var retained fieldResourceModel
	if d := response.State.Get(t.Context(), &retained); d.HasError() {
		t.Fatal(d)
	}
	if retained.ID.ValueString() != metadataTestFieldID || !retained.DeletionProtection.ValueBool() {
		t.Fatal("failed update disabled deletion protection")
	}
	mock.calls = nil
	deletion := resource.DeleteResponse{State: response.State}
	r.Delete(t.Context(), resource.DeleteRequest{State: response.State}, &deletion)
	if !deletion.Diagnostics.HasError() || len(mock.calls) != 0 {
		t.Fatal("failed opt-out allowed deletion or API requests")
	}
}

func TestFieldCreateCanonicalizesOnlyWireParentUUID(t *testing.T) {
	const parent = "ABCDEFAB-ABCD-4ABC-8ABC-ABCDEFABCDEF"
	mock := newMetadataMock()
	mock.objects[1]["id"] = strings.ToLower(parent)
	plan := fieldResourceTestModel()
	plan.ID = types.StringUnknown()
	plan.ObjectID = types.StringValue(parent)
	plan.Name = types.StringValue("extraTier")
	response := resource.CreateResponse{State: fieldResourceState(t, plan)}
	mockFieldResource(mock).Create(t.Context(), resource.CreateRequest{Plan: tfsdk.Plan(fieldResourceState(t, plan))}, &response)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	var variables struct {
		Input struct {
			Field struct {
				ObjectMetadataID string `json:"objectMetadataId"`
			}
		}
	}
	if err := json.Unmarshal(mock.inputs["CreateOneField"], &variables); err != nil {
		t.Fatal(err)
	}
	if variables.Input.Field.ObjectMetadataID != strings.ToLower(parent) {
		t.Fatal("case-sensitive upstream parent map received uppercase UUID")
	}
	var state fieldResourceModel
	if d := response.State.Get(t.Context(), &state); d.HasError() {
		t.Fatal(d)
	}
	if state.ObjectID.ValueString() != parent {
		t.Fatal("configured UUID spelling changed in Terraform state")
	}
}

func TestObjectRejectsLabelsThatCreationWouldCapitalize(t *testing.T) {
	for _, label := range []string{"invoice", "étiquette", "ßeta", "ǆuro", "ⅰnvoice", "ⓐlpha", "ᾈlpha"} {
		for _, plural := range []bool{false, true} {
			t.Run(label+map[bool]string{false: "/singular", true: "/plural"}[plural], func(t *testing.T) {
				mock := newMetadataMock()
				plan := objectResourceTestModel()
				plan.NameSingular, plan.NamePlural = types.StringValue("invoice"), types.StringValue("invoices")
				if plural {
					plan.LabelPlural = types.StringValue(label)
				} else {
					plan.LabelSingular = types.StringValue(label)
				}
				response := resource.CreateResponse{State: objectResourceState(t, plan)}
				mockObjectResource(mock).Create(t.Context(), resource.CreateRequest{Plan: objectResourcePlan(t, plan)}, &response)
				if !response.Diagnostics.HasError() || len(mock.calls) != 0 {
					t.Fatal("noncanonical object label reached upstream creation")
				}
			})
		}
	}
}
