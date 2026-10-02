# Terraform provider for Twenty

Unreleased Terraform provider tested against Twenty v2.44.0. The current subset authenticates an existing automation account with an in-memory password session and reads existing roles with the `twenty_role` data source. No resources are implemented.

All operations use Metadata GraphQL at `/metadata`, not Core GraphQL or CRM record APIs. Broader IAM resources are planned for stage 4 and are not part of the authorized stage-3 work. API keys do not cover the intended membership operations in this Twenty release.

## Configuration

See the [provider documentation](docs/index.md), [authentication guide](docs/guides/authentication.md), [role lookup](docs/data-sources/role.md), and [example configuration](examples/provider/provider.tf). The planned Registry address is `glitchedmob/twenty`; it is not published, so use a locally built binary with Terraform CLI `dev_overrides`.

| Attribute | Environment fallback |
| --- | --- |
| `endpoint` | `TWENTY_ENDPOINT` |
| `email` | `TWENTY_EMAIL` |
| `password` | `TWENTY_PASSWORD` |

All three values are required for authentication. Email must be a bare ASCII mailbox address, without a display name or comments. Explicit attributes override environment fallbacks, including empty strings. Empty explicit values and unknown configuration fail rather than falling back. Endpoint and email whitespace is trimmed; password bytes are preserved. There is no default endpoint.

Use an HTTPS instance base URL without a `/metadata` suffix, other paths, credentials, query, or fragment. Redirects are rejected. `allow_insecure_http = true` permits deliberate local tests on `localhost` or literal loopback IP addresses only. It has no environment fallback and does not disable HTTPS certificate verification.

Each authenticated configuration starts a new session and verifies the active workspace and member. Access and rotating refresh tokens remain in provider-process memory. Absent or raw-null configuration returns without reading credentials or making requests, so schema tools remain safe. An explicit empty provider block uses environment fallbacks and authenticates.

Use a dedicated verified automation identity that supports password sign-in. Interactive MFA, CAPTCHA, SSO-only sign-in, and unverified email are unsupported; do not weaken those settings on a live instance. Keep the bootstrap identity and its own role outside Terraform-managed resources and preserve an independent recovery administrator.

Inject credentials outside checked-in Terraform files. Sensitive schema attributes redact display output but do not keep their sources out of saved plans. Use environment secret injection or sensitive ephemeral inputs, not persisted token attributes or outputs.

## Development

The layout and tooling follow [terraform-provider-kaneo](https://github.com/glitchedmob/terraform-provider-kaneo), using HashiCorp Terraform Plugin Framework and MPL-2.0.

Requirements:

- Go 1.27.1, as pinned in `go.mod`
- Terraform 1.14.7 for the documentation checks used in CI
- No Twenty instance or credentials for unit, build, generation, or documentation checks
- Docker with Compose for disposable acceptance tests

```shell
go mod download
make generate
make fmt
make fmt-check
make lint
make test
make build
make generate-docs
make validate-docs
```

`make lint` uses golangci-lint v2.13.2 and documentation commands use tfplugindocs v0.25.0 through `go tool`. These tools and genqlient v0.8.1 are pinned in `go.mod`; no global installs are needed. `make build` compiles all packages. To build a binary for local Terraform `dev_overrides`, run `go build -o terraform-provider-twenty .`.

`docs/` is generated. Edit `templates/`, `examples/`, or Go schema descriptions, then regenerate and commit the output with the source changes. Documentation generation needs Go and Terraform, not credentials or Docker. Documentation validation checks Registry page structure; it does not run Terraform examples.

`make generate` verifies the committed Metadata SDL and license checksums, then generates selected Go operations without Terraform, Docker, credentials, or schema introspection. See [graphql/README.md](graphql/README.md) for provenance, update instructions, and optional-input contracts.

`make testacc` sets `TF_ACC=1` and runs a bounded suite against its own disposable stack from `integration/compose.yml`. Both Twenty server and worker use the v2.44.0 digest recorded in [DEVELOPMENT.md](DEVELOPMENT.md). The suite supplies test-only credentials and tears down only its own containers and volumes. The target removes ambient Twenty credentials and Terraform logging settings. Never use a live deployment for acceptance tests. Failure diagnostics belong in ignored `_artifacts/` and must omit credentials, tokens, and private member data.

The real-container suite has passed login and identity checks, two server-issued token renewals, role ID/label lookup using environment-only credentials, missing-role and wrong-password errors, actual `ROLES` permission denial, and preservation of both bootstrap administrators. See the exact versions and check results in [DEVELOPMENT.md](DEVELOPMENT.md).

Documentation commands also remove Twenty credential environment variables. They inspect schemas without login. The release workflow remains gated by an unset `RELEASE_ENABLED` repository variable. Releases and signing secrets need separate authorization.

Read [AGENTS.md](AGENTS.md) before changing code. [DEVELOPMENT.md](DEVELOPMENT.md) records the serial implementation phases, upstream pins, and handoff.

## License

Provider code uses [Mozilla Public License 2.0](LICENSE). The upstream Metadata SDL uses the preserved [Twenty client SDK MIT license](graphql/LICENSE); see [schema provenance](graphql/README.md).
