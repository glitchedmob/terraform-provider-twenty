// SPDX-License-Identifier: MPL-2.0

package acceptance

import (
	"context"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	tcexec "github.com/testcontainers/testcontainers-go/exec"

	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
)

const maintenanceTwentyImage = "twentycrm/twenty:v2.44.0@sha256:01fb6d2c00397976fd7613dbeb9703b514b52fb6270339b7a326a2a975d15b26"

// OperatorInvalidateRoleTargetCache is an explicit test-operator maintenance
// action, not provider behavior. It accepts neither a command nor a container
// selector. Only Start's local pinned server and this fixture's authenticated
// workspace are eligible. No host environment, credentials, shell, DB/Redis
// commands, privilege override, or logs are forwarded.
//
// Pinned official implementation:
// https://github.com/twentyhq/twenty/blob/f7a4720eb4d479bfa3f6634bcdd703bb4de66600/packages/twenty-server/src/engine/workspace-manager/workspace-migration/workspace-migration-runner/commands/flat-cache-invalidate.command.ts
// https://github.com/twentyhq/twenty/blob/f7a4720eb4d479bfa3f6634bcdd703bb4de66600/packages/twenty-server/src/engine/workspace-manager/workspace-migration/workspace-migration-runner/services/workspace-migration-runner.service.ts
// The migration runner's invalidateCache includes userWorkspaceRoleMap for
// flatRoleTargetMaps. This helper must remain outside provider runtime code.
func (f *Fixture) OperatorInvalidateRoleTargetCache(t *testing.T) {
	t.Helper()
	if f == nil || f.Stack == nil || f.Stack.server == nil || f.Operator == nil {
		t.Fatal("maintenance requires a freshly started disposable fixture")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	s := f.Stack
	inspect, err := s.server.Inspect(ctx)
	if err != nil || inspect == nil || inspect.Config == nil || inspect.State == nil {
		t.Fatal("inspect disposable maintenance server")
	}
	if !roleTargetMaintenanceAllowed(roleTargetMaintenanceScope{
		host: s.dockerHost, project: s.projectID, expectedContainer: s.serverID,
		container: inspect.ID, image: inspect.Config.Image, running: inspect.State.Running,
		projectLabel: inspect.Config.Labels["com.docker.compose.project"],
		serviceLabel: inspect.Config.Labels["com.docker.compose.service"],
		workspace:    f.WorkspaceID,
	}) {
		t.Fatal("refuse maintenance outside the pinned disposable server/workspace")
	}
	identity, err := client.CurrentUser(ctx, f.Operator.API)
	if err != nil {
		t.Fatal("verify disposable maintenance workspace identity")
	}
	workspace, workspaceErr := identity.CurrentUser.CurrentWorkspace.Get()
	member, memberErr := identity.CurrentUser.WorkspaceMember.Get()
	if workspaceErr != nil || memberErr != nil || workspace.Id != f.WorkspaceID || identity.CurrentUser.Id != f.Operator.UserID || member.Id != f.Operator.MemberID {
		t.Fatal("maintenance workspace does not match the fixture operator")
	}

	// Docker's exec context bounds the client wait. timeout also bounds the
	// in-container process so canceling the client cannot leave a running CLI.
	helpCode, helpReader, err := s.server.Exec(ctx, []string{
		"timeout", "-k", "10s", "90s", "yarn", "command:prod", "cache:flat-cache-invalidate", "--help",
	}, tcexec.WithWorkingDir("/app/packages/twenty-server"), tcexec.Multiplexed())
	if err != nil || helpCode != 0 {
		t.Fatal("verify pinned cache maintenance command help")
	}
	help, err := io.ReadAll(io.LimitReader(helpReader, 1<<20))
	if err != nil || !strings.Contains(string(help), "--workspace-id [workspace_id]") || !strings.Contains(string(help), "--metadataName <metadataName>") {
		t.Fatal("pinned cache maintenance help did not confirm targeted syntax")
	}
	code, reader, err := s.server.Exec(ctx, []string{
		"timeout", "-k", "10s", "90s", "yarn", "command:prod", "cache:flat-cache-invalidate",
		"--workspace-id", f.WorkspaceID, "--metadataName", "roleTarget",
	}, tcexec.WithWorkingDir("/app/packages/twenty-server"), tcexec.Multiplexed())
	if err != nil || code != 0 {
		t.Fatal("targeted disposable role-target cache maintenance failed")
	}
	output, err := io.ReadAll(io.LimitReader(reader, 1<<20))
	if err != nil || !roleTargetMaintenanceCompleted(string(output), f.WorkspaceID) {
		t.Fatal("targeted maintenance did not confirm exactly the fixture workspace")
	}
	// Output can contain private server configuration or IDs. Emit only this
	// fixed summary, never help, command output, state, or container inspection.
	t.Log("explicit test-operator roleTarget cache invalidation completed for the pinned disposable fixture workspace")
}

type roleTargetMaintenanceScope struct {
	host, project, expectedContainer, container, image string
	projectLabel, serviceLabel, workspace              string
	running                                            bool
}

func roleTargetMaintenanceAllowed(scope roleTargetMaintenanceScope) bool {
	id, err := uuid.Parse(scope.workspace)
	return err == nil && id != uuid.Nil && id.String() == scope.workspace &&
		localDockerHost(scope.host) && strings.HasPrefix(scope.project, "twenty-acc-") &&
		len(scope.project) == len("twenty-acc-")+48 && scope.projectLabel == scope.project &&
		scope.expectedContainer != "" && scope.container == scope.expectedContainer &&
		scope.serviceLabel == "server" && scope.image == maintenanceTwentyImage && scope.running
}

var maintenanceLogColor = regexp.MustCompile(`\x1b\[[0-9;]*m`)
var maintenanceLogTiming = regexp.MustCompile(`^\+[0-9]+ms$`)

func roleTargetMaintenanceCompleted(output, workspace string) bool {
	// Nest's official logger adds SGR color resets and an optional +Nms timing
	// suffix. Ignore only that formatting, not extra IDs or arbitrary text.
	output = maintenanceLogColor.ReplaceAllString(output, "")
	const marker = "Successfully invalidated cache for workspace: "
	if strings.Count(output, marker) != 1 || !strings.Contains(output, "Command completed!") || strings.Contains(output, "Command failed") {
		return false
	}
	_, tail, _ := strings.Cut(output, marker)
	line, _, _ := strings.Cut(tail, "\n")
	fields := strings.Fields(line)
	return len(fields) >= 1 && fields[0] == workspace &&
		(len(fields) == 1 || len(fields) == 2 && maintenanceLogTiming.MatchString(fields[1]))
}
