# Development plan and handoff

Work in separate, serial stages. Stages 1, 2, and 3 are complete. Initial IAM work is now authorized one assigned step at a time. Step 4A adds only the `twenty_role` resource, its guards, and docs/tests. Workspace lookup and membership management are the next serial steps; workspace settings and object/field permission ownership remain out of scope. Checkboxes record completed work; the handoffs below record actual checks and container results.

## Stage 1: scaffold

- [x] Public repository and Go module layout matching the Kaneo provider.
- [x] Plugin Framework entrypoint, metadata, configuration schema, and unit tests.
- [x] Environment fallback names `TWENTY_ENDPOINT`, `TWENTY_EMAIL`, and `TWENTY_PASSWORD`.
- [x] Schema-only configuration returns without reading credentials or making requests.
- [x] Pinned tools, generated provider docs, Registry manifest, MPL-2.0, and CI definitions.
- [x] Release preparation kept behind an unset `RELEASE_ENABLED` gate.
- [x] Run and report baseline checks before handoff.

At the stage-1 handoff, the scaffold did not authenticate, validate endpoints or credentials, or register resources or data sources. `providerConfig` held resolved values in provider-process memory. Stage 3 replaces that placeholder with `ClientData` and an authenticated `client.Session`. Absent or raw-null configuration must still return before reading credentials; an explicit empty configuration object may authenticate with environment-only credentials.

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
| Terraform Plugin Testing | v1.16.0 |
| testcontainers Compose | v0.44.0 |
| Terraform in CI | 1.14.7 |

Stage 2 added genqlient and the map-backed `nullable.Nullable[T]` dependency for three-state input handling. Stage 3 adds the pinned Terraform Plugin Testing dependency for disposable acceptance tests. Do not import Kaneo's REST client or container implementation, and add dependencies only when their stage needs them.

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

Authorized scope is password-session authentication, identity validation, the first read-only role data source, and tests against a disposable stack. No resources, release, tags, signing secrets, or infrastructure changes are authorized.

- [x] Add a disposable `integration/compose.yml`, pinned to the Twenty image below and pinned supporting services. Use local ports, isolated volumes, test-only credentials, and explicit readiness checks.
- [x] Bootstrap a verified automation user and a separate recovery administrator in that stack. Never discover credentials from a live deployment.
- [x] Implement endpoint validation and password-session login against `/metadata`. Derive origin from the configured instance URL. Reject redirects rather than forwarding secrets.
- [x] Exchange the login token for a workspace session; verify the workspace and authenticated member before any resource operation.
- [x] Keep access and refresh tokens only in memory. Serialize renewal within one provider instance, replacing both tokens and honoring context cancellation and timeouts.
- [x] Return clear, redacted diagnostics for disabled password auth, MFA, CAPTCHA, unverified email, permission denial, and malformed responses. Do not weaken authentication settings.
- [x] Replace the `providerConfig` handoff with a shared authenticated client. Add missing/unknown/invalid config tests, environment-only config coverage, and schema-only no-network regression tests.
- [x] Implement the first `twenty_role` data source using `getRoles`, with computed stable `id`, exactly one `role_id` or exact `label` selector, and errors for missing or duplicate matches.
- [x] Add role unit tests for schema, typed client configuration, selector validation, nullable state, sanitized errors, permission denial, required server values, and inconsistent permission flags.
- [x] Add source documentation, role examples, credential-free documentation targets, and an acceptance CI workflow using Terraform 1.14.7 and Go from `go.mod`.
- [x] Add `make testacc` with Terraform Plugin Testing and local container lifecycle management. Exercise login, renewal, workspace identity, role lookup, bad credentials, and denied permissions.
- [x] Document the working subset, run acceptance against the pinned container, and report exact versions and checks. Keep broader IAM resources for stage 4.

API keys and userless client-credentials tokens cannot perform all intended membership mutations in v2.44.0. Password mode starts a new session on each provider configuration, not a persisted rotating refresh token between runs.

### Stage 3 verification and handoff

Final checks passed with Go 1.27.1 and Terraform 1.14.7 on linux/amd64.
The local Docker server was 29.8.1, client 29.8.2, and Compose CLI 5.5.1.
Terraform 1.14.7 came from the official release archive and matched its
published SHA256. Preliminary bootstrap runs used the installed Terraform
1.14.5; the final Terraform acceptance and documentation runs used 1.14.7.

The disposable image pins are recorded with full digests in
`integration/compose.yml`:

| Service | Version |
| --- | --- |
| Twenty server and worker | v2.44.0, target digest below |
| PostgreSQL | 16.13-alpine |
| Redis | 7.4.8-alpine |
| Mailpit | v1.29.4 |
| Terraform Plugin Testing | v1.16.0 |
| testcontainers and Compose module | v0.44.0 |

Checks passed:

- `go mod tidy`, `go mod download`, and `go mod verify`.
- Two offline `make generate` runs with the installed Go 1.27.1 binary,
  `GOTOOLCHAIN=local`, `GOPROXY=off`, and `GOSUMDB=off`. Both generated-file
  SHA256 values were unchanged between runs. SDL and license checksums passed.
- `make fmt`, `make fmt-check`, and `make lint`, with zero lint issues.
- `make test`, with statement coverage of 100% in `internal/provider`,
  54.0% in `internal/client`, and 9.0% in disposable helpers. Container
  lifecycle code is skipped in unit tests. Entrypoint and generated
  test-bootstrap code have no unit coverage.
- `go test -race ./...` and `make build`.
- `make generate-docs` and `make validate-docs`, with Terraform 1.14.7 and
  tfplugindocs v0.25.0. A local sentinel endpoint with synthetic ambient
  credentials observed zero requests from both documentation commands.
- Full real-container `make testacc`, rerun after safety review, passed in
  87.86 seconds. Terraform was explicitly selected through
  `TF_ACC_TERRAFORM_PATH`. No disposable project containers or volumes remained.
- `git diff --check`.

The acceptance suite covers:

- Worker-delivered email verification for the operator, workspace creation and
  activation through the image's real migrations and generated Metadata calls.
- An independent recovery administrator verified through its server-issued
  personal invitation token captured from Mailpit.
- Real password/origin login, token exchange, workspace/member identity, two
  consecutive server-issued renewals, and authenticated reads afterwards.
- Terraform role lookup by native ID and exact label with all credentials
  supplied through the disposable environment.
- Missing-role and wrong-password errors.
- Actual `ROLES` permission denial for a third verified member assigned a
  test-only restricted role, including the Terraform diagnostic.
- Both operator and recovery administrator retaining their identity and
  settings permissions after the restricted-member test.

Bootstrap initially failed when the shared workspace fragment selected
`createdAt`/`updatedAt` through `CurrentUser`'s cached auth workspace.
Those fields now belong only to `CurrentWorkspace`. The operation source,
generated code, and synthetic timestamp test changed together. The committed
SDL and upstream pin did not change.

Consuming a personal invitation verifies the invited email in this server.
Global signup after the first workspace and re-verification of an already
verified invitation account are not used. No identity rows were edited,
credentials signed, or email-verification settings disabled.

The role data source exposes description, icon, editability, assignability, six global capability booleans, and explicit permission flags. It does not calculate full effective object/field permissions or modify roles. Its response wrapper validates required values before genqlient can decode null booleans as false. Nullable flags remain null rather than becoming an empty set, and unknown nonblank flag keys remain compatible with the SDL's string field.

`make testacc` removes ambient Twenty credentials and Terraform logging settings, sets `TF_ACC=1`, and limits the suite to 25 minutes. Tests own their stack lifecycle and credentials. `_artifacts/` is already ignored; only redacted disposable-stack diagnostics may be uploaded. Documentation targets remove credential environment variables and rely on the raw-null schema configuration guard.

### Shared interfaces and limits

`ClientData.Client` is a shared `*client.Session`. Its `Client()` implements
genqlient's `graphql.Client` and handles authentication/renewal for existing
generated operations. `Identity()` returns a defensive copy of the verified
configuration-time IDs and role assignments. `Refresh(ctx)` requests a real
server renewal; it does not forge expiry or tokens. Requests and refresh share
a context-aware gate. Ambiguous refresh failure discards tokens and requires
reconfiguration; operations are never blindly retried.

Future role/member resources must re-read current identity, roles, and
membership before safety decisions. The identity snapshot is not a live
membership reconciliation result. Protect the bootstrap member and its role,
the independent recovery admin, the last administrator, and the last member.
Those mutation guards are stage-4 work, not implemented resources.

Reuse `internal/acceptance.Start`, `Bootstrap`, and
`Fixture.InviteAccount`. The helpers register cleanup before startup, wait at
most ten minutes for migrations/readiness, bind exposed ports to loopback, and
remove project-scoped volumes. Docker/testcontainers configuration is isolated
before it is cached, including registry credentials and a fresh random reaper
session. Previously cached ambient configuration and remote Docker contexts
fail closed. Failure artifacts contain only allowlisted service status and
event counts, not raw logs, mail, credentials, GraphQL bodies, state, or plans.

Only the password-session and role-read subset is container-tested as provider
functionality. CAPTCHA, MFA, SSO-only accounts, unverified email, non-ASCII email
addresses, remote HTTP, ambient HTTP proxies, and persisted sessions are not
supported. Roles/member lists are unpaginated in this pin. The role data source
reports explicit flags/global booleans, not calculated effective object/field
permissions. Proactive-expiry/concurrency paths have synthetic race-tested
coverage; live renewal uses two explicit server calls rather than waiting for
the default server expiry. The pinned testcontainers library starts its
hardcoded `testcontainers/ryuk:0.14.0` cleanup image by tag; application and
supporting service images all have digest pins.

Stage 3 was completed without resources, releases, tags, pushes, or infrastructure
changes. The user subsequently authorized serial initial IAM implementation,
starting with step 4A below.

## Stage 4: IAM resources and guides

- [x] Add a `twenty_role` resource. Cover create/read/update/delete, native-ID import, drift, disappeared roles, and protection for built-in or bootstrap roles.
- [x] Expose explicit permission flags. Test `ROLES` and `WORKSPACE_MEMBERS` settings permissions separately from object-record permissions. Confirm server defaults and preserve explicit false values.
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

### Step 4A: role resource handoff

`twenty_role` is now both a resource and a separate read-only data source.
The resource owns the label, nullable description/icon, three assignability
booleans, six global capability booleans, and the whole explicit flag set.
Users default to assignable; agents, API keys, and all global grants default
to false. All nine booleans are sent explicitly. Flags are required; `[]`
sends a non-nil empty list to replace the set with no explicit flags.

`NewRoleResource` uses `ClientData.Client` and its shared `MutationLock`.
`readRoleSafety` re-reads the current operator identity, all workspace members,
roles and assignment relations, and the workspace default. Deletion separately
reads application defaults with the `APPLICATIONS` settings grant.
It cross-checks member-role relations and rejects missing, duplicate, foreign,
or inconsistent data before mutation. The configuration-time identity pins
who the operator is; its role IDs are not the current-role safety source.
The new generated `FindManyApplications` selection reads `id/defaultRoleId`
so application assignments cannot disappear behind the role relation lists.
Create/update need only ROLES plus member visibility; deletion additionally
requires APPLICATIONS or a global full-settings grant.

Guards protect current operator roles, workspace default, non-editable roles,
the pinned standard Admin universal identifier, and reserved Admin/Member/Guest
labels. The label reservation also protects custom lookalikes. Downscoping
must preserve an assigned full-settings administrator. Deletion refuses roles
assigned to any member, agent, returned API key, or application default, rather
than allowing the server's silent assignment rebinding. Expired keys still block
deletion. The pinned API omits revoked keys from assignment lists and deletion
rebinding; their inactive historical assignments cannot be enumerated here.

Creation supplies a random native UUID before sending the mutation. Partial
flag/read failure retains that ID in state and records actual server values
when they can be read. An ambiguous create response retains the requested ID
for inspection/import. Updates preserve the existing ID after partial failure.
There is no rollback deletion, label adoption, or blind mutation retry.
Only a validated role list proving absence removes state. The resource rejects
unavailable/unsupported flag sets; the data source still preserves nullable
lists and future nonblank string keys. Both share nullable-string and required
role-value validation without displaying raw upstream errors.

The pinned server rejects global object writes/deletes with global read false.
Real tests persisted all-false grants and switched all six globals on and off,
with explicit flag replacement and clearing. No object/field permission upsert
or CRM record operation is called by the resource. Create-time string
normalization is handled by rejecting noncanonical configured whitespace.
Null clears description/icon; empty strings remain distinct and persist.

Source docs now include a resource template, complete examples, an import shell
snippet, and import/permissions guides. Generated pages and the README support
list reflect the role resource without claiming workspace or membership support.

### Step 4A verification

Checks use Go 1.27.1, Terraform 1.14.7 on linux/amd64, golangci-lint v2.13.2,
and tfplugindocs v0.25.0. Docker server/client are 29.8.1/29.8.2; Compose is
5.5.1. The verified Terraform executable from stage 3 remains at
`/tmp/twenty-terraform-1.14.7/terraform`. The archived ZIP SHA256 matches the
stored official checksum, `e8bbcefea8015156e04e2a325cde37a0b2fb761728bda548e2fe3b8ad7c18c96`.
Acceptance selects it through `TF_ACC_TERRAFORM_PATH`; formatting/docs put
its directory first in PATH. No test in this step uses the global Terraform
1.14.5 binary.

Passed checks:

- `go mod download`, `go mod tidy`, and `go mod verify`. UUID v1.6.0 was already
  indirect and is now a direct dependency for preallocated native IDs.
- Two offline `make generate` runs with cached Go 1.27.1, `GOTOOLCHAIN=local`,
  `GOPROXY=off`, and `GOSUMDB=off`. SDL/license checksums passed and both
  generated-file SHA256 values stayed identical. Use `$(go env GOROOT)/bin`
  before setting `GOTOOLCHAIN=local`; the base PATH Go is 1.25.5 and an initial
  offline attempt correctly rejected it.
- `make fmt`, `make fmt-check`, `make lint`, `make test`, and `make build`.
  Lint reports zero issues. Unit coverage is 93.0% in provider, 53.2% in client,
  and 9.0% in disposable helpers. Generated bootstrap and entrypoint remain
  without unit coverage.
- `go test -race ./...`, including two role resources sharing a mutation lock
  across fresh safety reads and writes.
- `make generate-docs` and `make validate-docs` with synthetic ambient credentials
  pointing at a local sentinel. Both completed with zero endpoint requests.
- Full real-container `make testacc` with the unchanged pinned IAM stack,
  passing in 101.82 seconds after the application-visibility guard adjustment.
- `git diff --check`.

Unit/mock cases cover CRUD, imports/UUIDs, known/null/unknown inputs, exact flag
vocabulary, explicit false and nullable strings, malformed/duplicate responses,
confirmed absence versus read failures, partial/ambiguous mutations, no label
adoption or mutation retries, current-operator/default/built-in protection,
member/agent/API-key/application use, and final-administrator preservation.

Real-container acceptance covers:

- The entire stage-3 login, identity, two server-issued renewals, role ID/label
  lookup, missing-role, bad-password, and recovery-administrator suite.
- Terraform role create/read/rename/update, all global booleans on/off, safe
  omitted defaults, explicit flag set replacement and clearing, empty and null
  description/icon values, native-ID import, and empty plans after refresh.
- Out-of-band description/read/flag drift, confirmed deletion followed by
  Terraform recreation, normal destroy, and missing-role import rejection.
- Terraform assigned-member deletion refusal, followed by a fresh check that
  its assignment did not rebind. Only that test member is deliberately moved
  to release the role before normal destroy.
- Real bootstrap/default role deletion refusal, and server rejection of a
  global write grant when global read is false.
- Real permission denial for data-source reads, resource creation, and resource
  import/read. ROLES-only creation/update succeeds; deletion fails closed until
  APPLICATIONS permits checking application defaults. ROLES does not grant invitation
  permission, and WORKSPACE_MEMBERS does not grant role listing.
- Both the operator and independent recovery administrator retain identity
  and role settings access at the end.

The first acceptance attempt exposed a literal newline escape in a test HCL
string, not a provider/API failure. A later minimal-ROLES test exposed the
APPLICATIONS requirement for application visibility. The guard now queries
applications only before deletion, and the real test verifies denial followed
by deletion with explicit visibility. Corrected full runs passed. Tests remove
only their own disposable stack; existing unrelated local containers are not
part of cleanup. No production, infrastructure, live account, signing secret,
release, tag, or push was used.

### Next serial IAM steps

Add the workspace data source before the membership resource in separate
assigned tasks. Reuse `ClientData.Client`, the shared mutation lock, and the
fresh validated read pattern. Do not use `Session.Identity().RoleIDs` as a
membership or role reconciliation snapshot. Keep bootstrap membership and its
roles outside management, preserve an independent recovery administrator, and
never evict undeclared users or the final workspace member.

Membership requires a declared normalized-email identity, explicit role-ID
references for dependencies, tested native import semantics, and separate
pending-invitation versus accepted-member handling. Reuse
`acceptance.Start/Bootstrap/Fixture.InviteAccount`; keep disposable onboarding
code out of provider packages. Generated Metadata member/invitation operations
already exist. Do not implement object/field permissions or workspace setting
ownership as part of those steps.

Limits carried forward: the lock is per configured provider, not distributed.
Another Terraform run, provider alias, or administrator can race server reads
and mutations; the API has no transaction/conditional-write contract here.
Pause competing IAM writers during apply. The administrator guard counts
assigned full-settings roles, not independent login reachability, which must
be maintained outside Terraform. Agent/API-key/application assignment and
last-administrator failures have mock coverage but no corresponding live
mutation fixture; the real suite does not downscope either protected admin.
Partial-failure recovery also has mock coverage, without forced server faults.
Revoked API-key assignment history is not enumerable through the pinned role
or API-key lists. Active and expired returned assignments block deletion.

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

Stages 3 and 4A use the stage-2 operation/type handoff above and `graphql/README.md`, not a new client generator. `make generate` uses committed SDL only. Authentication stays in `TwentyProvider.Configure`, while absent/raw-null configuration tests and credential-free documentation commands remain regression checks. Initial IAM work may continue under the user's serial authorization, one assigned step at a time, using the validated shared session and preserving bootstrap/recovery protections.

`main.go` serves protocol 6 at `registry.terraform.io/glitchedmob/twenty`. The manifest advertises protocol 6.0. The release workflow follows Kaneo's GPG-signing layout but stays gated off, with no tags or secrets configured.
