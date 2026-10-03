// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestMemberImportRequiresAvailableAssignableAccess(t *testing.T) {
	for _, kind := range []string{"missing", "unassignable"} {
		t.Run(kind, func(t *testing.T) {
			mock := newMemberMock()
			if kind == "unassignable" {
				mock.invite(memberEmail, roleTestID)
				mock.roles[0]["canBeAssignedToUsers"] = false
			}
			r := mockMemberResource(mock)
			resp := resource.ImportStateResponse{State: memberState(t, memberModel())}
			r.ImportState(t.Context(), resource.ImportStateRequest{ID: memberImportID(safetyWorkspace, memberEmail)}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("missing or unassignable access imported")
			}
			if memberCallCount(mock, "SendInvitations") != 0 || memberCallCount(mock, "DeleteWorkspaceInvitation") != 0 {
				t.Fatal("import mutated access")
			}
		})
	}
}
