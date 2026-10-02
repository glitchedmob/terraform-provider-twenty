# Development plan and handoff

Work in separate, serial stages. This checklist is a plan, not a claim that Twenty operations have been tested.

## Stage 1: scaffold

- [x] Public repository and Go module layout matching the Kaneo provider.
- [x] Plugin Framework entrypoint, metadata, configuration schema, and unit tests.
- [x] Environment fallback names `TWENTY_ENDPOINT`, `TWENTY_EMAIL`, and `TWENTY_PASSWORD`.
- [x] Schema-only configuration returns without reading credentials or making requests.
- [x] Pinned tools, generated provider docs, Registry manifest, MPL-2.0, and CI definitions.
- [x] Release preparation kept behind an unset `RELEASE_ENABLED` gate.
- [x] Run and report baseline checks before handoff.

The scaffold does not authenticate, validate endpoints or credentials, or register resources or data sources. `providerConfig` only holds resolved values in provider-process memory. All-null configuration also returns early, even if credential environment variables exist. Stage 3 must preserve schema-only safety while adding environment-only authentication tests.

Current dependency choices match Kaneo where used:

| Component | Pin |
| --- | --- |
| Go | 1.27.1 |
| Terraform Plugin Framework | v1.19.0 |
| terraform-plugin-go | v0.31.0 |
| golangci-lint | v2.13.2 |
| tfplugindocs | v0.25.0 |
| genqlient | v0.8.1 |
| nullable optional values | v1.2.0 |
| Terraform in CI | 1.14.7 |

Stage 2 adds genqlient and the map-backed `nullable.Nullable[T]` dependency used for three-state input handling. No REST client, Terraform acceptance library, or container library is included. Add dependencies only when their stage needs them, with an explicit pin.

### Scaffold verification

Local checks passed with Go 1.27.1 and Terraform 1.14.7:

- `make fmt` and `make fmt-check`
- `make lint`, using golangci-lint v2.13.2, reported zero issues
- `make test`, with 100% statement coverage in `internal/provider`
- `make build` and `go test -race ./...`
- `make generate-docs` and `make validate-docs`, using tfplugindocs v0.25.0 without Twenty credential environment variables
- `go mod verify`

The entrypoint has no unit coverage. No Twenty container, acceptance test, GraphQL generation, authentication request, release build, or signing check ran in this stage.

## Stage 2: selected GraphQL generation

- [x] Fetch only the committed Metadata SDL from the upstream pin below. Record the source URL, commit, checksum, and license in `graphql/README.md`.
- [x] Pin genqlient with a `go.mod` tool directive. Add `graphql/genqlient.yaml` and selected operation documents, generating into `internal/client/`.
- [x] Select password-session operations `getLoginTokenFromCredentials`, `getAuthTokensFromLoginToken`, and `renewToken`. Include expiry fields and the origin-specific token exchange.
- [x] Select `currentUser` fields needed to identify the authenticated workspace, bootstrap member, and member role assignments. Select workspace lookup fields from the pinned SDL.
- [x] Select `getRoles`, `createOneRole`, `updateOneRole`, `deleteOneRole`, and permission-flag, object, and field permission upserts. Confirm exact mutation names and inputs against the SDL and resolvers.
- [x] Select `findWorkspaceInvitations`, `sendInvitations`, `deleteWorkspaceInvitation`, `updateWorkspaceMemberRole`, and `deleteUserFromWorkspace`.
- [x] Add `make generate` and a generated-client drift workflow covering tracked and untracked output.
- [x] Check generation twice, build, and run unit tests. Do not add authentication requests or provider resources in this stage.

Generate only the operations needed for IAM and configuration. Do not generate against a live endpoint or include CRM operations. Treat the upstream schema's license separately from this provider's MPL-2.0 license.

### Stage 2 verification

The cached SDL matched the official raw GitHub URL at the source pin byte-for-byte.
Its SHA256 is `f93a16c80bb560ee516f5f9af85c9d67e26848983952a8ff8c692ca70e5e9e59`.
The SDK package declares MIT; `graphql/LICENSE` preserves the upstream notice.
`make generate` verifies both files against `graphql/SHA256SUMS` without
fetching upstream or using introspection. Cached auth, user, workspace, role,
and invitation resolvers also matched fresh pinned-source downloads.
Permission services and role mappers were checked from the same public pin.

Checks passed with Go 1.27.1 and local Terraform 1.14.5. CI still pins
Terraform 1.14.7, so the local docs check did not use CI's exact Terraform version.

- Two offline `make generate` runs produced identical SHA256 manifests for both
  generated files. The offline check used the installed Go 1.27.1 binary with
  `GOTOOLCHAIN=local`, `GOPROXY=off`, and `GOSUMDB=off`. A final generation
  also passed with a constrained PATH containing Go and `sha256sum`, but no
  Terraform or Docker, and matched the same manifest.
- `go mod download`, `go mod tidy`, and `go mod verify`.
- `make fmt`, `make fmt-check`, and `make lint`, with zero lint issues.
- `make test`, with 44.9% coverage in generated `internal/client` and 100% in
  `internal/provider`. Synthetic in-memory tests cover optional-input
  serialization, token expiry, identity/permissions, and error propagation.
- `make build` and `go test -race ./...`.
- `make generate-docs` and `make validate-docs` with Twenty credential environment
  variables unset. Generated provider docs did not change.
- `git diff --check`.

The entrypoint and generated test-bootstrap package have no unit coverage.
No authentication transport, Terraform resources, bootstrap implementation,
container stack, acceptance tests, or live Twenty calls were added or run.

### Generated operation/type handoff

The main output is `internal/client/metadata.gen.go`, package `client`.
All functions accept a context and a genqlient `graphql.Client`; stage 3 must
supply the validated endpoint, safe HTTP transport, and in-memory session.

| Next agent | Generated operations | Useful generated types |
| --- | --- | --- |
| Authentication | `GetLoginTokenFromCredentials`, `GetAuthTokensFromLoginToken`, `RenewToken`, `GetPublicWorkspaceDataByDomain` | `Token`, `TokenPair` |
| Identity and workspace | `CurrentUser`, `CurrentWorkspace`, `GetPublicWorkspaceDataById` | `WorkspaceIdentity`, `AvailableWorkspaceIdentity`, `MemberIdentity` |
| Roles | `GetRoles`, `CreateOneRole`, `UpdateOneRole`, `DeleteOneRole`, `UpsertPermissionFlags`, `UpsertObjectPermissions`, `UpsertFieldPermissions` | `RoleProperties`, `RoleDetails`, `CreateRoleInput`, `UpdateRoleInput`, `UpdateRolePayload`, `UpsertPermissionFlagsInput`, `UpsertObjectPermissionsInput`, `ObjectPermissionInput`, `UpsertFieldPermissionsInput`, `FieldPermissionInput` |
| Membership | `FindWorkspaceInvitations`, `SendInvitations`, `DeleteWorkspaceInvitation`, `UpdateWorkspaceMemberRole`, `DeleteUserFromWorkspace` | `Invitation`, `MemberIdentity` |

Response types are `<Operation>Response`. Read `graphql/README.md` for the full
selection list and pinned resolver/service paths. Important choices:

- Nullable scalar/object fields use `nullable.Nullable[T]` v1.2.0. Targeted
  `omitempty` directives omit unspecified inputs while preserving explicit
  false, null, and empty string. No handwritten API response structs are needed.
- UUID is a string, DateTime is `time.Time`, and selected JSON values use
  `json.RawMessage`. Unused scalars are not bound.
- Required list inputs need non-nil empty slices to send `[]`. In particular,
  `UpsertPermissionFlagsInput.PermissionFlagKeys` replaces the whole flag set.
  Object upserts replace the object set; field upserts have different patch and
  related-field mirroring behavior.
- Sparse role update omission preserves values. Null can clear description/icon,
  but null label or capability booleans is not a valid persisted permission.
  Explicit false must never become omission.
- Role create/update selects scalar properties only. Re-read `GetRoles` for
  hydrated permissions/assignments. Re-read `CurrentUser` after member-role
  changes. Member mutations need workspace-member ID, not user ID or
  user-workspace ID. A removal response can contain the pre-removal membership;
  verify disappearance with a fresh read, not `deletedAt`.
- Login and exchange require explicit origin. Renewal's `appToken` is the
  refresh token. All token selections include `expiresAt`. Current member and
  current workspace can be null before provisioning; do not silently accept
  zero values as authenticated identity.
- Invitation send returns payload errors/results and can partially succeed.
  Revocation returns "success" or "error", not an ID. Listing is unpaginated
  and includes expired invitations. Null/omitted role resolves the default role
  at acceptance. Guards for bootstrap/recovery/last administrator/last member
  still belong to later code.

Disposable onboarding output is
`internal/client/testbootstrap/bootstrap.gen.go`. Its separate operations are
`TestSignUp`, `TestVerifyEmail`, `TestCreateWorkspace`,
`TestActivateWorkspace`, and `TestJoinWorkspace`. Do not import that package
from provider code. Stage 3 must use a disposable mail sink for server-issued
verification/invitation tokens, require a nonblank workspace display name, and
provision an independent recovery administrator. Generation does not implement
any of those steps.

## Stage 3: first container-tested functionality

- [ ] Add a disposable `integration/compose.yml`, pinned to the Twenty image below and pinned supporting services. Use local ports, isolated volumes, test-only credentials, and explicit readiness checks.
- [ ] Bootstrap a verified automation user and a separate recovery administrator in that stack. Never discover credentials from a live deployment.
- [ ] Implement endpoint validation and password-session login against `/metadata`. Derive origin from the configured instance URL. Reject redirects rather than forwarding secrets.
- [ ] Exchange the login token for a workspace session; verify the workspace and authenticated member before any resource operation.
- [ ] Keep access and refresh tokens only in memory. Serialize renewal within one provider instance, replacing both tokens and honoring context cancellation and timeouts.
- [ ] Return clear, redacted diagnostics for disabled password auth, MFA, CAPTCHA, unverified email, permission denial, and malformed responses. Do not weaken authentication settings.
- [ ] Replace the `providerConfig` handoff with a shared authenticated client. Add missing/unknown/invalid config tests, environment-only config coverage, and schema-only no-network regression tests.
- [ ] Implement the first `twenty_role` data source using `getRoles`, with a stable role ID and explicit lookup rules.
- [ ] Add `make testacc` with Terraform Plugin Testing and local container lifecycle management. Exercise login, renewal, workspace identity, role lookup, bad credentials, and denied permissions.
- [ ] Document the working subset, run acceptance against the pinned container, and report exact versions and checks. Keep broader IAM resources for stage 4.

API keys and userless client-credentials tokens cannot perform all intended membership mutations in v2.44.0. Password mode should start a new session on each provider configuration, not persist a rotating refresh token between runs.

## Stage 4: IAM resources and guides

- [ ] Add a `twenty_role` resource. Cover create/read/update/delete, native-ID import, drift, disappeared roles, and protection for built-in or bootstrap roles.
- [ ] Expose explicit permission flags. Test `ROLES` and `WORKSPACE_MEMBERS` settings permissions separately from object-record permissions. Confirm server defaults and preserve explicit false values.
- [ ] Add a `twenty_workspace` data source using Metadata identity or lookup operations, with no CRM record queries.
- [ ] Add `twenty_workspace_member`, keyed by workspace and normalized email. Validate a stable import format before documenting it.
- [ ] Create sends an invitation and returns without waiting for acceptance. Read distinguishes pending invitations from accepted members and preserves identity when the invitation is accepted.
- [ ] Update changes an accepted member's role. For pending invitations, test cancellation and replacement if the API cannot update the role.
- [ ] Destroy revokes pending invitations or removes accepted members with `deleteUserFromWorkspace`, never Core record deletion.
- [ ] Test existing-member adoption, missing members, duplicate invitations, pagination, role drift, out-of-band acceptance and revocation, and eventual consistency.
- [ ] Reject managing the bootstrap identity; guard the last administrator and last workspace member. Manage only declared identities, never reconcile by evicting unlisted members.
- [ ] Add import, authentication, permission, and invitation-lifecycle guides under `templates/guides/`. Add examples and per-resource import snippets, then regenerate `docs/`.
- [ ] Test full role and membership lifecycles against disposable identities in the pinned container. Include imports, pending-to-accepted transitions, permission-denied cases, and safety guards.
- [ ] Keep workspace setting and object/field/relation configuration work separate. Schema deletion can destroy business data and needs its own safeguards.
- [ ] Review release readiness only after acceptance and documentation pass. Do not publish or configure signing secrets as part of these implementation stages without separate authorization.

## Upstream pins

- Twenty target: `v2.44.0`
- Source commit: `f7a4720eb4d479bfa3f6634bcdd703bb4de66600`
- Container: `twentycrm/twenty:v2.44.0@sha256:01fb6d2c00397976fd7613dbeb9703b514b52fb6270339b7a326a2a975d15b26`
- [Metadata SDL](https://github.com/twentyhq/twenty/blob/f7a4720eb4d479bfa3f6634bcdd703bb4de66600/packages/twenty-client-sdk/src/metadata/generated/schema.graphql)
- [Auth resolver](https://github.com/twentyhq/twenty/blob/f7a4720eb4d479bfa3f6634bcdd703bb4de66600/packages/twenty-server/src/engine/core-modules/auth/auth.resolver.ts)
- [Role resolver](https://github.com/twentyhq/twenty/blob/f7a4720eb4d479bfa3f6634bcdd703bb4de66600/packages/twenty-server/src/engine/metadata-modules/role/role.resolver.ts)
- [Invitation resolver](https://github.com/twentyhq/twenty/blob/f7a4720eb4d479bfa3f6634bcdd703bb4de66600/packages/twenty-server/src/engine/core-modules/workspace-invitation/workspace-invitation.resolver.ts)
- [User resolver](https://github.com/twentyhq/twenty/blob/f7a4720eb4d479bfa3f6634bcdd703bb4de66600/packages/twenty-server/src/engine/core-modules/user/user.resolver.ts)

These public source pins guide implementation. They are not end-to-end authentication or compatibility results.

## Next-stage integration points

Start stage 3 with the stage-2 operation/type handoff above and `graphql/README.md`, not a new client generator. `make generate` is available and uses committed SDL only. Add authentication to `TwentyProvider.Configure` only when stage 3 is authorized. The existing no-config tests and credential-free documentation command remain regression checks for that boundary.

`main.go` serves protocol 6 at `registry.terraform.io/glitchedmob/twenty`. The manifest advertises protocol 6.0. The release workflow follows Kaneo's GPG-signing layout but stays gated off, with no tags or secrets configured.
