// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glitchedmob/terraform-provider-twenty/internal/acceptance"
	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
	"github.com/google/uuid"
)

// This regression deliberately retains a role after a failed destroy. Plugin
// Testing's automatic cleanup would repeat that unsupported destroy, so use a
// local binary/dev override and keep CLI output/state only in a temporary dir.
func testAccCombinedTeardown(t *testing.T, fixture *acceptance.Fixture) {
	newCombinedTeardownFailure(t, fixture)
	// No second destroy, cache write, rebind, or cleanup mutation. The parent
	// suite destroys this disposable stack, including the retained fixture role.
}

// This separate regression exercises explicit operator maintenance, not an
// automatic provider repair. The first destroy still must fail identically.
func testAccOperatorCacheMaintenanceTeardown(t *testing.T, fixture *acceptance.Fixture) {
	failed := newCombinedTeardownFailure(t, fixture)
	fixture.OperatorInvalidateRoleTargetCache(t)
	// One deliberate subsequent user request. No polling or mutation retry.
	failed.cli.requireSuccess(t, "destroy", "-auto-approve")
	if failed.count("DeleteOneRole") != 2 || failed.count("DeleteUserFromWorkspace") != 1 || failed.count("SendInvitations") != 1 || failed.count("UpdateWorkspaceMemberRole") != 0 || failed.count("DeleteWorkspaceInvitation") != 0 || failed.count("CreateOneRole") != 1 || failed.count("UpsertPermissionFlags") != 1 || failed.count("UpdateOneRole") != 0 {
		t.Fatal("operator-maintained destroy must issue only one additional role deletion")
	}
	if len(failed.cli.resources(t)) != 0 {
		t.Fatal("operator-maintained destroy did not remove the retained role from Terraform state")
	}
	session, err := client.NewSession(t.Context(), fixture.Stack.Endpoint, fixture.Operator.Email, fixture.Operator.Password, true)
	if err != nil {
		t.Fatal("authenticate fresh operator after maintained destroy")
	}
	snapshot, err := readMemberSnapshot(t.Context(), session.Client(), session.Identity())
	if err != nil {
		t.Fatal("read fresh server state after maintained destroy")
	}
	access, err := snapshot.access(failed.email)
	if err != nil || access.status != "absent" || snapshot.role(failed.roleID) != nil {
		t.Fatal("maintained destroy must leave both role and membership absent on the server")
	}
	if !reflect.DeepEqual(combinedTeardownAdminIdentities(t, fixture), failed.admins) {
		t.Fatal("operator maintenance or subsequent destroy changed protected administrator identities/roles")
	}
	t.Log("verified v2.44 operator maintenance recovery: first destroy failed, targeted official CLI completed, one subsequent destroy removed the role from server/state without changing either administrator")
}

type combinedTeardownFailure struct {
	cli    *disposableTerraform
	count  func(string) int
	roleID string
	email  string
	admins []client.Identity
}

func newCombinedTeardownFailure(t *testing.T, fixture *acceptance.Fixture) *combinedTeardownFailure {
	t.Helper()
	admins := combinedTeardownAdminIdentities(t, fixture)
	endpoint, err := url.Parse(fixture.Stack.Endpoint)
	if err != nil {
		t.Fatal("parse disposable endpoint")
	}
	proxy := httputil.NewSingleHostReverseProxy(endpoint)
	transport := &http.Transport{Proxy: nil}
	t.Cleanup(transport.CloseIdleConnections)
	proxy.Transport = transport
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "disposable Metadata forwarding failed", http.StatusBadGateway)
	}
	var lock sync.Mutex
	calls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/metadata" {
			http.Error(w, "Metadata only", http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		var request struct {
			Operation string `json:"operationName"`
		}
		if json.Unmarshal(body, &request) != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		lock.Lock()
		calls[request.Operation]++
		lock.Unlock()
		r.Body = io.NopCloser(bytes.NewReader(body))
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	count := func(op string) int { lock.Lock(); defer lock.Unlock(); return calls[op] }
	cli := newDisposableTerraform(t, fixture, server.URL)
	email := "combined-teardown-" + uuid.NewString() + "@acceptance.example"
	cli.writeConfig(t, fmt.Sprintf(`terraform {
 required_providers { twenty = { source = "glitchedmob/twenty" } }
}
provider "twenty" { allow_insecure_http = true }
resource "twenty_role" "custom" {
 label = %q
 permission_flags = []
}
resource "twenty_workspace_member" "declared" {
 email = %q
 role_id = twenty_role.custom.id
}
`, "Combined teardown "+uuid.NewString(), email))
	cli.requireSuccess(t, "apply", "-auto-approve")
	before := cli.resources(t)
	roleID, validID := before["twenty_role.custom"]["id"].(string)
	if !validID || !validRoleUUID(roleID) {
		t.Fatal("Terraform did not record a native custom-role ID")
	}
	compoundID := memberImportID(fixture.WorkspaceID, email)
	if len(before) != 2 || before["twenty_workspace_member.declared"]["status"] != "pending" || before["twenty_workspace_member.declared"]["id"] != compoundID {
		t.Fatal("Terraform did not own both the custom role and pending membership")
	}
	account := fixture.AcceptInvitation(t, email, roleID)
	cli.requireSuccess(t, "apply", "-refresh-only", "-auto-approve")
	cli.requireSuccess(t, "plan", "-detailed-exitcode")
	accepted := cli.resources(t)["twenty_workspace_member.declared"]
	if accepted["status"] != "accepted" || accepted["member_id"] != account.MemberID || accepted["id"] != compoundID || accepted["ownership_confirmed"] != true || accepted["invitation_id"] != nil {
		t.Fatal("accepted access did not preserve its compound identity/ownership")
	}
	output, code := cli.run(t, "destroy", "-auto-approve")
	// Expected pinned upstream failure, not a tolerated fixture cleanup error.
	if code != 1 || !strings.Contains(output, "Unable to Delete Twenty Role") || !strings.Contains(strings.Join(strings.Fields(output), " "), pinnedRoleDeletionCacheDiagnostic) {
		t.Fatal("expected the classified v2.44 combined teardown failure, not successful destroy or another error")
	}
	// The plan itself displays native IDs. Check only the failure diagnostic;
	// the whole CLI output stays in memory and is never logged or archived.
	_, diagnostic, found := strings.Cut(output, "Error: Unable to Delete Twenty Role")
	if !found || strings.Contains(diagnostic, account.MemberID) || strings.Contains(diagnostic, fixture.Operator.Password) || strings.Contains(diagnostic, "User workspaces not found") {
		t.Fatal("destroy diagnostic leaked raw upstream data")
	}
	if count("DeleteOneRole") != 1 || count("DeleteUserFromWorkspace") != 1 || count("SendInvitations") != 1 || count("UpdateWorkspaceMemberRole") != 0 || count("DeleteWorkspaceInvitation") != 0 || count("CreateOneRole") != 1 || count("UpsertPermissionFlags") != 1 || count("UpdateOneRole") != 0 {
		t.Fatal("combined teardown retried or attempted an unrelated assignment/cache repair")
	}
	after := cli.resources(t)
	if len(after) != 1 || after["twenty_role.custom"]["id"] != roleID {
		t.Fatal("failed destroy must retain only the role in Terraform state")
	}
	session, err := client.NewSession(t.Context(), fixture.Stack.Endpoint, fixture.Operator.Email, fixture.Operator.Password, true)
	if err != nil {
		t.Fatal("authenticate fresh operator after combined destroy")
	}
	snapshot, err := readMemberSnapshot(t.Context(), session.Client(), session.Identity())
	if err != nil {
		t.Fatal("read fresh server state after combined destroy")
	}
	access, err := snapshot.access(email)
	if err != nil || access.status != "absent" || snapshot.role(roleID) == nil {
		t.Fatal("server must retain the custom role but not the accepted member or invitation")
	}
	if !reflect.DeepEqual(combinedTeardownAdminIdentities(t, fixture), admins) {
		t.Fatal("combined teardown changed protected administrator identities/roles")
	}
	t.Log("expected v2.44 destroy failure: member absent from state/server, custom role retained in state/server, one deletion request each, both administrators preserved")
	return &combinedTeardownFailure{cli: cli, count: count, roleID: roleID, email: email, admins: admins}
}

func combinedTeardownAdminIdentities(t *testing.T, fixture *acceptance.Fixture) []client.Identity {
	t.Helper()
	var admins []client.Identity
	for _, admin := range []*acceptance.Account{fixture.Operator, fixture.Recovery} {
		fresh, err := client.NewSession(t.Context(), fixture.Stack.Endpoint, admin.Email, admin.Password, true)
		if err != nil || fresh.Identity().WorkspaceMemberID != admin.MemberID || fresh.Identity().UserID != admin.UserID {
			t.Fatal("combined teardown changed a protected administrator's identity/login")
		}
		identity, err := client.CurrentUser(t.Context(), fresh.Client())
		if err != nil {
			t.Fatal("read protected administrator after combined teardown")
		}
		member, err := identity.CurrentUser.WorkspaceMember.Get()
		if err != nil || len(member.Roles) != 1 || member.Roles[0].Id != fixture.AdminRole.Id || fresh.Identity().WorkspaceID != fixture.WorkspaceID {
			t.Fatal("combined teardown changed a protected administrator's role/workspace")
		}
		roles, err := fresh.GetRoles(t.Context())
		if err != nil {
			t.Fatal("read protected administrator role properties")
		}
		found := false
		for _, role := range roles.GetRoles {
			if role.Id == fixture.AdminRole.Id {
				found = reflect.DeepEqual(role.RoleProperties, fixture.AdminRole)
			}
		}
		if !found {
			t.Fatal("combined teardown changed the unmanaged administrator role properties")
		}
		admins = append(admins, fresh.Identity())
	}
	return admins
}

type disposableTerraform struct {
	dir    string
	binary string
	env    []string
}

func newDisposableTerraform(t *testing.T, fixture *acceptance.Fixture, endpoint string) *disposableTerraform {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal("create local provider binary directory")
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate local provider source")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	// Plugin Testing can put HOME in the parent test's temporary directory.
	// Keep any resulting module cache writable so TempDir cleanup can remove it.
	build := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-modcacherw", "-o", filepath.Join(bin, "terraform-provider-twenty"), ".")
	build.Dir = filepath.Join(filepath.Dir(source), "../..")
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(key, "TWENTY_") && !strings.HasPrefix(key, "TF_") {
			build.Env = append(build.Env, entry)
		}
	}
	if _, err := build.CombinedOutput(); err != nil {
		t.Fatal("build local acceptance provider binary")
	}
	config := filepath.Join(dir, "terraform.rc")
	if err := os.WriteFile(config, []byte(fmt.Sprintf("provider_installation {\n dev_overrides {\n \"glitchedmob/twenty\" = %q\n }\n direct {}\n}\n", bin)), 0o600); err != nil {
		t.Fatal("write local dev override")
	}
	binary := os.Getenv("TF_ACC_TERRAFORM_PATH")
	if binary == "" {
		var err error
		binary, err = exec.LookPath("terraform")
		if err != nil {
			t.Fatal("locate pinned Terraform executable")
		}
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal("resolve pinned Terraform executable")
	}
	cli := &disposableTerraform{dir: dir, binary: binary, env: []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "TMPDIR=" + dir,
		"TF_CLI_CONFIG_FILE=" + config, "TF_IN_AUTOMATION=1", "TF_INPUT=0", "CHECKPOINT_DISABLE=1",
		"TWENTY_ENDPOINT=" + endpoint, "TWENTY_EMAIL=" + fixture.Operator.Email, "TWENTY_PASSWORD=" + fixture.Operator.Password,
	}}
	output, code := cli.run(t, "version", "-json")
	var version struct {
		TerraformVersion string `json:"terraform_version"`
	}
	if code != 0 || json.Unmarshal([]byte(output), &version) != nil || version.TerraformVersion != "1.14.7" {
		t.Fatal("combined regression requires the pinned local Terraform 1.14.7 binary")
	}
	return cli
}

func (c *disposableTerraform) writeConfig(t *testing.T, config string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(c.dir, "main.tf"), []byte(config), 0o600); err != nil {
		t.Fatal("write disposable Terraform configuration")
	}
}

func (c *disposableTerraform) run(t *testing.T, args ...string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.binary, append(args, "-no-color")...)
	cmd.Dir, cmd.Env = c.dir, c.env
	output, err := cmd.CombinedOutput()
	if err == nil {
		return string(output), 0
	}
	if ctx.Err() != nil {
		t.Fatal("disposable Terraform command exceeded its timeout")
	}
	if exit, ok := err.(*exec.ExitError); ok {
		return string(output), exit.ExitCode()
	}
	t.Fatal("execute pinned local Terraform binary")
	return "", -1
}

func (c *disposableTerraform) requireSuccess(t *testing.T, args ...string) {
	t.Helper()
	if _, code := c.run(t, args...); code != 0 {
		// Never print CLI output. It can contain state or private runtime data.
		t.Fatalf("disposable Terraform %s failed", args[0])
	}
}

func (c *disposableTerraform) resources(t *testing.T) map[string]map[string]any {
	t.Helper()
	output, code := c.run(t, "show", "-json")
	var state struct {
		Values struct {
			RootModule struct {
				Resources []struct {
					Address string
					Values  map[string]any
				}
			} `json:"root_module"`
		}
	}
	if code != 0 || json.Unmarshal([]byte(output), &state) != nil {
		t.Fatal("read disposable Terraform state in memory")
	}
	result := map[string]map[string]any{}
	for _, resource := range state.Values.RootModule.Resources {
		result[resource.Address] = resource.Values
	}
	return result
}
