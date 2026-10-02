// SPDX-License-Identifier: MPL-2.0

package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
)

const (
	maxSessionResponseBytes = 4 << 20
	maxSessionRequestBytes  = 1 << 20
)

var (
	errUnsafeEndpoint      = errors.New("twenty endpoint must be an HTTPS instance base URL without credentials, path, query, or fragment; HTTP requires allow_insecure_http and a localhost or loopback address")
	errCredentialsRequired = errors.New("twenty email and password must be nonempty")
	errInvalidEmail        = errors.New("twenty email must be a bare ASCII mailbox address without a display name, comments, or control characters")
	errBadCredentials      = errors.New("twenty rejected the email or password")
	errPasswordDisabled    = errors.New("twenty password authentication is disabled or unavailable for this account")
	errCaptchaRequired     = errors.New("twenty requires CAPTCHA; unattended password authentication is not supported")
	errMFARequired         = errors.New("twenty requires MFA; unattended password authentication is not supported")
	errEmailUnverified     = errors.New("twenty requires a verified automation account email")
	errAccountDisabled     = errors.New("twenty automation account is disabled")
	errPermissionDenied    = errors.New("twenty denied permission for this Metadata operation")
	errMalformedResponse   = errors.New("twenty returned a malformed or incomplete Metadata response")
	errMalformedIdentity   = errors.New("twenty did not return a consistent active workspace and automation member identity")
	errRedirect            = errors.New("twenty returned a redirect; redirects are not permitted")
	errRequestFailed       = errors.New("twenty Metadata request failed; check connectivity and TLS configuration")
	errResponseTooLarge    = errors.New("twenty Metadata response exceeded the size limit")
	errRequestTooLarge     = errors.New("twenty Metadata request exceeded the size limit")
	errSessionExpired      = errors.New("twenty session expired; configure the provider again")
	errRenewalFailed       = errors.New("twenty session renewal failed; configure the provider again")
	errAuthentication      = errors.New("twenty session authentication was rejected; configure the provider again")
	errServer              = errors.New("twenty could not complete the Metadata operation")
)

// DiagnosticMessage returns only canonical messages for known session errors.
// Wrapped errors are matched by errors.Is; all other errors use a fixed fallback.
func DiagnosticMessage(err error) string {
	for _, known := range []error{
		errUnsafeEndpoint, errCredentialsRequired, errInvalidEmail, errBadCredentials,
		errPasswordDisabled, errCaptchaRequired, errMFARequired, errEmailUnverified,
		errAccountDisabled, errPermissionDenied, errMalformedResponse, errMalformedIdentity,
		errRedirect, errRequestFailed, errResponseTooLarge, errRequestTooLarge,
		errSessionExpired, errRenewalFailed, errAuthentication, errServer,
		context.Canceled, context.DeadlineExceeded,
	} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	return errServer.Error()
}

type sessionWireClient struct {
	endpoint   string
	origin     string
	httpClient *http.Client
}

var _ graphql.Client = (*sessionWireClient)(nil)

func newSessionWireClient(endpoint string, allowHTTP bool) (*sessionWireClient, error) {
	origin, err := validateSessionEndpoint(endpoint, allowHTTP)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		// Do not send credentials through an ambient proxy. TLS certificate
		// verification remains enabled, including for HTTPS loopback endpoints.
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			// Resolve deliberate localhost HTTP locally, not through ambient DNS.
			if strings.HasPrefix(origin, "http://") && strings.EqualFold(host, "localhost") {
				address = net.JoinHostPort("127.0.0.1", port)
			}
			return dialer.DialContext(ctx, network, address)
		},
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: sessionTimeout,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          10,
	}
	return &sessionWireClient{
		endpoint: origin + "/metadata", origin: origin,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   sessionTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func validateSessionEndpoint(endpoint string, allowHTTP bool) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	u, err := url.Parse(endpoint)
	if err != nil || u.Opaque != "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery ||
		strings.Contains(endpoint, "#") || u.RawPath != "" || u.Path != "" && u.Path != "/" ||
		u.Scheme != "https" && u.Scheme != "http" {
		return "", errUnsafeEndpoint
	}
	host := u.Hostname()
	if host == "" || strings.ContainsAny(host, "%\\") || strings.HasSuffix(u.Host, ":") {
		return "", errUnsafeEndpoint
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", errUnsafeEndpoint
		}
	}
	addr, ipErr := netip.ParseAddr(host)
	if ipErr != nil && (!validSessionHostname(host) || strings.ContainsAny(u.Host, "[]")) {
		return "", errUnsafeEndpoint
	}
	if u.Scheme == "http" && (!allowHTTP || !strings.EqualFold(host, "localhost") && (ipErr != nil || !addr.Unmap().IsLoopback())) {
		return "", errUnsafeEndpoint
	}
	u.Path = ""
	return u.String(), nil
}

func validSessionHostname(host string) bool {
	if len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func (c *sessionWireClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	return c.makeRequest(ctx, req, resp, "")
}

func (c *sessionWireClient) makeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response, token string) error {
	ctx, cancel := context.WithTimeout(ctx, sessionTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	if req == nil || resp == nil || resp.Data == nil || req.OpName == "" {
		return errMalformedResponse
	}
	body, err := json.Marshal(req)
	if err != nil {
		return errMalformedResponse
	}
	if len(body) > maxSessionRequestBytes {
		return errRequestTooLarge
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return errRequestFailed
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Origin", c.origin)
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}
	result, err := c.httpClient.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errRequestFailed
	}
	defer func() { _ = result.Body.Close() }()
	if result.StatusCode >= 300 && result.StatusCode < 400 {
		return errRedirect
	}
	data, err := io.ReadAll(io.LimitReader(result.Body, maxSessionResponseBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errRequestFailed
	}
	if len(data) > maxSessionResponseBytes {
		return errResponseTooLarge
	}
	// Decode the transport envelope separately so neither server errors nor
	// extensions can escape through graphql.Response or an error's text.
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message    string `json:"message"`
			Extensions struct {
				Code    string `json:"code"`
				SubCode string `json:"subCode"`
			} `json:"extensions"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		if result.StatusCode == http.StatusUnauthorized {
			return sessionStatusError(req.OpName, false)
		}
		if result.StatusCode == http.StatusForbidden {
			return sessionStatusError(req.OpName, true)
		}
		if result.StatusCode != http.StatusOK {
			return errServer
		}
		return errMalformedResponse
	}
	for _, e := range envelope.Errors {
		for _, code := range []string{e.Extensions.SubCode, e.Extensions.Code} {
			switch code {
			case "EMAIL_NOT_VERIFIED":
				return errEmailUnverified
			case "INVALID_CAPTCHA", "CAPTCHA_REQUIRED":
				return errCaptchaRequired
			case "TWO_FACTOR_AUTHENTICATION_PROVISION_REQUIRED", "TWO_FACTOR_AUTHENTICATION_VERIFICATION_REQUIRED", "MFA_REQUIRED":
				return errMFARequired
			case "PASSWORD_AUTH_DISABLED", "USE_SSO_AUTH":
				return errPasswordDisabled
			}
		}
		// This server uses generic FORBIDDEN/INVALID_INPUT codes for these two
		// cases. Match the pinned messages but only return our fixed text.
		switch e.Message {
		case "Email/Password auth is not enabled for this workspace", "Incorrect login method":
			return errPasswordDisabled
		}
	}
	if len(envelope.Errors) > 0 {
		for _, e := range envelope.Errors {
			if e.Extensions.Code == "INTERNAL_SERVER_ERROR" {
				return errServer
			}
		}
		if req.OpName == "GetLoginTokenFromCredentials" {
			return errBadCredentials
		}
		for _, e := range envelope.Errors {
			for _, code := range []string{e.Extensions.SubCode, e.Extensions.Code} {
				switch code {
				case "UNAUTHENTICATED", "APPLICATION_REFRESH_TOKEN_INVALID_OR_EXPIRED":
					return errAuthentication
				case "FORBIDDEN", "FORBIDDEN_EXCEPTION", "INSUFFICIENT_SCOPES":
					return errPermissionDenied
				}
			}
		}
		return errServer
	}
	if result.StatusCode == http.StatusUnauthorized || result.StatusCode == http.StatusForbidden {
		return sessionStatusError(req.OpName, result.StatusCode == http.StatusForbidden)
	}
	if result.StatusCode != http.StatusOK {
		return errServer
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Data, &fields); err != nil || fields == nil {
		return errMalformedResponse
	}
	field := strings.ToLower(req.OpName[:1]) + req.OpName[1:]
	value, ok := fields[field]
	if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return errMalformedResponse
	}
	if req.OpName == "CurrentUser" {
		if err := validateCurrentUserResponse(value); err != nil {
			return err
		}
	}
	if err := json.Unmarshal(envelope.Data, resp.Data); err != nil {
		return errMalformedResponse
	}
	return nil
}

func validateCurrentUserResponse(value json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(value, &fields); err != nil || fields == nil {
		return errMalformedResponse
	}
	// Genqlient's non-null bool fields would otherwise decode missing/null as
	// false and misreport incomplete identity data as an account restriction.
	for _, field := range []string{"isEmailVerified", "hasPassword", "disabled"} {
		switch string(bytes.TrimSpace(fields[field])) {
		case "true", "false":
		default:
			return errMalformedResponse
		}
	}
	return nil
}

func sessionStatusError(operation string, forbidden bool) error {
	if operation == "GetLoginTokenFromCredentials" {
		return errBadCredentials
	}
	if forbidden {
		return errPermissionDenied
	}
	return errAuthentication
}
