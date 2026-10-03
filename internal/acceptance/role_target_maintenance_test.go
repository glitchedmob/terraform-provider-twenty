// SPDX-License-Identifier: MPL-2.0

package acceptance

import (
	"strings"
	"testing"
)

func TestRoleTargetMaintenanceScope(t *testing.T) {
	valid := roleTargetMaintenanceScope{
		host: "unix:///var/run/docker.sock", project: "twenty-acc-" + strings.Repeat("a", 48),
		expectedContainer: "fixture-server-id", container: "fixture-server-id",
		image: maintenanceTwentyImage, running: true,
		projectLabel: "twenty-acc-" + strings.Repeat("a", 48), serviceLabel: "server",
		workspace: "e4bc1b7c-6afd-4f85-9bdc-fbb644aef1ea",
	}
	if !roleTargetMaintenanceAllowed(valid) {
		t.Fatal("valid local pinned disposable scope rejected")
	}
	for name, change := range map[string]func(*roleTargetMaintenanceScope){
		"remote_daemon":    func(s *roleTargetMaintenanceScope) { s.host = "tcp://remote:2376" },
		"other_project":    func(s *roleTargetMaintenanceScope) { s.project = "unrelated" },
		"project_mismatch": func(s *roleTargetMaintenanceScope) { s.projectLabel += "other" },
		"missing_identity": func(s *roleTargetMaintenanceScope) { s.expectedContainer = "" },
		"other_container":  func(s *roleTargetMaintenanceScope) { s.container = "other-server-id" },
		"worker":           func(s *roleTargetMaintenanceScope) { s.serviceLabel = "worker" },
		"other_image":      func(s *roleTargetMaintenanceScope) { s.image = "twentycrm/twenty:v2.44.0" },
		"stopped":          func(s *roleTargetMaintenanceScope) { s.running = false },
		"empty_workspace":  func(s *roleTargetMaintenanceScope) { s.workspace = "" },
		"nil_workspace":    func(s *roleTargetMaintenanceScope) { s.workspace = "00000000-0000-0000-0000-000000000000" },
		"upper_workspace":  func(s *roleTargetMaintenanceScope) { s.workspace = strings.ToUpper(s.workspace) },
		"option_injection": func(s *roleTargetMaintenanceScope) { s.workspace += " --all-metadata" },
	} {
		t.Run(name, func(t *testing.T) {
			scope := valid
			change(&scope)
			if roleTargetMaintenanceAllowed(scope) {
				t.Fatal("unsafe maintenance scope accepted")
			}
		})
	}
}

func TestRoleTargetMaintenanceCompletion(t *testing.T) {
	workspace := "e4bc1b7c-6afd-4f85-9bdc-fbb644aef1ea"
	success := "Successfully invalidated cache for workspace: " + workspace + "\nCommand completed!\n"
	for _, output := range []string{
		success,
		"\x1b[32m[Nest] LOG [FlatCacheInvalidateCommand] Successfully invalidated cache for workspace: " + workspace + "\x1b[39m \x1b[33m+12ms\x1b[39m\n\x1b[32mCommand completed!\x1b[39m\n",
	} {
		if !roleTargetMaintenanceCompleted(output, workspace) {
			t.Fatal("targeted completion rejected")
		}
	}
	for _, output := range []string{
		"", "Command completed!", strings.ReplaceAll(success, workspace, "other-workspace"),
		success + success, strings.ReplaceAll(success, "Command completed!", "Command failed"),
		success + "Command failed", strings.ReplaceAll(success, workspace, workspace+"extra"),
		strings.ReplaceAll(success, workspace, workspace+" unexpected-workspace"),
	} {
		if roleTargetMaintenanceCompleted(output, workspace) {
			t.Fatal("unconfirmed or multi-workspace maintenance accepted")
		}
	}
}
