// SPDX-License-Identifier: MPL-2.0

package acceptance

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
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

type failingClient struct{ err error }

func (f failingClient) MakeRequest(context.Context, *graphql.Request, *graphql.Response) error {
	return f.err
}

func TestBootstrapErrorRedaction(t *testing.T) {
	for _, upstream := range []error{
		errors.New("password=secret-token user@example.invalid"),
		errors.New("FORBIDDEN: password=secret-token user@example.invalid"),
		&graphql.HTTPError{StatusCode: 401},
	} {
		safe := (&sanitizedClient{base: failingClient{err: upstream}}).MakeRequest(t.Context(), &graphql.Request{}, &graphql.Response{})
		if safe == nil || strings.Contains(safe.Error(), "secret-token") || strings.Contains(safe.Error(), "user@example") {
			t.Fatal("bootstrap error failed redaction")
		}
	}
}

func TestSinkRejectsInvalidURLWithoutEcho(t *testing.T) {
	err := sinkJSON(t.Context(), "://token=secret", nil)
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("invalid sink URL was not rejected safely")
	}
}
