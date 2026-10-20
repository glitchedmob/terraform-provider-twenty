# GraphQL client generation

We commit Twenty's Metadata API schema so `genqlient` can check our queries and generate typed Go code. Updating the schema helps catch breaking API changes during generation and compilation. The provider does not read the schema at runtime.

- `schema.graphql` describes Twenty's `/metadata` API.
- `operations/` contains our queries and mutations, which generate `internal/client/metadata.gen.go`.
- `testbootstrap/` contains disposable acceptance-test onboarding operations. These generate a separate `internal/client/testbootstrap/bootstrap.gen.go` and are never used by provider resources.
- `genqlient*.yaml` configures generation and Go type mappings.

## Regenerate

From the repository root:

```shell
make generate
go test ./internal/client/...
```

Edit the operation files or generator settings, not the generated Go files. Commit the inputs and generated output together.

Generation verifies the schema and license checksums and uses the tool version pinned in `go.mod`. Once Go modules are cached, it works offline without a Twenty server or credentials.

## Schema source and updates

The schema is an unmodified copy from [Twenty v2.46.0's SDK][schema]. Its [MIT license][license] is preserved in [LICENSE](LICENSE), with file hashes in [SHA256SUMS](SHA256SUMS). Provider-authored code remains MPL-2.0.

To update it, copy the schema and SDK license from a specific upstream release commit, update the version and source links here, and run `sha256sum schema.graphql LICENSE > SHA256SUMS` from this directory. Use upstream files, not live introspection.

Regenerate and review the changes, then run unit and disposable acceptance tests. Schema checks cannot detect changes in server behavior. If changing the supported Twenty baseline, also update the acceptance images, workflow, and documented minimum.

[schema]: https://raw.githubusercontent.com/twentyhq/twenty/9070979232c4f37165108a2eb5a4042e0cfdc3d6/packages/twenty-client-sdk/src/metadata/generated/schema.graphql
[license]: https://raw.githubusercontent.com/twentyhq/twenty/9070979232c4f37165108a2eb5a4042e0cfdc3d6/packages/twenty-client-sdk/LICENSE
