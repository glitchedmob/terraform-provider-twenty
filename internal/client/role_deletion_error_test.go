// SPDX-License-Identifier: MPL-2.0

package client

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
)

func TestRoleDeletionCacheErrorIsOperationScopedAndSanitized(t *testing.T) {
	const nativeID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	for _, op := range []string{"DeleteOneRole", "UpdateWorkspaceMemberRole", "DeleteUserFromWorkspace", "GetRoles"} {
		for _, message := range []string{"User workspaces not found", "User workspaces not found: " + nativeID, "User workspaces not found: " + nativeID + ", " + nativeID, "User workspaces not found: secret-token private@example.invalid", "prefix User workspaces not found", "User workspaces not found: " + nativeID + " secret-token"} {
			t.Run(op+"/"+message, func(t *testing.T) {
				requests := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					requests++
					_ = json.NewEncoder(w).Encode(map[string]any{"errors": []any{map[string]any{"message": message, "extensions": map[string]any{"code": "INTERNAL_SERVER_ERROR", "private": "secret-token"}}}})
				}))
				defer server.Close()
				wire, err := newSessionWireClient(server.URL, true)
				if err != nil {
					t.Fatal(err)
				}
				var data any
				err = wire.MakeRequest(t.Context(), &graphql.Request{OpName: op, Query: "mutation { ignored }"}, &graphql.Response{Data: &data})
				want := errServer
				if op == "DeleteOneRole" && (message == "User workspaces not found" || message == "User workspaces not found: "+nativeID || message == "User workspaces not found: "+nativeID+", "+nativeID) {
					want = errRoleDeletionCache
				}
				if !errors.Is(err, want) || DiagnosticMessage(err) != want.Error() || requests != 1 {
					t.Fatal("wrong classification, raw upstream diagnostic, or automatic retry")
				}
				for _, private := range []string{nativeID, "secret-token", "private@example.invalid"} {
					if strings.Contains(err.Error(), private) || strings.Contains(DiagnosticMessage(err), private) {
						t.Fatal("role deletion leaked upstream membership data")
					}
				}
			})
		}
	}
}
