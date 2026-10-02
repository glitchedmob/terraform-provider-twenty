// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Khan/genqlient/graphql"
)

func TestDiagnosticMessage(t *testing.T) {
	t.Parallel()
	for _, known := range []error{
		errUnsafeEndpoint, errCredentialsRequired, errInvalidEmail, errBadCredentials,
		errPasswordDisabled, errCaptchaRequired, errMFARequired, errEmailUnverified,
		errAccountDisabled, errPermissionDenied, errMalformedResponse, errMalformedIdentity,
		errRedirect, errRequestFailed, errResponseTooLarge, errRequestTooLarge,
		errSessionExpired, errRenewalFailed, errAuthentication, errServer,
		context.Canceled, context.DeadlineExceeded,
	} {
		t.Run(known.Error(), func(t *testing.T) {
			t.Parallel()
			for _, err := range []error{known, fmt.Errorf("secret-token and private-email@example.invalid: %w", known)} {
				if got := DiagnosticMessage(err); got != known.Error() {
					t.Fatalf("known errors must use their canonical diagnostic, got %q", got)
				}
			}
		})
	}
	for _, unknown := range []error{
		nil, errors.New("secret-token private-email@example.invalid"),
		fmt.Errorf("secret-token: %w", errors.New("raw server error")),
		errors.New(errPermissionDenied.Error()), errors.New("context canceled secret-token"),
	} {
		if got := DiagnosticMessage(unknown); got != errServer.Error() {
			t.Fatalf("unknown errors must use the safe fallback, got %q", got)
		}
	}
	if got := DiagnosticMessage(errors.Join(errors.New("secret-token"), errPermissionDenied)); got != errPermissionDenied.Error() {
		t.Fatalf("joined errors must not expose unrelated raw text, got %q", got)
	}
}

func TestSessionEndpointValidation(t *testing.T) {
	t.Parallel()
	for _, endpoint := range []string{
		"", "twenty.example.invalid", "//twenty.example.invalid", "ftp://twenty.example.invalid",
		"https://", "https:opaque", "https://user:secret@twenty.example.invalid",
		"https://twenty.example.invalid/metadata", "https://twenty.example.invalid/a/..",
		"https://twenty.example.invalid//", "https://twenty.example.invalid/%2f",
		"https://twenty.example.invalid?", "https://twenty.example.invalid?q=secret",
		"https://twenty.example.invalid#", "https://twenty.example.invalid#secret",
		"https://twenty.example.invalid:", "https://twenty.example.invalid:0", "https://twenty.example.invalid:65536",
		"https://twenty.example.invalid:abc", "https://-unsafe.example.invalid", "https://unsafe..example.invalid",
		"https://[::1%25eth0]", "http://twenty.example.invalid", "http://localhost.example.invalid",
		"http://localhost.", "http://127.0.0.1.example.invalid", "http://0.0.0.0", "http://192.168.1.1",
		"http://[::]", "http://[fe80::1]", "http://2130706433", "http://127.1",
	} {
		t.Run(endpoint, func(t *testing.T) {
			t.Parallel()
			if _, err := validateSessionEndpoint(endpoint, true); !errors.Is(err, errUnsafeEndpoint) || strings.Contains(err.Error(), "secret@") {
				t.Fatalf("unsafe endpoint was not rejected safely: %v", err)
			}
		})
	}
	for _, endpoint := range []string{"http://localhost:3000", "http://LOCALHOST:3000", "http://127.0.0.1", "http://127.0.0.2:3000", "http://[::1]:3000"} {
		t.Run(endpoint, func(t *testing.T) {
			t.Parallel()
			if _, err := validateSessionEndpoint(endpoint, false); err == nil {
				t.Fatal("HTTP must require deliberate opt-in")
			}
			if got, err := validateSessionEndpoint(endpoint+"/", true); err != nil || got != endpoint {
				t.Fatalf("literal loopback or localhost should work only with opt-in: %v", err)
			}
		})
	}
	if got, err := validateSessionEndpoint(" https://twenty.example.invalid:443/ ", false); err != nil || got != "https://twenty.example.invalid:443" {
		t.Fatal("safe HTTPS instance root must preserve origin and normalize trailing slash")
	}
}

func TestSessionTransportDefaults(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://unsafe-proxy.example.invalid")
	t.Setenv("HTTPS_PROXY", "http://unsafe-proxy.example.invalid")
	wire, err := newSessionWireClient("https://twenty.example.invalid", false)
	if err != nil {
		t.Fatal(err)
	}
	transport := wire.httpClient.Transport.(*http.Transport)
	if transport.Proxy != nil || transport.TLSClientConfig.InsecureSkipVerify || transport.TLSHandshakeTimeout <= 0 ||
		transport.ResponseHeaderTimeout <= 0 || wire.httpClient.Timeout <= 0 || wire.httpClient.CheckRedirect == nil {
		t.Fatal("transport must not use ambient proxies, skip TLS validation, or omit timeout and redirect limits")
	}
	if wire.endpoint != "https://twenty.example.invalid/metadata" || wire.origin != "https://twenty.example.invalid" {
		t.Fatal("transport must use only the Metadata endpoint with the configured origin")
	}
}

func TestSessionTLSValidation(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("untrusted TLS must not receive credentials")
	}))
	defer server.Close()
	s, err := NewSession(t.Context(), server.URL, sessionEmail, sessionPassword, true)
	if s != nil || !errors.Is(err, errRequestFailed) || strings.Contains(err.Error(), server.URL) {
		t.Fatal("local HTTP opt-in must not disable HTTPS certificate verification")
	}
}

func TestSessionRedirectsNeverFollowed(t *testing.T) {
	t.Parallel()
	for _, status := range []int{301, 302, 303, 307, 308} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			t.Parallel()
			target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Error("redirect must never receive password or token")
			}))
			defer target.Close()
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", target.URL+"/secret-token")
				w.WriteHeader(status)
			}))
			defer source.Close()
			if _, err := NewSession(t.Context(), source.URL, sessionEmail, sessionPassword, true); !errors.Is(err, errRedirect) {
				t.Fatal("redirect must produce a fixed error without following it")
			}
		})
	}
}

func TestSessionSanitizedAuthenticationFailures(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		status int
		body   string
		want   error
	}{
		"password disabled": {200, `{"errors":[{"message":"Email/Password auth is not enabled for this workspace","extensions":{"code":"FORBIDDEN"}}]}`, errPasswordDisabled},
		"wrong method":      {200, `{"errors":[{"message":"Incorrect login method"}]}`, errPasswordDisabled},
		"captcha":           {200, `{"errors":[{"message":"secret-token","extensions":{"code":"BAD_USER_INPUT","subCode":"INVALID_CAPTCHA"}}]}`, errCaptchaRequired},
		"mfa setup":         {200, `{"errors":[{"message":"secret-token","extensions":{"code":"FORBIDDEN","subCode":"TWO_FACTOR_AUTHENTICATION_PROVISION_REQUIRED"}}]}`, errMFARequired},
		"mfa verify":        {200, `{"errors":[{"message":"secret-token","extensions":{"code":"FORBIDDEN","subCode":"TWO_FACTOR_AUTHENTICATION_VERIFICATION_REQUIRED"}}]}`, errMFARequired},
		"unverified":        {200, `{"errors":[{"message":"secret-token","extensions":{"subCode":"EMAIL_NOT_VERIFIED"}}]}`, errEmailUnverified},
		"bad password":      {200, `{"errors":[{"message":"secret-token","extensions":{"code":"FORBIDDEN"}}]}`, errBadCredentials},
		"bad user":          {200, `{"errors":[{"message":"secret-token","extensions":{"subCode":"USER_NOT_FOUND","code":"UNAUTHENTICATED"}}]}`, errBadCredentials},
		"unauthorized":      {401, `secret-token`, errBadCredentials},
		"forbidden":         {403, `secret-token`, errBadCredentials},
		"server error":      {500, `secret-token`, errServer},
		"malformed":         {200, `secret-token`, errMalformedResponse},
		"empty response":    {200, `{}`, errMalformedResponse},
		"null data":         {200, `{"data":null}`, errMalformedResponse},
		"null operation":    {200, `{"data":{"getLoginTokenFromCredentials":null}}`, errMalformedResponse},
		"null envelope":     {200, `null`, errMalformedResponse},
		"trailing data":     {200, `{} {"secret":"secret-token"}`, errMalformedResponse},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			s, err := NewSession(t.Context(), server.URL, sessionEmail, sessionPassword, true)
			if s != nil || !errors.Is(err, test.want) || strings.Contains(err.Error(), "secret-token") || strings.Contains(err.Error(), sessionEmail) {
				t.Fatalf("expected sanitized error %v, got %v", test.want, err)
			}
		})
	}
}

func TestSessionResponseBodyLimit(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("s", maxSessionResponseBytes+1))
	}))
	defer server.Close()
	if _, err := NewSession(t.Context(), server.URL, sessionEmail, sessionPassword, true); !errors.Is(err, errResponseTooLarge) {
		t.Fatal("response bodies must have a finite read limit")
	}
}

func TestSessionRequestBodyLimit(t *testing.T) {
	t.Parallel()
	wire, err := newSessionWireClient("http://127.0.0.1:1", true)
	if err != nil {
		t.Fatal(err)
	}
	request := &graphql.Request{OpName: "GetRoles", Variables: strings.Repeat("s", maxSessionRequestBytes+1)}
	if err := wire.MakeRequest(t.Context(), request, &graphql.Response{Data: &GetRolesResponse{}}); !errors.Is(err, errRequestTooLarge) {
		t.Fatal("oversized requests must fail before reaching a transport")
	}
}

func TestSessionNetworkDeadline(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	_, err := NewSession(ctx, server.URL, sessionEmail, sessionPassword, true)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("body read must obey caller deadline: %v", err)
	}
}

func TestSessionEmptyCredentials(t *testing.T) {
	t.Parallel()
	for _, credentials := range [][2]string{{"", "test"}, {sessionEmail, ""}, {sessionEmail, " \t"}} {
		if _, err := NewSession(t.Context(), "http://127.0.0.1:1", credentials[0], credentials[1], true); !errors.Is(err, errCredentialsRequired) {
			t.Fatal("empty credentials must fail without network access")
		}
	}
}
