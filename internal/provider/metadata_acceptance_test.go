// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"testing"

	"github.com/glitchedmob/terraform-provider-twenty/internal/acceptance"
	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/oapi-codegen/nullable"
)

// Uses only the existing disposable stack. No CRM records are created. All
// deletion protection opt-outs below apply only to these disposable fixtures.
func testAccMetadata(t *testing.T, fixture *acceptance.Fixture) {
	const objectAddress = "twenty_object.test"
	const textAddress = `twenty_field.test["TEXT"]`
	var objectID, companyID, companyNameID, companyFieldID string
	fieldIDs := map[string]string{}
	objects, err := readMetadataObjects(t.Context(), fixture.Operator.API)
	if err != nil {
		t.Fatal("read disposable metadata baseline")
	}
	for _, object := range objects {
		if object.NameSingular == "company" {
			companyID = object.Id
		}
	}
	if companyID == "" {
		t.Fatal("disposable company object missing")
	}
	baselineFields, err := readMetadataFields(t.Context(), fixture.Operator.API, companyID)
	if err != nil {
		t.Fatal("read disposable company fields")
	}
	for _, field := range baselineFields {
		if field.Name == "name" {
			companyNameID = field.Id
		}
	}
	if companyNameID == "" {
		t.Fatal("disposable company name field missing")
	}
	capture := func(s *terraform.State) error {
		objectID = s.RootModule().Resources[objectAddress].Primary.ID
		companyFieldID = s.RootModule().Resources["twenty_field.company_extra"].Primary.ID
		for _, kind := range []string{"TEXT", "NUMBER", "BOOLEAN", "DATE", "DATE_TIME"} {
			fieldIDs[kind] = s.RootModule().Resources[`twenty_field.test["`+kind+`"]`].Primary.ID
			if !metadataUUID(fieldIDs[kind]) {
				return fmt.Errorf("field did not persist its native UUID")
			}
		}
		if !metadataUUID(objectID) || !metadataUUID(companyFieldID) {
			return fmt.Errorf("metadata did not persist native UUIDs")
		}
		return nil
	}
	config := func(updated bool) string {
		objectLabel, fieldPrefix, description, icon := "Subscription", "Sample", `"Initial description"`, `"IconBox"`
		if updated {
			objectLabel, fieldPrefix, description, icon = "Plan", "Updated", "null", "null"
		}
		return fmt.Sprintf(`
provider "twenty" { allow_insecure_http = true }
resource "twenty_object" "test" {
 name_singular = "terraformSubscription"
 name_plural = "terraformSubscriptions"
 label_singular = %q
 label_plural = %q
 description = %s
 icon = %s
 deletion_protection = false
}
resource "twenty_field" "test" {
 for_each = toset(["TEXT", "NUMBER", "BOOLEAN", "DATE", "DATE_TIME"])
 object_id = twenty_object.test.id
 name = "sample${replace(lower(each.key), "_", "") }"
 label = "Sample ${each.key}"
 type = each.key
 description = %s
 icon = %s
 deletion_protection = false
}
data "twenty_object" "by_name" {
 name_singular = twenty_object.test.name_singular
 depends_on = [twenty_field.test]
}
data "twenty_object" "by_id" { object_id = twenty_object.test.id }
data "twenty_object" "company" { name_singular = "company" }
resource "twenty_field" "company_extra" {
 object_id = data.twenty_object.company.id
 name = "terraformTier"
 label = %q
 type = "TEXT"
 deletion_protection = false
}
data "twenty_field" "by_id" { field_id = twenty_field.test["TEXT"].id }
data "twenty_field" "by_name" {
 object_id = twenty_object.test.id
 name = twenty_field.test["TEXT"].name
 depends_on = [twenty_field.test]
}
data "twenty_field" "standard_name" {
 object_id = data.twenty_object.company.id
 name = "name"
}
`, objectLabel, objectLabel+"s", description, icon, description, icon, fieldPrefix+" tier")
	}
	initial, updated := config(false), config(true)
	noChanges := resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acceptanceFactories(),
		CheckDestroy: func(_ *terraform.State) error {
			object, err := readMetadataObject(t.Context(), fixture.Operator.API, objectID)
			if err != nil || object != nil {
				return fmt.Errorf("custom object survived destroy or its absence could not be confirmed")
			}
			for _, id := range append([]string{companyFieldID}, fieldIDs["TEXT"], fieldIDs["NUMBER"], fieldIDs["BOOLEAN"], fieldIDs["DATE"], fieldIDs["DATE_TIME"]) {
				field, err := readMetadataField(t.Context(), fixture.Operator.API, id)
				if err != nil || field != nil {
					return fmt.Errorf("custom field survived destroy or its absence could not be confirmed")
				}
			}
			company, err := readMetadataObject(t.Context(), fixture.Operator.API, companyID)
			if err != nil || company == nil || company.NameSingular != "company" {
				return fmt.Errorf("standard company object was changed or removed")
			}
			fields, err := readMetadataFields(t.Context(), fixture.Operator.API, companyID)
			if err != nil || len(fields) != len(baselineFields) {
				return fmt.Errorf("company fields were not restored after teardown")
			}
			name, err := readMetadataField(t.Context(), fixture.Operator.API, companyNameID)
			if err != nil || name == nil || name.Name != "name" {
				return fmt.Errorf("standard company name field was changed or removed")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: initial, Check: resource.ComposeAggregateTestCheckFunc(capture,
				resource.TestCheckResourceAttrPair("data.twenty_object.by_name", "id", objectAddress, "id"),
				resource.TestCheckResourceAttrPair("data.twenty_object.by_id", "id", objectAddress, "id"),
				resource.TestCheckResourceAttrPair("data.twenty_field.by_name", "id", textAddress, "id"),
				resource.TestCheckResourceAttrPair("data.twenty_field.by_id", "id", textAddress, "id"),
				resource.TestCheckResourceAttr("data.twenty_object.company", "id", companyID),
				resource.TestCheckResourceAttr("data.twenty_field.standard_name", "id", companyNameID))},
			{ResourceName: objectAddress, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"deletion_protection"}},
			{ResourceName: textAddress, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"deletion_protection"}},
			{Config: updated, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(objectAddress, plancheck.ResourceActionUpdate), plancheck.ExpectResourceAction(textAddress, plancheck.ResourceActionUpdate)}}, Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(objectAddress, "label_singular", "Plan"), resource.TestCheckNoResourceAttr(objectAddress, "description"), resource.TestCheckNoResourceAttr(textAddress, "description"),
				func(s *terraform.State) error {
					if s.RootModule().Resources[objectAddress].Primary.ID != objectID || s.RootModule().Resources[textAddress].Primary.ID != fieldIDs["TEXT"] {
						return fmt.Errorf("label update replaced metadata")
					}
					return nil
				})},
			{Config: updated, ConfigPlanChecks: noChanges},
			{PreConfig: func() {
				_, err := client.UpdateMetadataField(t.Context(), fixture.Operator.API, client.UpdateOneFieldMetadataInput{Id: fieldIDs["TEXT"], Update: client.UpdateFieldInput{Description: nullable.NewNullableWithValue("Out of band")}})
				if err != nil {
					t.Fatal("out-of-band disposable field update failed")
				}
			}, Config: updated, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(textAddress, plancheck.ResourceActionUpdate)}}},
			{Config: updated, ConfigPlanChecks: noChanges},
			{PreConfig: func() {
				_, err := client.DeleteMetadataField(t.Context(), fixture.Operator.API, client.DeleteOneFieldInput{Id: fieldIDs["NUMBER"]})
				if err != nil {
					t.Fatal("out-of-band disposable field deletion failed")
				}
			}, Config: updated, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(`twenty_field.test["NUMBER"]`, plancheck.ResourceActionCreate)}}, Check: capture},
			{Config: updated, ConfigPlanChecks: noChanges},
		},
	})
}
