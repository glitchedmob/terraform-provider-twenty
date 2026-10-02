# Terraform provider for Twenty

Initial scaffold only. This is not a working or released provider. It exports provider metadata and an `endpoint`, `email`, and sensitive `password` configuration schema. It has no resources, data sources, authentication, or network requests.

The intended target is Twenty v2.44.0. Future work will manage IAM and configuration through `/metadata` GraphQL, not CRM records. Password-session authentication is forthcoming. API keys do not cover the intended membership operations in this release.

## Configuration

See the [generated provider documentation](docs/index.md) and [example configuration](examples/provider/provider.tf). The planned Registry address is `glitchedmob/twenty`; it is not published, so `terraform init` cannot install this scaffold from the Registry.

| Attribute | Environment fallback |
| --- | --- |
| `endpoint` | `TWENTY_ENDPOINT` |
| `email` | `TWENTY_EMAIL` |
| `password` | `TWENTY_PASSWORD` |

Explicit values override environment variables, including explicit empty strings. Endpoint and email whitespace is trimmed; password bytes are preserved. There is no default endpoint. When Terraform provides no configuration, `Configure` returns without reading credentials. Otherwise it resolves configuration only, without validating credentials or creating a session.

Use a dedicated automation identity and inject its credentials outside checked-in Terraform files. Sensitive schema attributes redact display output, but do not by themselves keep secrets out of saved plans. Never manage the bootstrap identity or its recovery administrator with this provider.

## Development

The layout and tooling follow [terraform-provider-kaneo](https://github.com/glitchedmob/terraform-provider-kaneo), using HashiCorp Terraform Plugin Framework and MPL-2.0.

Requirements:

- Go 1.27.1, as pinned in `go.mod`
- Terraform 1.14.7 for the documentation checks used in CI
- No Twenty instance or credentials for scaffold checks

```shell
go mod download
make fmt
make fmt-check
make lint
make test
make build
make generate-docs
make validate-docs
```

`make lint` uses golangci-lint v2.13.2 and documentation commands use tfplugindocs v0.25.0 through `go tool`. Both tools are pinned in `go.mod`; no global installs are needed. `make build` compiles all packages. To build a binary for local Terraform `dev_overrides`, run `go build -o terraform-provider-twenty .`.

`docs/` is generated. Edit `templates/`, `examples/`, or Go schema descriptions, then regenerate and commit the output with the source changes. Documentation generation needs Go and Terraform, not credentials or Docker. Documentation validation checks Registry page structure; it does not run Terraform examples.

`make generate` and `make testacc` deliberately fail until later stages implement GraphQL generation and container acceptance tests. The release workflow is preparation only and is gated by an unset `RELEASE_ENABLED` repository variable. Do not enable releases or configure signing secrets during the scaffold stage.

Read [AGENTS.md](AGENTS.md) before changing code. [DEVELOPMENT.md](DEVELOPMENT.md) records the serial implementation phases, upstream pins, and handoff.

## License

[Mozilla Public License 2.0](LICENSE).
