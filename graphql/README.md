# GraphQL client generation

The provider uses the committed Twenty v2.46.0 Metadata schema at `/metadata`, not the Core or CRM APIs. Only operations selected in `operations/` generate provider client functions. Declarations elsewhere in the schema do not add provider features.

## Provenance

- Upstream: [twentyhq/twenty](https://github.com/twentyhq/twenty), tag `v2.46.0`.
- Source commit: `9070979232c4f37165108a2eb5a4042e0cfdc3d6`.
- [Unmodified Metadata SDL](https://raw.githubusercontent.com/twentyhq/twenty/9070979232c4f37165108a2eb5a4042e0cfdc3d6/packages/twenty-client-sdk/src/metadata/generated/schema.graphql).
- [SDK MIT license](https://raw.githubusercontent.com/twentyhq/twenty/9070979232c4f37165108a2eb5a4042e0cfdc3d6/packages/twenty-client-sdk/LICENSE), preserved in [LICENSE](LICENSE).
- File hashes: [SHA256SUMS](SHA256SUMS).

The SDK [package manifest](https://github.com/twentyhq/twenty/blob/9070979232c4f37165108a2eb5a4042e0cfdc3d6/packages/twenty-client-sdk/package.json) and upstream [root license](https://github.com/twentyhq/twenty/blob/9070979232c4f37165108a2eb5a4042e0cfdc3d6/LICENSE) identify the SDK as MIT-licensed. Preserve its notice when redistributing the schema and generated code. Provider code remains MPL-2.0.

## Generate

From the repository root:

```shell
make generate
go test ./internal/client/...
git diff --exit-code -- internal/client/
```

Generation verifies the SDL/license hashes and runs the genqlient version pinned in `go.mod`. It uses committed files only and works offline once Go modules are cached. No Twenty instance, credentials, or introspection is needed.

Do not edit `internal/client/metadata.gen.go` or `internal/client/testbootstrap/bootstrap.gen.go`. Edit the operation documents/configuration and regenerate; commit sources and output together.

## Update the schema

1. Choose an upstream release and exact source commit.
2. Download its Metadata SDL, SDK license, and package manifest from the official URLs above, substituting the new commit.
3. Review schema, licensing, and resolver/service changes for selected operations. Do not use live introspection.
4. Replace `schema.graphql` and `LICENSE`, then run `sha256sum schema.graphql LICENSE > SHA256SUMS` in this directory.
5. Update this provenance. For a server-baseline change, also update the images in `integration/compose.yml`, the acceptance workflow, and the documented minimum.
6. Regenerate twice to check reproducibility, then run unit and disposable acceptance tests.

## Request and response contracts

UUIDs decode as strings, timestamps as `time.Time`, and JSON as `json.RawMessage`. Generated types do not validate identities, permissions, or ownership; provider code must do so.

Nullable scalar/object inputs use `nullable.Nullable[T]` to distinguish omission, null, and explicit values:

```go
input := client.UpdateRolePayload{
    CanReadAllObjectRecords: nullable.NewNullableWithValue(false),
    Description:            nullable.NewNullNullable[string](),
    Icon:                   nullable.NewNullableWithValue(""),
}
```

Unspecified fields are omitted. For responses, use `Get`, `IsNull`, and `IsSpecified` where absence matters; `GetOrEmpty` loses that distinction. Required lists need non-nil slices: `[]string{}` sends `[]`, while nil sends `null`.

Important server contracts:

- Role creation defaults all three assignability booleans to true; the provider explicitly uses narrower defaults. Sparse role updates preserve omitted values; null clears description/icon but is invalid for labels or permission booleans.
- Permission-flag and object-permission upserts replace their sets. Field-permission upserts have different merge semantics. Object/field upserts are generated but not used by provider resources.
- Invitation creation can succeed before mail delivery fails. Listing includes expired invitations. A null invitation role follows the current workspace default.
- Role deletion can rebind assignments; member deletion returns the pre-deletion row. Resources check references before deletion and verify results with fresh reads rather than trusting mutation payloads.
- `CurrentUser.currentWorkspace` is a cached summary without timestamps. Use `CurrentWorkspace` for full workspace properties.

See [generated-request tests](../internal/client/metadata_test.go) for wire-level coverage.

## Disposable bootstrap client

`testbootstrap/operations.graphql` and `genqlient.testbootstrap.yaml` generate a separate `internal/client/testbootstrap` package used only by `internal/acceptance`. Never import it from provider configuration or resources.

The helpers onboard verified automation and recovery identities using server-issued mail tokens from the disposable stack. They do not edit databases, mint credentials with signing secrets, or weaken live security settings.
