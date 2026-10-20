# Terraform provider for Twenty

Manage Twenty custom objects, fields, roles, and workspace memberships with Terraform. Requires **Twenty v2.46.0 or later**.

The provider is not yet published to the Terraform Registry. See [setup and authentication](docs/guides/authentication.md) to use a local build.

## Documentation

- [Provider configuration](docs/index.md)
- Custom objects: [manage](docs/resources/object.md) or [look up](docs/data-sources/object.md)
- Fields: [manage custom fields](docs/resources/field.md) or [look up existing fields](docs/data-sources/field.md)
- Roles: [manage custom roles](docs/resources/role.md) or [look up existing roles](docs/data-sources/role.md)
- [Manage workspace memberships](docs/resources/workspace_member.md)
- [Read the current workspace](docs/data-sources/workspace.md)
- [Bulk membership and recovery](docs/guides/membership.md)

The provider uses Twenty's Metadata API. It does not manage workspace settings, global users/passwords, object/field permissions, or individual CRM records. Deleting objects or fields removes their stored data, so deletion is protected by default.

## Development

Use the Go version in `go.mod` and Terraform 1.14.7, as in CI. Tools run through `go tool`; no global installs are needed.

```shell
go mod download
make generate fmt-check lint test build generate-docs validate-docs
```

These checks need no Twenty credentials or running instance. `make testacc` additionally needs Docker Compose and creates its own disposable stack. Never run acceptance tests against a live deployment or commit credentials, Terraform state, or private member data.

`docs/` is generated. Edit `templates/`, `examples/`, or schema descriptions in `internal/provider/`, then run `make generate-docs validate-docs`. See [GraphQL generation](graphql/README.md) for schema provenance and client updates.

## License

[MPL-2.0](LICENSE). The bundled Twenty Metadata schema is [MIT-licensed](graphql/LICENSE).
