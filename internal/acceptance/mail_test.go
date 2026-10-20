// SPDX-License-Identifier: MPL-2.0

package acceptance

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func TestTokenFromMail(t *testing.T) {
	for _, tc := range []struct {
		name, body, parameter, want string
	}{
		{"verification", `Verify https://127.0.0.1/verify?email=a%40example.com&emailVerificationToken=server-issued`, "emailVerificationToken", "server-issued"},
		{"html invitation", `<a href="http://127.0.0.1/invite/hash?email=a%40example.com&amp;inviteToken=personal%2Btoken">Join</a>`, "inviteToken", "personal+token"},
		{"wrong purpose", `http://127.0.0.1/invite?inviteToken=personal`, "emailVerificationToken", ""},
		{"no link", "no token", "inviteToken", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tokenFromMail(tc.body, tc.parameter); got != tc.want {
				t.Fatalf("token extraction returned %q, want %q", got, tc.want)
			}
		})
	}
}

type failingClient struct {
	err    error
	errors gqlerror.List
}

func (f failingClient) MakeRequest(_ context.Context, _ *graphql.Request, response *graphql.Response) error {
	response.Errors = f.errors
	return f.err
}

func TestBootstrapErrorRedaction(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		err     error
		graphql bool
		want    string
	}{
		{"unknown", errors.New("password=secret-token user@example.invalid"), false, "disposable Metadata operation failed; upstream details withheld"},
		{"known code", errors.New("FORBIDDEN: password=secret-token user@example.invalid"), true, "disposable Metadata operation rejected: FORBIDDEN"},
		{"known cache error", errors.New("User workspaces not found: secret-token"), true, "disposable Metadata operation failed: User workspaces not found"},
		{"unknown GraphQL", errors.New("unknown field secret-token user@example.invalid"), true, "disposable Metadata GraphQL error; upstream details withheld"},
		{"HTTP", &graphql.HTTPError{StatusCode: 401}, false, "disposable Metadata operation returned HTTP 401; upstream details withheld"},
		{"GraphQL before HTTP", &graphql.HTTPError{StatusCode: 500}, true, "disposable Metadata GraphQL error; upstream details withheld"},
		{"known fallback", errors.New("Cannot query field secret-token"), false, "disposable Metadata operation failed: Cannot query field"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			base := failingClient{err: test.err}
			if test.graphql {
				base.errors = gqlerror.List{{
					Message:    "private-message secret-token user@example.invalid",
					Path:       ast.Path{ast.PathName("private-path"), ast.PathIndex(0)},
					Extensions: map[string]any{"private-extension": "secret-token"},
				}}
			}
			req := &graphql.Request{Query: "query Bootstrap { workspace { id } }", Variables: map[string]any{"password": "secret-token", "email": "user@example.invalid"}}
			safe := (&sanitizedClient{base: base}).MakeRequest(t.Context(), req, &graphql.Response{})
			if safe == nil || safe.Error() != test.want {
				t.Fatalf("sanitized error = %v, want %q", safe, test.want)
			}
		})
	}
}

func TestSinkRejectsInvalidURLWithoutEcho(t *testing.T) {
	err := sinkJSON(t.Context(), "://token=secret", nil)
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("invalid sink URL was not rejected safely")
	}
}
