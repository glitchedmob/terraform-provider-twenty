# Metadata GraphQL generation

The committed SDL is the complete Twenty v2.44.0 Metadata schema. Only the
handwritten IAM, identity, and password-session operations in `operations/`
generate provider client code. The schema contains other upstream operations;
they are not selected. Use `/metadata`, never Core GraphQL `/graphql` or CRM
record REST endpoints. The stage-2 generation and unit checks do not establish
end-to-end compatibility with a running server.

## Provenance and license

- Upstream repository: [twentyhq/twenty](https://github.com/twentyhq/twenty).
- Release: `v2.44.0`.
- Source commit: `f7a4720eb4d479bfa3f6634bcdd703bb4de66600`.
- Official raw SDL source: <https://raw.githubusercontent.com/twentyhq/twenty/f7a4720eb4d479bfa3f6634bcdd703bb4de66600/packages/twenty-client-sdk/src/metadata/generated/schema.graphql>.
- SDL SHA256: `f93a16c80bb560ee516f5f9af85c9d67e26848983952a8ff8c692ca70e5e9e59`.
- Official package license: <https://raw.githubusercontent.com/twentyhq/twenty/f7a4720eb4d479bfa3f6634bcdd703bb4de66600/packages/twenty-client-sdk/LICENSE>.
- License SHA256: `f2c3d261cefa7be3fe81790e777f72c83083b832e653c493db0cc960e81f2b0f`.

The cached research SDL matched a fresh download of that official raw URL
byte-for-byte when stage 2 added it. `schema.graphql` is an unmodified copy.
No introspection was used.

`LICENSE` is the upstream twenty-client-sdk MIT license, with its copyright
notice intact. The pinned
[package manifest](https://github.com/twentyhq/twenty/blob/f7a4720eb4d479bfa3f6634bcdd703bb4de66600/packages/twenty-client-sdk/package.json)
declares MIT. The upstream
[root license](https://github.com/twentyhq/twenty/blob/f7a4720eb4d479bfa3f6634bcdd703bb4de66600/LICENSE)
also identifies this SDK package as an MIT exception to the server's AGPL
license. The provider's own code remains MPL-2.0. Keep this upstream notice
with the schema and generated client when redistributing them.

## Generate and check drift

From the repository root:

```shell
go mod download
make generate
go test ./internal/client/...
git diff --exit-code
test -z "$(git ls-files --others --exclude-standard -- internal/client/)"
```

`make generate` verifies `SHA256SUMS`, then runs genqlient v0.8.1 through
the `go.mod` tool directive. It writes:

- `internal/client/metadata.gen.go`, package `client`.
- `internal/client/testbootstrap/bootstrap.gen.go`, package `testbootstrap`.

Both files have genqlient's generator header. Do not edit them. Edit operation
documents or configuration, regenerate, and commit the sources and output
together. The generated-client workflow checks tracked diffs and untracked
client output on pull requests, pushes to main, and manual runs.

Generation uses committed files only. After Go modules are cached, it can run
offline. It needs Go and `sha256sum`, not Terraform, Docker, Twenty credentials,
an instance, or live schema discovery. CI does not download an upstream schema.

## Update the upstream pin

Do this deliberately, not during every generation. Choose a release and its
exact source commit. Download the Metadata SDL, SDK license, and package
manifest from official raw GitHub URLs at that commit into temporary files:

```shell
commit=f7a4720eb4d479bfa3f6634bcdd703bb4de66600
base="https://raw.githubusercontent.com/twentyhq/twenty/$commit/packages/twenty-client-sdk"
tmp="$(mktemp -d)"
curl --fail --location --silent --show-error "$base/src/metadata/generated/schema.graphql" -o "$tmp/schema.graphql"
curl --fail --location --silent --show-error "$base/LICENSE" -o "$tmp/LICENSE"
curl --fail --location --silent --show-error "$base/package.json" -o "$tmp/package.json"
sha256sum "$tmp/schema.graphql" "$tmp/LICENSE"
```

For the existing pin, compare those hashes with the provenance above before
copying. For a new pin, review the license and SDL diff, then inspect the pinned
resolvers and services for changed contracts. Do not introspect a running
instance. Copy the reviewed SDL and license into this directory, regenerate
`SHA256SUMS` with `cd graphql && sha256sum schema.graphql LICENSE > SHA256SUMS`,
and update the provenance here plus the pins in `AGENTS.md` and `DEVELOPMENT.md`.
Remove the temporary directory. Run generation twice, compare output, and run
the repository checks before committing.

## Selected operations and generated types

Function names match the GraphQL operation names, not necessarily their SDL
field capitalization. Every function takes a context and a `graphql.Client`;
it does not construct a transport, authenticate, or validate server responses.

| Document | Generated functions | Shared generated types |
| --- | --- | --- |
| `operations/auth.graphql` | `GetLoginTokenFromCredentials`, `GetAuthTokensFromLoginToken`, `RenewToken` | `Token`, `TokenPair` |
| `operations/identity.graphql` | `CurrentUser`, `CurrentWorkspace`, `GetPublicWorkspaceDataByDomain`, `GetPublicWorkspaceDataById` | `WorkspaceIdentity`, `AvailableWorkspaceIdentity`, `MemberIdentity` |
| `operations/roles.graphql` | `GetRoles`, `CreateOneRole`, `UpdateOneRole`, `DeleteOneRole`, `UpsertPermissionFlags`, `UpsertObjectPermissions`, `UpsertFieldPermissions` | `RoleProperties`, `RoleDetails`, `PermissionFlag`, `ObjectPermissionDetails`, `FieldPermissionDetails`, `PermissionPredicate`, `PermissionPredicateGroup` |
| `operations/members.graphql` | `FindWorkspaceInvitations`, `SendInvitations`, `DeleteWorkspaceInvitation`, `UpdateWorkspaceMemberRole`, `DeleteUserFromWorkspace` | `Invitation` |

Each function returns its generated `<Operation>Response` and an error.
Fragments share types where possible. Some nested fields still have generated
wrapper types with an embedded fragment. Use the embedded fragment or generated
getters rather than copying response structs by hand.

Role input types are `CreateRoleInput`, `UpdateRoleInput`,
`UpdateRolePayload`, `UpsertPermissionFlagsInput`,
`UpsertObjectPermissionsInput`, `ObjectPermissionInput`,
`UpsertFieldPermissionsInput`, and `FieldPermissionInput`.

Scalar bindings only cover selected types:

- `UUID` uses `string`. This preserves wire identity without accepting or
  rejecting UUID syntax here. Resource import and response validation belong
  to later stages.
- `DateTime` uses `time.Time`, including login, access, refresh, and invitation
  expiry. Invalid date strings fail JSON decoding.
- `JSON` uses `json.RawMessage` for restricted fields and row predicate values.
  It preserves numbers and arbitrary JSON without handwritten response types.
- Unused scalars such as `JSONObject`, `BigInt`, `Upload`, and
  `ConnectionCursor` have no binding until a selected operation needs them.

## Optional input contracts

Nullable scalar and object fields use
`github.com/oapi-codegen/nullable.Nullable[T]` v1.2.0. The map-backed type works
with genqlient's targeted `omitempty` directives. A pointer plus `omitempty`
cannot express both omitted and explicit null.

```go
input := client.UpdateRoleInput{
    Id: roleID,
    Update: client.UpdateRolePayload{
        // Unset fields are omitted and preserve existing values.
        CanReadAllObjectRecords: nullable.NewNullableWithValue(false),
        Description: nullable.NewNullNullable[string](),
        Icon: nullable.NewNullableWithValue(""),
    },
}
```

Explicit `false` is sent, null is sent, and an unspecified value is omitted.
Use `Get`, `IsNull`, and `IsSpecified` for nullable responses.
`GetOrEmpty` would conflate denied permissions with missing data. Nullable
lists remain slices, with nil for null and a non-nil empty slice for `[]`.
Required list inputs must use non-nil slices. For example,
`PermissionFlagKeys: []string{}` sends `[]` to clear flags; nil sends invalid
`null`. There is no generated runtime input validator.

Pinned server contracts matter even where the SDL marks an input nullable:

- Role creation defaults the six capability booleans to false and the three
  assignability booleans to true. Null and omission use those defaults.
- Role update is a sparse merge. Omission preserves values. Null clears
  description/icon, but null label or permission booleans fail server
  validation. Do not send Terraform unknowns as null.
- Permission-flag upsert replaces the entire set of string keys. Object
  permission upsert also replaces the object set; omitted override booleans
  preserve existing overrides, null resets to inheritance.
- Field permission upsert leaves unmentioned rows alone except for related
  fields the service mirrors. A mentioned field with both values null or
  omitted removes its restriction. Values support false restrictions, not
  true grants.
- Invitation role omission or null leaves the role unset. Acceptance then
  resolves the current default role.

`internal/client/metadata_test.go` checks actual generated request variables
and synthetic response decoding without network calls.

## Resolver checks and next-stage cautions

These contracts were checked against source at the pinned commit, not against
a container. Paths below are relative to upstream `packages/twenty-server/`:

- `src/engine/core-modules/auth/auth.resolver.ts` and
  `auth/token/services/renew-token.service.ts` confirm login/exchange origin
  arguments and renewal using the refresh token as `appToken`. Both tokens
  rotate. Login can consume an invitation and add membership.
- `src/engine/core-modules/user/user.resolver.ts` confirms that `currentUser`
  exposes the current workspace, own member, and the whole unpaginated member
  list. User ID, user-workspace ID, and workspace-member ID differ. Membership
  mutations need the workspace-member ID. Own member can be null before
  provisioning. Available workspace IDs/names come from `availableWorkspaces`;
  the SDL's `UserWorkspace` type has no workspace identity field.
- `src/engine/core-modules/workspace/workspace.resolver.ts` confirms workspace
  lookups. Our domain operation requires an explicit origin to avoid the
  resolver's synthetic omitted-origin result.
- `src/engine/metadata-modules/role/role.resolver.ts`, `role/role.service.ts`,
  and the `flat-role/utils/` create/update mappers confirm role operations.
  Create/update return scalar-only role DTOs. Re-read `GetRoles` for permissions
  and assignments. Member-role mutations return raw role entities; re-read
  `CurrentUser` for mapped permission flags.
- `src/engine/metadata-modules/role-permission-flag/role-permission-flag.service.ts`,
  `object-permission/object-permission.service.ts`, and
  `object-permission/field-permission/field-permission.service.ts` confirm the
  upsert behavior.
- `src/engine/core-modules/workspace-invitation/workspace-invitation.resolver.ts`
  and `services/workspace-invitation.service.ts` confirm invitation arguments.
  Listing includes expired invitations. Revocation returns "success" or
  "error", not an ID. Sending can partially succeed, and mail delivery can fail
  after creation. Check payload errors and results before retrying.

Role operations require the `ROLES` settings permission. Invitations require
`WORKSPACE_MEMBERS`. Removal has an authenticated-self exception. The server
can delete a workspace when its last member is removed, and role deletion can
rebind assignments. Later resource code must protect the bootstrap identity and
role, independent recovery administrator, last administrator, and last member.
The generated code implements none of those protections. Member removal returns
the membership loaded before deletion, so `deletedAt` is not proof of removal.
Confirm disappearance with a fresh member read.

## Disposable onboarding documents

`testbootstrap/operations.graphql` and `genqlient.testbootstrap.yaml` are
separate from provider operations. They generate only into
`internal/client/testbootstrap/`. Do not import this package from provider
configuration or resources.

The five functions are `TestSignUp`, `TestVerifyEmail`,
`TestCreateWorkspace`, `TestActivateWorkspace`, and `TestJoinWorkspace`.
They select `signUp`, `verifyEmailAndGetWorkspaceAgnosticToken`,
`signUpInNewWorkspace`, `activateWorkspace`, and `signUpInWorkspace`.
These are needed to onboard verified automation and recovery identities in a
future disposable stack without database edits or minted credentials.

Stage 3 must capture server-issued verification and personal invitation tokens
from that stack's mail sink. Workspace creation requires a nonblank display
name despite SDL nullability. Activation takes `ActivateWorkspaceInput{}`;
its legacy display-name field is ignored. Join with an explicit workspace and
personal invitation, never implicit new-workspace signup. Invite the recovery
administrator with an explicit admin role. The relevant source is
`auth/services/sign-in-up.service.ts` and
`user-workspace/user-workspace.service.ts`.

These documents do not implement bootstrap, weaken authentication settings,
start containers, or exercise a live account.
