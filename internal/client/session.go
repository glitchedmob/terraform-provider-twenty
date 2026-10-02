// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"errors"
	"net/mail"
	"slices"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
)

const sessionTimeout = 30 * time.Second

// Identity is the validated automation account and its current workspace membership.
// It contains no credentials or tokens.
type Identity struct {
	UserID            string
	Email             string
	WorkspaceID       string
	WorkspaceMemberID string
	UserWorkspaceID   string
	RoleIDs           []string
	PermissionFlags   []PermissionFlagType
}

// Session keeps rotating credentials in memory. Its gate serializes requests and
// renewal so a rotating refresh token is never consumed concurrently.
type Session struct {
	wire       *sessionWireClient
	gate       chan struct{}
	tokens     TokenPair
	refreshAt  time.Time
	identity   Identity
	renewError error
	now        func() time.Time
}

var _ graphql.Client = (*Session)(nil)

// NewSession authenticates with password credentials and validates the workspace
// and member before returning a client. HTTP requires explicit local-only consent.
func NewSession(ctx context.Context, endpoint, email, password string, allowHTTP bool) (*Session, error) {
	wire, err := newSessionWireClient(endpoint, allowHTTP)
	if err != nil {
		return nil, err
	}
	return newSession(ctx, wire, email, password)
}

// ValidateEmail accepts one bare ASCII mailbox, with optional surrounding spaces.
// It performs no DNS lookup and never returns the supplied value in an error.
func ValidateEmail(email string) error {
	for _, c := range email {
		if c < ' ' || c >= 127 {
			return errInvalidEmail
		}
	}
	mailbox := strings.TrimSpace(email)
	address, err := mail.ParseAddress(mailbox)
	if err != nil || address.Name != "" || address.Address != mailbox {
		return errInvalidEmail
	}
	return nil
}

func newSession(ctx context.Context, wire *sessionWireClient, email, password string) (*Session, error) {
	if strings.TrimSpace(email) == "" || strings.TrimSpace(password) == "" {
		return nil, errCredentialsRequired
	}
	if err := ValidateEmail(email); err != nil {
		return nil, err
	}
	email = strings.TrimSpace(email)
	ctx, cancel := context.WithTimeout(ctx, sessionTimeout)
	defer cancel()
	login, err := GetLoginTokenFromCredentials(ctx, wire, email, password, wire.origin, nil, nil, nil)
	if err != nil {
		return nil, err
	}
	if !validSessionToken(login.GetLoginTokenFromCredentials.LoginToken, time.Now()) {
		return nil, errMalformedResponse
	}
	exchange, err := GetAuthTokensFromLoginToken(ctx, wire, login.GetLoginTokenFromCredentials.LoginToken.Token, wire.origin)
	if err != nil {
		return nil, err
	}
	s := &Session{wire: wire, gate: make(chan struct{}, 1), now: time.Now}
	if err := s.replaceTokens(exchange.GetAuthTokensFromLoginToken.Tokens.TokenPair); err != nil {
		return nil, err
	}
	user, err := CurrentUser(ctx, s)
	if err != nil {
		return nil, err
	}
	workspace, err := CurrentWorkspace(ctx, s)
	if err != nil {
		return nil, err
	}
	s.identity, err = validateSessionIdentity(user.CurrentUser, workspace.CurrentWorkspace.WorkspaceIdentity, email)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// Client exposes the authenticated Metadata GraphQL client for generated operations.
func (s *Session) Client() graphql.Client { return s }

// GraphQL is an alias for Client.
func (s *Session) GraphQL() graphql.Client { return s }

// Identity returns a copy of the validated identity captured during configuration.
func (s *Session) Identity() Identity {
	id := s.identity
	id.RoleIDs = slices.Clone(id.RoleIDs)
	id.PermissionFlags = slices.Clone(id.PermissionFlags)
	return id
}

// GetRoles reads workspace roles through the generated Metadata operation.
func (s *Session) GetRoles(ctx context.Context) (*GetRolesResponse, error) {
	return GetRoles(ctx, s)
}

// Refresh explicitly renews the session using the server-issued refresh token.
// It is useful for long-running callers and never signs or fabricates credentials.
func (s *Session) Refresh(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, sessionTimeout)
	defer cancel()
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	return s.renew(ctx)
}

// MakeRequest renews proactively, then sends the request once. Permission and
// authentication failures never cause a blind retry of the operation.
func (s *Session) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	ctx, cancel := context.WithTimeout(ctx, sessionTimeout)
	defer cancel()
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	if s.renewError != nil {
		return s.renewError
	}
	if !s.now().Before(s.refreshAt) {
		if err := s.renew(ctx); err != nil {
			return err
		}
	}
	return s.wire.makeRequest(ctx, req, resp, s.tokens.AccessOrWorkspaceAgnosticToken.Token)
}

func (s *Session) lock(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case s.gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			s.unlock()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Session) unlock() { <-s.gate }

func (s *Session) renew(ctx context.Context) error {
	if s.renewError != nil {
		return s.renewError
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validSessionToken(s.tokens.RefreshToken, s.now()) {
		s.renewError = errSessionExpired
		s.tokens = TokenPair{}
		return s.renewError
	}
	result, err := RenewToken(ctx, s.wire, s.tokens.RefreshToken.Token)
	if err == nil {
		err = s.replaceTokens(result.RenewToken.Tokens.TokenPair)
	}
	if err != nil {
		// The server may have consumed the rotating token even if the response was
		// lost. Require reconfiguration instead of replaying it on another call.
		s.renewError = errRenewalFailed
		s.tokens = TokenPair{}
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			s.renewError = err
		}
		return err
	}
	return nil
}

func (s *Session) replaceTokens(pair TokenPair) error {
	now := s.now()
	if !validSessionToken(pair.AccessOrWorkspaceAgnosticToken, now) || !validSessionToken(pair.RefreshToken, now) {
		return errMalformedResponse
	}
	// Short-lived tokens need proportionally smaller headroom; a fixed skew
	// would otherwise renew a valid short-lived token on every request.
	expiry := pair.AccessOrWorkspaceAgnosticToken.ExpiresAt
	if pair.RefreshToken.ExpiresAt.Before(expiry) {
		expiry = pair.RefreshToken.ExpiresAt
	}
	lead := min(30*time.Second, expiry.Sub(now)/10)
	s.tokens = pair
	s.refreshAt = expiry.Add(-lead)
	return nil
}

func validSessionToken(token Token, now time.Time) bool {
	if token.Token == "" || len(token.Token) > 64*1024 || !token.ExpiresAt.After(now) {
		return false
	}
	for _, c := range token.Token {
		if c <= ' ' || c >= 127 {
			return false
		}
	}
	return true
}

func validateSessionIdentity(user CurrentUserCurrentUser, workspace WorkspaceIdentity, email string) (Identity, error) {
	if !user.IsEmailVerified {
		return Identity{}, errEmailUnverified
	}
	disabled, err := user.Disabled.Get()
	if err != nil {
		return Identity{}, errMalformedIdentity
	}
	if disabled {
		return Identity{}, errAccountDisabled
	}
	if !user.HasPassword {
		return Identity{}, errPasswordDisabled
	}
	currentWorkspace, err := user.CurrentWorkspace.Get()
	if err != nil || !validSessionID(currentWorkspace.Id) || currentWorkspace.ActivationStatus != WorkspaceActivationStatusActive ||
		workspace.Id != currentWorkspace.Id || workspace.ActivationStatus != WorkspaceActivationStatusActive {
		return Identity{}, errMalformedIdentity
	}
	uw, err := user.CurrentUserWorkspace.Get()
	if err != nil || !validSessionID(uw.Id) || uw.UserId != user.Id {
		return Identity{}, errMalformedIdentity
	}
	member, err := user.WorkspaceMember.Get()
	if err != nil || !validSessionID(member.Id) || member.UserId != user.Id || !strings.EqualFold(member.UserEmail, email) {
		return Identity{}, errMalformedIdentity
	}
	memberUW, err := member.UserWorkspaceId.Get()
	if err != nil || memberUW != uw.Id || !validSessionID(user.Id) || !strings.EqualFold(user.Email, email) {
		return Identity{}, errMalformedIdentity
	}
	matches := 0
	for _, m := range user.WorkspaceMembers {
		if m.Id != member.Id {
			continue
		}
		id, err := m.UserWorkspaceId.Get()
		if err != nil || id != uw.Id || m.UserId != user.Id || !strings.EqualFold(m.UserEmail, email) {
			return Identity{}, errMalformedIdentity
		}
		matches++
	}
	if matches != 1 || len(member.Roles) == 0 || uw.PermissionFlags == nil {
		return Identity{}, errMalformedIdentity
	}
	roleIDs := make([]string, 0, len(member.Roles))
	for _, role := range member.Roles {
		if !validSessionID(role.Id) || slices.Contains(roleIDs, role.Id) {
			return Identity{}, errMalformedIdentity
		}
		roleIDs = append(roleIDs, role.Id)
	}
	return Identity{
		UserID: user.Id, Email: user.Email, WorkspaceID: workspace.Id,
		WorkspaceMemberID: member.Id, UserWorkspaceID: uw.Id,
		RoleIDs: roleIDs, PermissionFlags: slices.Clone(uw.PermissionFlags),
	}, nil
}

func validSessionID(id string) bool {
	if len(id) != 36 || id == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i, c := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' {
			continue
		} else {
			return false
		}
	}
	return true
}
