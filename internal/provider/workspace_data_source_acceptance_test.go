// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"strconv"
	"testing"

	"github.com/glitchedmob/terraform-provider-twenty/internal/acceptance"
	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const workspaceAcceptanceConfig = `
provider "twenty" {
  allow_insecure_http = true
}
data "twenty_workspace" "current" {}
`

// Called from the shared suite, not a separate container stack. The only change
// is accepting a disposable invitation, so workspace settings remain untouched.
func testAccWorkspaceDataSource(t *testing.T, fixture *acceptance.Fixture) {
	t.Helper()
	response, err := client.CurrentWorkspace(t.Context(), fixture.Operator.API)
	if err != nil {
		t.Fatal("read disposable workspace identity")
	}
	workspace := response.CurrentWorkspace
	defaultRole, err := workspace.DefaultRole.Get()
	if err != nil {
		t.Fatal("disposable workspace has no default role")
	}
	count, err := workspace.WorkspaceMembersCount.Get()
	if err != nil || count != 2 {
		t.Fatal("expected only the bootstrap operator and recovery administrator")
	}
	check := func(expectedCount int) resource.TestCheckFunc {
		return resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("data.twenty_workspace.current", "id", fixture.WorkspaceID),
			resource.TestCheckResourceAttr("data.twenty_workspace.current", "display_name", "Disposable Terraform acceptance"),
			resource.TestCheckResourceAttr("data.twenty_workspace.current", "default_role_id", defaultRole.Id),
			resource.TestCheckResourceAttr("data.twenty_workspace.current", "activation_status", "ACTIVE"),
			resource.TestCheckResourceAttr("data.twenty_workspace.current", "subdomain", workspace.Subdomain),
			resource.TestCheckResourceAttr("data.twenty_workspace.current", "subdomain_url", workspace.WorkspaceUrls.SubdomainUrl),
			resource.TestCheckResourceAttr("data.twenty_workspace.current", "workspace_members_count", strconv.Itoa(expectedCount)),
			resource.TestCheckResourceAttrSet("data.twenty_workspace.current", "created_at"),
			resource.TestCheckResourceAttrSet("data.twenty_workspace.current", "updated_at"),
		)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acceptanceFactories(),
		Steps: []resource.TestStep{
			{Config: workspaceAcceptanceConfig, Check: check(2)},
			{PreConfig: func() { fixture.InviteAccount(t, "workspace-count", defaultRole.Id) }, Config: workspaceAcceptanceConfig, Check: check(3)},
			{Config: workspaceAcceptanceConfig, PlanOnly: true, ExpectNonEmptyPlan: false},
		},
	})
}
