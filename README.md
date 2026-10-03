# Terraform provider for Twenty

Unreleased Terraform provider tested against Twenty v2.44.0. It authenticates an existing automation account with an in-memory password session, reads the current workspace and existing roles, and manages custom roles and declared email memberships.

All operations use Metadata GraphQL at `/metadata`, not Core GraphQL or CRM record APIs. Create sends invitations without waiting for login; accepted membership updates manage role assignments. Workspace settings and global users/passwords are not managed. API keys do not cover the intended membership operations in this Twenty release.

## Known teardown constraint

Combined teardown of an accepted member and its Terraform-owned custom role is not fully supported on Twenty v2.44.0. Membership removal succeeds, but a stale upstream role-assignment cache can block the following role deletion. Terraform retains the custom role in state and on the server; the removed member is absent from both. The provider reports a fixed error and does not retry or change other assignments to repair the cache.

Inspect membership and retained role state before taking further action. A version-specific [operator-controlled maintenance procedure](docs/guides/membership.md#operator-controlled-cache-maintenance-on-v2440) was verified in a disposable v2.44.0 container. It requires separate server access, then a deliberate destroy rerun. Unattended combined teardown remains unsupported. The provider stays Metadata-only and never runs maintenance or needs shell, cluster, or database credentials.

## Supported types

- `twenty_workspace` data source, current authenticated workspace identity, default role, domains/URLs, timestamps, and member count, with no selectors
- `twenty_role` data source, lookup by native UUID or exact label
- `twenty_role` resource, custom role CRUD, native UUID import, global booleans, and complete explicit permission flag ownership

- `twenty_workspace_member` resource, single-email invitations, stable workspace/email import, accepted role updates, revocation/removal, and expiration replacement

The role resource protects bootstrap/default/built-in roles and refuses assigned-role deletion. It does not manage assignments or object/field permission rows. Membership create refuses existing access until explicit import. Keep the operator and independent recovery administrator outside managed resources. See [role resource documentation](docs/resources/role.md), [membership lifecycle and recovery](docs/guides/membership.md), [permissions](docs/guides/permissions.md), and [import guidance](docs/guides/import.md).

## Configuration

See the [provider documentation](docs/index.md), [authentication guide](docs/guides/authentication.md), [role lookup](docs/data-sources/role.md), [current workspace](docs/data-sources/workspace.md), and [example configuration](examples/provider/provider.tf). The planned Registry address is `glitchedmob/twenty`; it is not published, so use a locally built binary with Terraform CLI `dev_overrides`.

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

## Initial IAM configuration

Inject sensitive ephemeral authentication at runtime. This example uses the authenticated workspace, looks up a built-in role, creates a custom role, and manages only declared emails. Import preexisting access first. Never include the automation or recovery administrator in the member map. This configuration demonstrates creation and assignment, but accepted-member plus custom-role destroy has the v2.44 teardown constraint above.

```terraform
variable "twenty_email" {
  type      = string
  sensitive = true
  ephemeral = true
}
variable "twenty_password" {
  type      = string
  sensitive = true
  ephemeral = true
}
provider "twenty" {
  endpoint = "https://twenty.example.com"
  email    = var.twenty_email
  password = var.twenty_password
}
data "twenty_workspace" "current" {}
data "twenty_role" "member" { label = "Member" }
resource "twenty_role" "triage" {
  label                      = "Support triage"
  can_read_all_object_records = true
  permission_flags           = []
}
resource "twenty_workspace_member" "declared" {
  for_each = {
    "alice@example.com" = twenty_role.triage.id
    "bob@example.com"   = data.twenty_role.member.id
  }
  email   = each.key
  role_id = each.value
}
output "workspace_id" { value = data.twenty_workspace.current.id }
```

Use Terraform 1.10 or later for ephemeral variables. The [complete example](examples/guides/membership/main.tf) includes provider requirements and member-map validation. Removing a declared key removes its access. Destroying accepted access can trigger Twenty's connected-account, workflow, chat-history, and global-user cascades. Review the [membership guide](docs/guides/membership.md) before applying.

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

The real-container suite covers session renewal, current workspace lookup without settings permissions, member-count refresh after a disposable invitation is accepted, role lookup, custom role CRUD/import/drift, explicit false/default values, null and empty strings, flag replacement and clearing, missing roles, actual settings permission denial, assigned-role deletion refusal, pending invitation create/update/import/revoke, server-mail acceptance with a stable state ID, accepted role drift/removal, existing-access import, and preservation of both bootstrap administrators. A separate Terraform-owned custom-role plus accepted-member regression verifies the expected classified destroy failure, retained role state/server presence, removed membership, and no automatic retry. Another regression reproduces that failure, explicitly runs the official targeted maintenance CLI as the test operator, then verifies one requested destroy succeeds without changing either administrator. See the exact versions and check results in [DEVELOPMENT.md](DEVELOPMENT.md).

Documentation commands also remove Twenty credential environment variables. They inspect schemas without login. The release workflow remains gated by an unset `RELEASE_ENABLED` repository variable. Releases and signing secrets need separate authorization.

Read [AGENTS.md](AGENTS.md) before changing code. [DEVELOPMENT.md](DEVELOPMENT.md) records the serial implementation phases, upstream pins, and handoff.

## License

Provider code uses [Mozilla Public License 2.0](LICENSE). The upstream Metadata SDL uses the preserved [Twenty client SDK MIT license](graphql/LICENSE); see [schema provenance](graphql/README.md).
