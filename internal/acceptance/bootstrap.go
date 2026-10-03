// SPDX-License-Identifier: MPL-2.0

package acceptance

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/oapi-codegen/nullable"

	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
	"github.com/glitchedmob/terraform-provider-twenty/internal/client/testbootstrap"
)

// Account credentials belong to a disposable stack and stay in test memory.
type Account struct {
	Email    string
	Password string
	UserID   string
	MemberID string
	API      graphql.Client
}

// Fixture preserves the operator and an independent verified recovery admin.
// Stage 4 tests may create additional members but must not manage either account.
type Fixture struct {
	Stack       *Stack
	Operator    *Account
	Recovery    *Account
	WorkspaceID string
	AdminRole   client.RoleProperties
}

// Bootstrap uses server-issued mail tokens and generated Metadata mutations.
// It never edits identity rows or signs credentials with APP_SECRET.
func Bootstrap(t *testing.T, stack *Stack) *Fixture {
	t.Helper()
	ctx := t.Context()
	public := stack.api("")
	operator := newAccount(t, "operator")
	t.Log("bootstrapping disposable operator")
	verified := stack.verifyAccount(t, operator)
	workspace, err := testbootstrap.TestCreateWorkspace(ctx, stack.api(verified), testbootstrap.SignUpInNewWorkspaceInput{
		DisplayName: nullable.NewNullableWithValue("Disposable Terraform acceptance"),
	})
	if err != nil {
		t.Fatalf("create disposable workspace: %s", err)
	}
	if workspace.SignUpInNewWorkspace.Workspace.Id == "" {
		t.Fatal("workspace creation returned no ID")
	}
	operator.API = stack.exchange(t, workspace.SignUpInNewWorkspace.LoginToken.Token)
	activated, err := testbootstrap.TestActivateWorkspace(ctx, operator.API, testbootstrap.ActivateWorkspaceInput{})
	if err != nil {
		t.Fatalf("activate disposable workspace: %s", err)
	}
	if activated.ActivateWorkspace.ActivationStatus != testbootstrap.WorkspaceActivationStatusActive {
		t.Fatal("disposable workspace is not active")
	}
	// Exchange once more after activation so tokens are issued for a provisioned workspace.
	operator.API = stack.passwordLogin(t, operator)
	t.Log("disposable workspace activated; checking operator identity")
	identify(t, operator, workspace.SignUpInNewWorkspace.Workspace.Id)
	roles, err := client.GetRoles(ctx, operator.API)
	if err != nil {
		t.Fatalf("read bootstrap administrator role: %s", err)
	}
	var admin client.RoleProperties
	for _, role := range roles.GetRoles {
		if role.CanUpdateAllSettings {
			admin = role.RoleProperties
			break
		}
	}
	if admin.Id == "" {
		t.Fatal("no administrator role in disposable workspace")
	}
	fixture := &Fixture{Stack: stack, Operator: operator, WorkspaceID: workspace.SignUpInNewWorkspace.Workspace.Id, AdminRole: admin}
	recovery := newAccount(t, "recovery")
	fixture.inviteAndJoin(t, public, recovery, admin.Id)
	fixture.Recovery = recovery
	if recovery.UserID == operator.UserID || recovery.MemberID == operator.MemberID {
		t.Fatal("recovery administrator must be an independent identity")
	}
	t.Log("disposable bootstrap complete: active workspace, verified operator and independent verified recovery administrator")
	return fixture
}

func newAccount(t *testing.T, purpose string) *Account {
	t.Helper()
	return &Account{Email: purpose + "-" + randomString(t) + "@acceptance.example", Password: "Aa1!" + randomString(t)[:32]}
}

func (s *Stack) verifyAccount(t *testing.T, account *Account) string {
	t.Helper()
	ctx := t.Context()
	_, err := testbootstrap.TestSignUp(ctx, s.api(""), account.Email, account.Password, nil, nil, nil)
	if err != nil {
		t.Fatalf("sign up disposable identity: %s", err)
	}
	return s.verifyEmail(t, account)
}

func (s *Stack) verifyEmail(t *testing.T, account *Account) string {
	t.Helper()
	ctx := t.Context()
	token, err := s.mailToken(ctx, account.Email, "emailVerificationToken")
	if err != nil {
		t.Fatal(err)
	}
	verified, err := testbootstrap.TestVerifyEmail(ctx, s.api(""), token, account.Email, nil)
	if err != nil {
		t.Fatalf("verify disposable identity using mail token: %s", err)
	}
	access := verified.VerifyEmailAndGetWorkspaceAgnosticToken.Tokens.AccessOrWorkspaceAgnosticToken.Token
	if access == "" {
		t.Fatal("verification returned no workspace-agnostic token")
	}
	return access
}

// InviteAccount creates a verified test-only member with an explicit role.
// Operator and recovery accounts are never reassigned by this helper.
func (f *Fixture) InviteAccount(t *testing.T, purpose, roleID string) *Account {
	t.Helper()
	account := newAccount(t, purpose)
	f.inviteAndJoin(t, f.Stack.api(""), account, roleID)
	return account
}

func (f *Fixture) inviteAndJoin(t *testing.T, public graphql.Client, account *Account, roleID string) {
	t.Helper()
	ctx := t.Context()
	invitation, err := client.SendInvitations(ctx, f.Operator.API, []string{account.Email}, nullable.NewNullableWithValue(roleID))
	if err != nil {
		t.Fatalf("invite disposable identity: %s", err)
	}
	if !invitation.SendInvitations.Success || len(invitation.SendInvitations.Errors) != 0 || len(invitation.SendInvitations.Result) != 1 {
		t.Fatal("disposable identity invitation failed")
	}
	f.acceptInvitation(t, public, account, roleID)
}

// AcceptInvitation consumes only server-issued mail in this disposable stack.
// It does not send another invitation or invent a token. Call it after Terraform
// has invited a fresh test mailbox; keep its credentials out of Terraform.
func (f *Fixture) AcceptInvitation(t *testing.T, email, roleID string) *Account {
	t.Helper()
	if !strings.HasSuffix(email, "@acceptance.example") || client.ValidateEmail(email) != nil ||
		strings.EqualFold(email, f.Operator.Email) || strings.EqualFold(email, f.Recovery.Email) {
		t.Fatal("refuse to accept an invitation outside a fresh disposable test identity")
	}
	account := newAccount(t, "terraform-invited")
	account.Email = email
	f.acceptInvitation(t, f.Stack.api(""), account, roleID)
	return account
}

func (f *Fixture) acceptInvitation(t *testing.T, public graphql.Client, account *Account, roleID string) {
	t.Helper()
	ctx := t.Context()
	token, err := f.Stack.mailToken(ctx, account.Email, "inviteToken")
	if err != nil {
		t.Fatal(err)
	}
	joined, err := testbootstrap.TestJoinWorkspace(ctx, public, account.Email, account.Password,
		nullable.NewNullableWithValue(f.WorkspaceID), nil, nullable.NewNullableWithValue(token), nil, nil, nil)
	if err != nil {
		t.Fatalf("join disposable workspace with personal mail invitation: %s", err)
	}
	if joined.SignUpInWorkspace.Workspace.Id != f.WorkspaceID {
		t.Fatal("invited identity joined a different workspace")
	}
	// The pinned server verifies the email when consuming the personal invitation
	// from that mailbox. Re-verification is rejected as "Email already verified".
	// identify below still requires persisted isEmailVerified=true.
	account.API = f.Stack.passwordLogin(t, account)
	identify(t, account, f.WorkspaceID)
	identity, err := client.CurrentUser(ctx, account.API)
	if err != nil {
		t.Fatalf("check invited identity role: %s", err)
	}
	member, err := identity.CurrentUser.WorkspaceMember.Get()
	if err != nil || len(member.Roles) != 1 || member.Roles[0].Id != roleID {
		t.Fatal("invited identity did not receive the explicit role")
	}
}

func identify(t *testing.T, account *Account, workspaceID string) {
	t.Helper()
	identity, err := client.CurrentUser(t.Context(), account.API)
	if err != nil {
		t.Fatalf("identify disposable workspace member: %s", err)
	}
	user := identity.CurrentUser
	workspace, workspaceErr := user.CurrentWorkspace.Get()
	member, memberErr := user.WorkspaceMember.Get()
	if !user.IsEmailVerified || user.Email != account.Email || user.Id == "" || workspaceErr != nil || workspace.Id != workspaceID || memberErr != nil || member.Id == "" || member.UserId != user.Id {
		t.Fatal("disposable identity is not a verified member of the intended workspace")
	}
	account.UserID, account.MemberID = user.Id, member.Id
}

func (s *Stack) passwordLogin(t *testing.T, account *Account) graphql.Client {
	t.Helper()
	login, err := client.GetLoginTokenFromCredentials(t.Context(), s.api(""), account.Email, account.Password, s.Origin, nil, nil, nil)
	if err != nil {
		t.Fatalf("password login for disposable identity: %s", err)
	}
	return s.exchange(t, login.GetLoginTokenFromCredentials.LoginToken.Token)
}

func (s *Stack) exchange(t *testing.T, token string) graphql.Client {
	t.Helper()
	result, err := client.GetAuthTokensFromLoginToken(t.Context(), s.api(""), token, s.Origin)
	if err != nil {
		t.Fatalf("exchange server-issued disposable login token: %s", err)
	}
	access := result.GetAuthTokensFromLoginToken.Tokens.AccessOrWorkspaceAgnosticToken.Token
	if access == "" {
		t.Fatal("disposable session exchange returned no access token")
	}
	return s.api(access)
}

func (s *Stack) api(token string) graphql.Client {
	httpClient := disposableHTTPClient()
	httpClient.Transport = &bearerTransport{base: httpClient.Transport, token: token, origin: s.Origin}
	return &sanitizedClient{base: graphql.NewClient(s.Endpoint+"/metadata", httpClient)}
}

type bearerTransport struct {
	base   http.RoundTripper
	token  string
	origin string
}

func (b *bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header.Set("Origin", b.origin)
	if b.token != "" {
		request.Header.Set("Authorization", "Bearer "+b.token)
	}
	return b.base.RoundTrip(request)
}

type sanitizedClient struct{ base graphql.Client }

func (s *sanitizedClient) MakeRequest(ctx context.Context, request *graphql.Request, response *graphql.Response) error {
	if err := s.base.MakeRequest(ctx, request, response); err != nil {
		// Fixed classifications only. Upstream messages can contain member email,
		// URL query tokens, passwords, or raw variables and must not be printed.
		for _, code := range []string{"GRAPHQL_VALIDATION_FAILED", "UNAUTHENTICATED", "FORBIDDEN", "BAD_USER_INPUT", "INTERNAL_SERVER_ERROR", "INVALID_INPUT", "EMAIL_NOT_VERIFIED", "SIGNUP_DISABLED"} {
			if strings.Contains(err.Error(), code) {
				return errors.New("disposable Metadata operation rejected: " + code)
			}
		}
		if strings.Contains(err.Error(), "User workspaces not found") {
			return errors.New("disposable Metadata operation failed: User workspaces not found")
		}
		if len(response.Errors) != 0 {
			classes := []string{}
			for _, marker := range []string{"complex", "duplicate", "root resolver", "many fields", "nested fields", "authentication", "invalid", "permission", "workspace", "unknown", "syntax", "unexpected", "expected ", "session", "limit", "fragment"} {
				if strings.Contains(strings.ToLower(err.Error()), marker) {
					classes = append(classes, marker)
				}
			}
			queryFields := map[string]bool{}
			for _, field := range regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`).FindAllString(request.Query, -1) {
				queryFields[field] = true
			}
			selected := []string{}
			for _, item := range response.Errors {
				for _, name := range regexp.MustCompile(`"([A-Za-z_][A-Za-z0-9_]*)"`).FindAllStringSubmatch(item.Message, -1) {
					if queryFields[name[1]] {
						selected = append(selected, name[1])
					}
				}
			}
			path := []string{}
			for _, element := range response.Errors[0].Path {
				field := fmt.Sprint(element)
				// Paths may include indices; emit only fields present in the static query.
				if queryFields[field] {
					path = append(path, field)
				}
			}
			return fmt.Errorf("disposable Metadata GraphQL error: categories %s, selected fields %s, referenced query fields %s; upstream details withheld", strings.Join(classes, ","), strings.Join(path, "."), strings.Join(selected, ","))
		}
		var httpError *graphql.HTTPError
		if errors.As(err, &httpError) {
			return fmt.Errorf("disposable Metadata operation returned HTTP %d; upstream details withheld", httpError.StatusCode)
		}
		for _, classification := range []string{"Cannot query field", "Password too weak", "Email already verified", "unmarshal", "invalid character", "connection refused", "context deadline exceeded", "Email is required", "parsing time", "Cannot return null for non-nullable field"} {
			if strings.Contains(err.Error(), classification) {
				return errors.New("disposable Metadata operation failed: " + classification)
			}
		}
		return errors.New("disposable Metadata operation failed; upstream details withheld")
	}
	return nil
}
