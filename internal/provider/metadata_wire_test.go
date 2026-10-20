// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"errors"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/oapi-codegen/nullable"
)

func TestMetadataMalformedWire(t *testing.T) {
	for _, entity := range []string{"object", "field"} {
		keys := []string{"id", "applicationId", "universalIdentifier", "description", "icon", "writability", "isActive", "isSystem", "isLabelSyncedWithName"}
		if entity == "object" {
			keys = append(keys, "nameSingular", "namePlural", "labelSingular", "labelPlural", "isRemote", "labelIdentifierFieldMetadataId")
		} else {
			keys = append(keys, "name", "label", "type", "objectMetadataId", "isNullable")
		}
		for _, key := range keys {
			for _, malformed := range []string{"omitted", "wrong_type", "null"} {
				if malformed == "null" && (key == "description" || key == "icon" || key == "labelIdentifierFieldMetadataId") {
					continue
				}
				t.Run(entity+"/"+key+"/"+malformed, func(t *testing.T) {
					mock := newMetadataMock()
					mock.change = func(op string, data map[string]any) {
						root := "objects"
						if entity == "field" {
							root = "fields"
						}
						connection, ok := data[root].(map[string]any)
						if !ok {
							return
						}
						node := connection["edges"].([]map[string]any)[0]["node"].(map[string]any)
						switch malformed {
						case "omitted":
							delete(node, key)
						case "null":
							node[key] = nil
						default:
							node[key] = map[string]any{"secret": "private-upstream-detail"}
						}
					}
					var err error
					if entity == "object" {
						_, err = readMetadataObjects(t.Context(), mock)
					} else {
						_, err = readMetadataFields(t.Context(), mock, metadataTestObjectID)
					}
					if !errors.Is(err, errInvalidMetadataResponse) {
						t.Fatalf("malformed %s %s accepted: %v", entity, key, err)
					}
				})
			}
		}
	}
}

func TestMetadataPaginationFailures(t *testing.T) {
	for _, kind := range []string{"missing_page_info", "missing_has_next", "null_has_next", "missing_cursor", "null_cursor_with_next", "empty_edges_with_next", "duplicate_id", "repeated_cursor"} {
		t.Run(kind, func(t *testing.T) {
			mock := newMetadataMock()
			mock.pageSize = 1
			mock.change = func(op string, data map[string]any) {
				connection, ok := data["objects"].(map[string]any)
				if !ok {
					return
				}
				page := connection["pageInfo"].(map[string]any)
				switch kind {
				case "missing_page_info":
					delete(connection, "pageInfo")
				case "missing_has_next":
					delete(page, "hasNextPage")
				case "null_has_next":
					page["hasNextPage"] = nil
				case "missing_cursor":
					delete(page, "endCursor")
				case "null_cursor_with_next":
					page["hasNextPage"], page["endCursor"] = true, nil
				case "empty_edges_with_next":
					page["hasNextPage"] = true
					connection["edges"] = []map[string]any{}
				case "duplicate_id":
					connection["edges"].([]map[string]any)[0]["node"].(map[string]any)["id"] = metadataTestStandardObjectID
				case "repeated_cursor":
					page["hasNextPage"], page["endCursor"] = true, "1"
				}
			}
			if _, err := readMetadataObjects(t.Context(), mock); !errors.Is(err, errInvalidMetadataResponse) {
				t.Fatalf("unsafe pagination accepted: %v", err)
			}
		})
	}
}

func TestMetadataFieldFilterCannotCrossObjects(t *testing.T) {
	mock := newMetadataMock()
	mock.change = func(op string, data map[string]any) {
		if op != "ListMetadataFields" {
			return
		}
		data["fields"].(map[string]any)["edges"].([]map[string]any)[0]["node"].(map[string]any)["objectMetadataId"] = metadataTestStandardObjectID
	}
	if _, err := readMetadataFields(t.Context(), mock, metadataTestObjectID); !errors.Is(err, errInvalidMetadataResponse) {
		t.Fatalf("foreign field accepted: %v", err)
	}
}

func TestMetadataCustomOwnership(t *testing.T) {
	mock := newMetadataMock()
	app, err := metadataCustomApplication(t.Context(), mock, workspaceTestIdentity())
	if err != nil || app != metadataTestCustomApp {
		t.Fatalf("workspace custom application: %v", err)
	}
	objects, err := readMetadataObjects(t.Context(), mock)
	if err != nil {
		t.Fatal(err)
	}
	if guardMetadataObject(&objects[0], app) != errUnsafeMetadata {
		t.Fatal("editable non-system built-in object was accepted")
	}
	if err := guardMetadataObject(&objects[1], app); err != nil {
		t.Fatal(err)
	}
	fields, err := readMetadataFields(t.Context(), mock, metadataTestObjectID)
	if err != nil || len(fields) != 1 {
		t.Fatal(err)
	}
	if err := guardMetadataField(&fields[0], &objects[1], app); err != nil {
		t.Fatal(err)
	}
	identifier := fields[0].Id
	objects[1].LabelIdentifierFieldMetadataId = nullable.NewNullableWithValue(identifier)
	if err := guardMetadataField(&fields[0], &objects[1], app); err != errUnsafeMetadata {
		t.Fatal("label identifier field was accepted")
	}
	mock.workspace.workspace["workspaceCustomApplicationId"] = ""
	if _, err := metadataCustomApplication(t.Context(), mock, workspaceTestIdentity()); err != errInvalidMetadataResponse {
		t.Fatal("missing custom application accepted")
	}
}

func TestMetadataDiagnosticRedaction(t *testing.T) {
	var diagnostics diag.Diagnostics
	metadataError(&diagnostics, "Read", "Object", errors.New("private-upstream-detail"))
	if !diagnostics.HasError() || strings.Contains(diagnostics[0].Detail(), "private-upstream-detail") {
		t.Fatal("upstream metadata error escaped")
	}
}
