// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type blockedRoleClient struct {
	*roleMock
	first       sync.Once
	entered     chan struct{}
	release     chan struct{}
	safetyReads chan struct{}
}

func (c *blockedRoleClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	if req.OpName == "CurrentUser" {
		c.safetyReads <- struct{}{}
	}
	if req.OpName == "CreateOneRole" {
		c.first.Do(func() { close(c.entered); <-c.release })
	}
	return c.roleMock.MakeRequest(ctx, req, resp)
}
func TestRoleMutationLockCoversFreshReadsAndWrites(t *testing.T) {
	api := &blockedRoleClient{roleMock: newRoleMock(), entered: make(chan struct{}), release: make(chan struct{}), safetyReads: make(chan struct{}, 2)}
	first := mockRoleResource(api.roleMock)
	first.client = api
	second := mockRoleResource(api.roleMock)
	second.client = api
	second.mutationLock = first.mutationLock
	plan := roleResourceTestModel()
	plan.ID = types.StringUnknown()
	firstPlan := roleResourcePlan(t, plan)
	firstState := roleResourceState(t, plan)
	plan.Label = types.StringValue("Second role")
	secondPlan := roleResourcePlan(t, plan)
	secondState := roleResourceState(t, plan)
	responses := make(chan resource.CreateResponse, 2)
	go func() {
		resp := resource.CreateResponse{State: firstState}
		first.Create(t.Context(), resource.CreateRequest{Plan: firstPlan}, &resp)
		responses <- resp
	}()
	select {
	case <-api.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first mutation did not start")
	}
	<-api.safetyReads
	started := make(chan struct{})
	go func() {
		close(started)
		resp := resource.CreateResponse{State: secondState}
		second.Create(t.Context(), resource.CreateRequest{Plan: secondPlan}, &resp)
		responses <- resp
	}()
	<-started
	select {
	case <-api.safetyReads:
		close(api.release)
		t.Fatal("second resource read safety data outside the shared mutation lock")
	case <-time.After(25 * time.Millisecond):
	}
	close(api.release)
	for range 2 {
		select {
		case resp := <-responses:
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("serialized role creation did not finish")
		}
	}
	select {
	case <-api.safetyReads:
	default:
		t.Fatal("second resource did not obtain its own fresh safety data")
	}
}
