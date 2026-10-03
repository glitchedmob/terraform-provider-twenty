// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/glitchedmob/terraform-provider-twenty/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

const pinnedRoleDeletionCacheDiagnostic = "twenty v2.44 could not delete the role because its role-assignment cache references missing workspace memberships. Membership removal may already have succeeded. The role remains managed in state; inspect current membership and role state and resolve the upstream cache defect before another teardown attempt. No automatic retry or assignment repair was attempted"

type roleDeletionErrorClient struct {
	mock    *roleMock
	session *client.Session
}

func (c roleDeletionErrorClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	if req.OpName == "DeleteOneRole" {
		return c.session.MakeRequest(ctx, req, resp)
	}
	return c.mock.MakeRequest(ctx, req, resp)
}

func TestRoleDeletionCacheFailureRetainsStateAndNeverRetries(t *testing.T) {
	auth, _ := providerAuthFixture(t)
	deletions := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req graphql.Request
		_ = json.Unmarshal(body, &req)
		if req.OpName == "DeleteOneRole" {
			deletions++
			_ = json.NewEncoder(w).Encode(map[string]any{"errors": []any{map[string]any{"message": "User workspaces not found: " + memberMembershipID, "extensions": map[string]any{"code": "INTERNAL_SERVER_ERROR", "private": "secret-token"}}}})
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		auth.Config.Handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	session, err := client.NewSession(t.Context(), server.URL, "automation@example.invalid", " test-provider-password ", true)
	if err != nil {
		t.Fatal("authenticate synthetic role deletion session")
	}
	mock := newRoleMock()
	r := mockRoleResource(mock)
	r.client = roleDeletionErrorClient{mock: mock, session: session}
	state := roleResourceState(t, roleResourceTestModel())
	for attempt := 1; attempt <= 2; attempt++ {
		deleted := resource.DeleteResponse{State: state}
		r.Delete(t.Context(), resource.DeleteRequest{State: state}, &deleted)
		if !deleted.Diagnostics.HasError() || !deleted.State.Raw.Equal(state.Raw) || deletions != attempt || len(mock.roles) != 2 {
			t.Fatal("failed deletion lost role state/server identity or retried automatically")
		}
		detail := deleted.Diagnostics[0].Detail()
		if detail != pinnedRoleDeletionCacheDiagnostic || strings.Contains(detail, memberMembershipID) || strings.Contains(detail, "secret-token") || strings.Contains(detail, "User workspaces not found") {
			t.Fatal("role cache error did not provide fixed sanitized recovery guidance")
		}
		refreshed := resource.ReadResponse{State: deleted.State}
		r.Read(t.Context(), resource.ReadRequest{State: deleted.State}, &refreshed)
		var model roleResourceModel
		d := refreshed.State.Get(t.Context(), &model)
		if refreshed.Diagnostics.HasError() || d.HasError() || model.ID.ValueString() != roleTestID {
			t.Fatal("refresh lost the role retained after failed deletion")
		}
	}
}
