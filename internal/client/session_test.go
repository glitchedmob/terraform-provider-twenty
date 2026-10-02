// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Khan/genqlient/graphql"
)

const (
	sessionUserID      = "00000000-0000-4000-8000-000000000101"
	sessionWorkspaceID = "00000000-0000-4000-8000-000000000102"
	sessionMemberID    = "00000000-0000-4000-8000-000000000103"
	sessionUWID        = "00000000-0000-4000-8000-000000000104"
	sessionRoleID      = "00000000-0000-4000-8000-000000000105"
	sessionEmail       = "automation@example.invalid"
	sessionPassword    = " test-only-password "
)

type sessionFixture struct {
	t        *testing.T
	server   *httptest.Server
	mu       sync.Mutex
	ops      []string
	renews   int
	user     map[string]any
	override func(http.ResponseWriter, string) bool
}

func sessionUserFixture() map[string]any {
	member := map[string]any{
		"id": sessionMemberID, "userId": sessionUserID, "userWorkspaceId": sessionUWID,
		"userEmail": sessionEmail, "roles": []any{map[string]any{"id": sessionRoleID}},
	}
	return map[string]any{
		"id": sessionUserID, "email": sessionEmail, "isEmailVerified": true, "disabled": false,
		"hasPassword": true, "onboardingStatus": "COMPLETED",
		"currentWorkspace":     map[string]any{"id": sessionWorkspaceID, "activationStatus": "ACTIVE"},
		"currentUserWorkspace": map[string]any{"id": sessionUWID, "userId": sessionUserID, "permissionFlags": []string{"ROLES"}},
		"workspaceMember":      member, "workspaceMembers": []any{member},
	}
}

func newSessionFixture(t *testing.T) *sessionFixture {
	t.Helper()
	f := &sessionFixture{t: t, user: sessionUserFixture()}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

func sessionPairFixture(n int) map[string]any {
	expiry := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	return map[string]any{
		"accessOrWorkspaceAgnosticToken": map[string]any{"token": fmt.Sprintf("test-access-%d", n), "expiresAt": expiry},
		"refreshToken":                   map[string]any{"token": fmt.Sprintf("test-refresh-%d", n), "expiresAt": expiry},
	}
}

func (f *sessionFixture) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var req struct {
		OpName    string         `json:"operationName"`
		Variables map[string]any `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		f.t.Error("request must contain valid JSON")
		return
	}
	f.ops = append(f.ops, req.OpName)
	if r.Method != http.MethodPost || r.URL.Path != "/metadata" || r.Header.Get("Content-Type") != "application/json" ||
		r.Header.Get("Origin") != f.server.URL {
		f.t.Error("unexpected Metadata request method, path, or headers")
	}
	if f.override != nil && f.override(w, req.OpName) {
		return
	}
	var value any
	field := strings.ToLower(req.OpName[:1]) + req.OpName[1:]
	switch req.OpName {
	case "GetLoginTokenFromCredentials":
		if req.Variables["email"] != sessionEmail || req.Variables["password"] != sessionPassword || req.Variables["origin"] != f.server.URL || r.Header.Get("Authorization") != "" {
			f.t.Error("login must use exact credentials and instance origin without bearer authorization")
		}
		value = map[string]any{"loginToken": map[string]any{"token": "test-login", "expiresAt": time.Now().Add(time.Minute).Format(time.RFC3339Nano)}}
	case "GetAuthTokensFromLoginToken":
		if req.Variables["loginToken"] != "test-login" || req.Variables["origin"] != f.server.URL || r.Header.Get("Authorization") != "" {
			f.t.Error("exchange must use login token and instance origin without bearer authorization")
		}
		value = map[string]any{"tokens": sessionPairFixture(f.renews)}
	case "RenewToken":
		if req.Variables["appToken"] != fmt.Sprintf("test-refresh-%d", f.renews) || r.Header.Get("Authorization") != "" {
			f.t.Error("renewal must consume only the current refresh token")
		}
		f.renews++
		value = map[string]any{"tokens": sessionPairFixture(f.renews)}
	case "CurrentUser", "CurrentWorkspace", "GetRoles":
		if r.Header.Get("Authorization") != fmt.Sprintf("Bearer test-access-%d", f.renews) {
			f.t.Error("authenticated operation did not use the current access token")
		}
		switch req.OpName {
		case "CurrentUser":
			value = f.user
		case "CurrentWorkspace":
			value = map[string]any{"id": sessionWorkspaceID, "activationStatus": "ACTIVE"}
		case "GetRoles":
			value = []any{map[string]any{"id": sessionRoleID, "label": "Test role"}}
		}
	default:
		f.t.Error("unexpected operation")
	}
	if err := json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{field: value}}); err != nil {
		f.t.Error("encode fixture response")
	}
}

func (f *sessionFixture) session(t *testing.T) *Session {
	t.Helper()
	s, err := NewSession(t.Context(), f.server.URL, sessionEmail, sessionPassword, true)
	if err != nil {
		t.Fatalf("synthetic authentication: %v", err)
	}
	return s
}

func TestValidateEmail(t *testing.T) {
	t.Parallel()
	for _, email := range []string{
		sessionEmail, " " + sessionEmail + " ", "automation+stage-3@sub.example.invalid",
		"First.Last@Example.Invalid", "o'hara@example.invalid", "postmaster@localhost",
		"a!#$%&'*+-/=?^_`{|}~@example.invalid", "automation@[127.0.0.1]",
	} {
		t.Run(email, func(t *testing.T) {
			t.Parallel()
			if err := ValidateEmail(email); err != nil {
				t.Fatalf("bare mailbox syntax should be accepted: %v", err)
			}
		})
	}
	for _, email := range []string{
		"", " ", "not-an-email", "automation@", "@example.invalid", "automation@@example.invalid",
		"automation..user@example.invalid", ".automation@example.invalid", "automation.@example.invalid",
		"Automation <automation@example.invalid>", "<automation@example.invalid>",
		"automation@example.invalid (Automation)", "automation(comment)@example.invalid",
		"automation@example.invalid,other@example.invalid", "automation @example.invalid",
		"\"automation\"@example.invalid", "automation@example.invalid\r\n", "\tautomation@example.invalid",
		"auto\x00mation@example.invalid", "auto\x1fmation@example.invalid", "auto\x7fmation@example.invalid",
		"automatiön@example.invalid", "automation@exämple.invalid", "automation\u200b@example.invalid",
	} {
		t.Run(email, func(t *testing.T) {
			t.Parallel()
			if err := ValidateEmail(email); !errors.Is(err, errInvalidEmail) || err.Error() != errInvalidEmail.Error() {
				t.Fatalf("invalid mailbox must return the fixed email diagnostic: %v", err)
			}
		})
	}
}

func TestSessionInvalidEmailDoesNotAuthenticate(t *testing.T) {
	t.Parallel()
	for _, email := range []string{
		"not-an-email", "Automation <automation@example.invalid>", "automation@example.invalid (Automation)",
		"automation@example.invalid\r\n", "\tautomation@example.invalid", "auto\x00mation@example.invalid",
		"automatiön@example.invalid", "automation@exämple.invalid",
	} {
		t.Run(email, func(t *testing.T) {
			t.Parallel()
			f := newSessionFixture(t)
			s, err := NewSession(t.Context(), f.server.URL, email, sessionPassword, true)
			if s != nil || !errors.Is(err, errInvalidEmail) || err.Error() != errInvalidEmail.Error() || len(f.ops) != 0 {
				t.Fatalf("invalid email must fail locally before any request: %v", err)
			}
		})
	}
}

func TestSessionEmailSurroundingSpaces(t *testing.T) {
	t.Parallel()
	f := newSessionFixture(t)
	s, err := NewSession(t.Context(), f.server.URL, " "+sessionEmail+" ", sessionPassword, true)
	if err != nil || s == nil || s.Identity().Email != sessionEmail || len(f.ops) != 4 {
		t.Fatalf("surrounding spaces must be trimmed before authentication: %v", err)
	}
}

func TestSessionLoginIdentityAndRefresh(t *testing.T) {
	t.Parallel()
	f := newSessionFixture(t)
	s := f.session(t)
	id := s.Identity()
	if id.UserID != sessionUserID || id.WorkspaceID != sessionWorkspaceID || id.WorkspaceMemberID != sessionMemberID || id.UserWorkspaceID != sessionUWID || id.Email != sessionEmail || len(id.RoleIDs) != 1 || id.RoleIDs[0] != sessionRoleID {
		t.Fatal("validated identity is incomplete")
	}
	id.RoleIDs[0] = "changed"
	id.PermissionFlags[0] = PermissionFlagTypeWorkspaceMembers
	if s.Identity().RoleIDs[0] != sessionRoleID || s.Identity().PermissionFlags[0] != PermissionFlagTypeRoles {
		t.Fatal("identity caller must not mutate the session")
	}
	if s.Client() != s || s.GraphQL() != s {
		t.Fatal("generated operations must share the session")
	}
	for range 2 {
		if err := s.Refresh(t.Context()); err != nil {
			t.Fatal(err)
		}
		result, err := s.GetRoles(t.Context())
		if err != nil || len(result.GetRoles) != 1 || result.GetRoles[0].Id != sessionRoleID {
			t.Fatal("role operation failed after rotating both tokens")
		}
	}
	if f.renews != 2 {
		t.Fatal("explicit renewal must call the server once")
	}
}

func TestSessionRefreshPreservesIdentitySnapshot(t *testing.T) {
	t.Parallel()
	f := newSessionFixture(t)
	s := f.session(t)
	f.mu.Lock()
	f.user["email"] = "changed@example.invalid"
	f.user["workspaceMember"].(map[string]any)["roles"] = []any{map[string]any{"id": sessionWorkspaceID}}
	f.user["currentUserWorkspace"].(map[string]any)["permissionFlags"] = []string{"WORKSPACE_MEMBERS"}
	f.mu.Unlock()
	if err := s.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	id := s.Identity()
	if id.Email != sessionEmail || len(id.RoleIDs) != 1 || id.RoleIDs[0] != sessionRoleID || len(id.PermissionFlags) != 1 || id.PermissionFlags[0] != PermissionFlagTypeRoles {
		t.Fatal("token renewal must preserve the identity snapshot captured during configuration")
	}
	if len(f.ops) != 5 || f.ops[4] != "RenewToken" {
		t.Fatal("token renewal must not claim to refresh the validated workspace/member identity")
	}
}

func TestSessionProactiveRenewalSerialized(t *testing.T) {
	t.Parallel()
	f := newSessionFixture(t)
	s := f.session(t)
	s.refreshAt = time.Now().Add(-time.Second)
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			if _, err := s.GetRoles(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if f.renews != 1 || s.tokens.AccessOrWorkspaceAgnosticToken.Token != "test-access-1" || s.tokens.RefreshToken.Token != "test-refresh-1" {
		t.Fatal("concurrent requests must share one proactive renewal and replace both tokens")
	}
	if !s.refreshAt.Before(s.tokens.AccessOrWorkspaceAgnosticToken.ExpiresAt) || !s.refreshAt.After(time.Now()) {
		t.Fatal("proactive renewal must honor the new access expiry")
	}
}

func TestSessionShortLivedTokens(t *testing.T) {
	t.Parallel()
	now := time.Now()
	s := &Session{now: func() time.Time { return now }}
	pair := TokenPair{AccessOrWorkspaceAgnosticToken: Token{Token: "access", ExpiresAt: now.Add(10 * time.Second)}, RefreshToken: Token{Token: "refresh", ExpiresAt: now.Add(time.Hour)}}
	if err := s.replaceTokens(pair); err != nil || !s.refreshAt.Equal(now.Add(9*time.Second)) {
		t.Fatal("short expiry must use proportional renewal headroom")
	}
	pair.AccessOrWorkspaceAgnosticToken.ExpiresAt = now.Add(time.Hour)
	pair.RefreshToken.ExpiresAt = now.Add(20 * time.Second)
	if err := s.replaceTokens(pair); err != nil || !s.refreshAt.Equal(now.Add(18*time.Second)) {
		t.Fatal("proactive renewal must also honor an earlier refresh-token expiry")
	}
}

func TestSessionRenewalFailureIsNotRetried(t *testing.T) {
	t.Parallel()
	f := newSessionFixture(t)
	s := f.session(t)
	f.override = func(w http.ResponseWriter, op string) bool {
		if op != "RenewToken" {
			return false
		}
		_, _ = fmt.Fprint(w, `{"errors":[{"message":"test-only-password","extensions":{"code":"UNAUTHENTICATED"}}]}`)
		return true
	}
	s.refreshAt = time.Now().Add(-time.Second)
	for range 2 {
		if _, err := s.GetRoles(t.Context()); !errors.Is(err, errAuthentication) {
			t.Fatalf("renewal failure = %v", err)
		}
	}
	count := 0
	for _, op := range f.ops {
		if op == "RenewToken" {
			count++
		}
		if op == "GetRoles" {
			t.Fatal("failed renewal must not send the protected request")
		}
	}
	if count != 1 || s.tokens.RefreshToken.Token != "" {
		t.Fatal("rotating token must not be replayed after failed renewal")
	}
}

func TestSessionMalformedRenewalRequiresReconfigure(t *testing.T) {
	t.Parallel()
	f := newSessionFixture(t)
	s := f.session(t)
	f.override = func(w http.ResponseWriter, op string) bool {
		if op != "RenewToken" {
			return false
		}
		w.WriteHeader(http.StatusOK)
		return true
	}
	// An empty successful response is just as ambiguous as a dropped connection:
	// the server may have rotated the refresh token before the body was lost.
	if err := s.Refresh(t.Context()); !errors.Is(err, errMalformedResponse) {
		t.Fatal("incomplete renewal must fail")
	}
	if err := s.Refresh(t.Context()); !errors.Is(err, errMalformedResponse) || len(f.ops) != 5 {
		t.Fatal("failed rotating-token renewal must require reconfiguration, not replay")
	}
}

func TestSessionExpiredRefreshToken(t *testing.T) {
	t.Parallel()
	f := newSessionFixture(t)
	s := f.session(t)
	s.tokens.RefreshToken.ExpiresAt = time.Now().Add(-time.Second)
	s.refreshAt = time.Now().Add(-time.Second)
	if _, err := s.GetRoles(t.Context()); !errors.Is(err, errSessionExpired) {
		t.Fatal("expired refresh token must require reconfiguration")
	}
	if len(f.ops) != 4 {
		t.Fatal("expired session must not send requests")
	}
}

func TestSessionPermissionFailureNoRetry(t *testing.T) {
	t.Parallel()
	f := newSessionFixture(t)
	s := f.session(t)
	f.override = func(w http.ResponseWriter, op string) bool {
		if op != "GetRoles" {
			return false
		}
		_, _ = fmt.Fprint(w, `{"data":{"getRoles":[]},"errors":[{"message":"test-only-password","extensions":{"code":"FORBIDDEN"}}]}`)
		return true
	}
	response, err := s.GetRoles(t.Context())
	if !errors.Is(err, errPermissionDenied) || len(f.ops) != 5 || f.renews != 0 || response.GetRoles != nil {
		t.Fatal("permission failure must reject partial data without renewal or retry")
	}
}

func TestSessionContextCancellation(t *testing.T) {
	t.Parallel()
	f := newSessionFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := NewSession(ctx, f.server.URL, sessionEmail, sessionPassword, true); !errors.Is(err, context.Canceled) || len(f.ops) != 0 {
		t.Fatal("cancelled authentication must not reach the server")
	}
	s := f.session(t)
	s.gate <- struct{}{}
	ctx, cancel = context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if err := s.Refresh(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("renewal lock must honor context deadline")
	}
	<-s.gate
	if _, err := s.GetRoles(t.Context()); err != nil {
		t.Fatal("cancellation before renewal must not poison the session")
	}
}

func TestSessionMalformedTokens(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"null login":     `{"data":{"getLoginTokenFromCredentials":{"loginToken":null}}}`,
		"missing login":  `{"data":{"getLoginTokenFromCredentials":{}}}`,
		"empty login":    `{"data":{"getLoginTokenFromCredentials":{"loginToken":{"token":"","expiresAt":"2099-01-01T00:00:00Z"}}}}`,
		"expired login":  `{"data":{"getLoginTokenFromCredentials":{"loginToken":{"token":"secret-token","expiresAt":"2000-01-01T00:00:00Z"}}}}`,
		"bad expiry":     `{"data":{"getLoginTokenFromCredentials":{"loginToken":{"token":"secret-token","expiresAt":"secret-token"}}}}`,
		"missing expiry": `{"data":{"getLoginTokenFromCredentials":{"loginToken":{"token":"secret-token"}}}}`,
		"header token":   `{"data":{"getLoginTokenFromCredentials":{"loginToken":{"token":"secret-token\r\nheader","expiresAt":"2099-01-01T00:00:00Z"}}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newSessionFixture(t)
			f.override = func(w http.ResponseWriter, _ string) bool { _, _ = fmt.Fprint(w, body); return true }
			_, err := NewSession(t.Context(), f.server.URL, sessionEmail, sessionPassword, true)
			if !errors.Is(err, errMalformedResponse) || strings.Contains(err.Error(), "secret-token") {
				t.Fatalf("unsafe token error: %v", err)
			}
		})
	}
	for _, field := range []string{"accessOrWorkspaceAgnosticToken", "refreshToken"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			f := newSessionFixture(t)
			f.override = func(w http.ResponseWriter, op string) bool {
				if op != "GetAuthTokensFromLoginToken" {
					return false
				}
				pair := sessionPairFixture(0)
				pair[field] = nil
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"getAuthTokensFromLoginToken": map[string]any{"tokens": pair}}})
				return true
			}
			if _, err := NewSession(t.Context(), f.server.URL, sessionEmail, sessionPassword, true); !errors.Is(err, errMalformedResponse) {
				t.Fatal("both session tokens must be present")
			}
		})
	}
}

func TestSessionIdentityValidation(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		mutate func(map[string]any)
		want   error
	}{
		"unverified":                       {func(u map[string]any) { u["isEmailVerified"] = false }, errEmailUnverified},
		"disabled":                         {func(u map[string]any) { u["disabled"] = true }, errAccountDisabled},
		"password unavailable":             {func(u map[string]any) { u["hasPassword"] = false }, errPasswordDisabled},
		"null workspace":                   {func(u map[string]any) { u["currentWorkspace"] = nil }, errMalformedIdentity},
		"inactive workspace":               {func(u map[string]any) { u["currentWorkspace"].(map[string]any)["activationStatus"] = "SUSPENDED" }, errMalformedIdentity},
		"wrong workspace":                  {func(u map[string]any) { u["currentWorkspace"].(map[string]any)["id"] = sessionRoleID }, errMalformedIdentity},
		"null member":                      {func(u map[string]any) { u["workspaceMember"] = nil }, errMalformedIdentity},
		"null user workspace":              {func(u map[string]any) { u["currentUserWorkspace"] = nil }, errMalformedIdentity},
		"null member user workspace":       {func(u map[string]any) { u["workspaceMember"].(map[string]any)["userWorkspaceId"] = nil }, errMalformedIdentity},
		"mismatched member user workspace": {func(u map[string]any) { u["workspaceMember"].(map[string]any)["userWorkspaceId"] = sessionRoleID }, errMalformedIdentity},
		"mismatched user":                  {func(u map[string]any) { u["workspaceMember"].(map[string]any)["userId"] = sessionRoleID }, errMalformedIdentity},
		"wrong email":                      {func(u map[string]any) { u["email"] = "other@example.invalid" }, errMalformedIdentity},
		"invalid id":                       {func(u map[string]any) { u["id"] = "not-a-uuid" }, errMalformedIdentity},
		"null flags":                       {func(u map[string]any) { u["currentUserWorkspace"].(map[string]any)["permissionFlags"] = nil }, errMalformedIdentity},
		"null roles":                       {func(u map[string]any) { u["workspaceMember"].(map[string]any)["roles"] = nil }, errMalformedIdentity},
		"missing own member":               {func(u map[string]any) { u["workspaceMembers"] = []any{} }, errMalformedIdentity},
		"duplicate own member":             {func(u map[string]any) { u["workspaceMembers"] = []any{u["workspaceMember"], u["workspaceMember"]} }, errMalformedIdentity},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newSessionFixture(t)
			test.mutate(f.user)
			s, err := NewSession(t.Context(), f.server.URL, sessionEmail, sessionPassword, true)
			if s != nil || !errors.Is(err, test.want) || err.Error() != test.want.Error() {
				t.Fatalf("identity failure must use its exact fixed diagnostic; want %v, got %v", test.want, err)
			}
		})
	}
}

func TestSessionIdentityRequiredBooleans(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"isEmailVerified", "hasPassword", "disabled"} {
		for name, mutate := range map[string]func(map[string]any){
			"missing": func(u map[string]any) { delete(u, field) },
			"null":    func(u map[string]any) { u[field] = nil },
			"string":  func(u map[string]any) { u[field] = "false secret-token" },
			"number":  func(u map[string]any) { u[field] = 0 },
			"array":   func(u map[string]any) { u[field] = []bool{false} },
			"object":  func(u map[string]any) { u[field] = map[string]any{"secret-token": false} },
		} {
			t.Run(field+"/"+name, func(t *testing.T) {
				t.Parallel()
				f := newSessionFixture(t)
				mutate(f.user)
				s, err := NewSession(t.Context(), f.server.URL, sessionEmail, sessionPassword, true)
				if s != nil || !errors.Is(err, errMalformedResponse) || err.Error() != errMalformedResponse.Error() || len(f.ops) != 3 {
					t.Fatalf("missing, null, or non-boolean identity data must be rejected before decoding: %v", err)
				}
			})
		}
	}
}

func TestSessionNoRawErrorsEscape(t *testing.T) {
	t.Parallel()
	f := newSessionFixture(t)
	s := f.session(t)
	f.override = func(w http.ResponseWriter, _ string) bool {
		_, _ = fmt.Fprint(w, `{"data":{"getRoles":[]},"extensions":{"secret":"secret-token"},"errors":[{"message":"secret-token","extensions":{"code":"FORBIDDEN"}}]}`)
		return true
	}
	resp := &graphql.Response{Data: &GetRolesResponse{}}
	err := s.MakeRequest(t.Context(), &graphql.Request{OpName: "GetRoles"}, resp)
	if !errors.Is(err, errPermissionDenied) || resp.Errors != nil || resp.Extensions != nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatal("server errors and extensions must not escape the safe transport")
	}
}
