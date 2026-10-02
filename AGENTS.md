# Agent instructions

## Scope and safety

This repository starts as a configuration-only scaffold. Do not add GraphQL generation, authentication, or resources during stage 1. See the serial stage checklist in [DEVELOPMENT.md](DEVELOPMENT.md). Finish each stage and hand it off before starting the next.

The eventual provider manages IAM and configuration through Twenty's Metadata GraphQL endpoint, `/metadata`. Do not add CRM record CRUD, Core GraphQL `/graphql` calls, or Core REST record operations.

Do not access production Twenty instances, infrastructure repositories, Kubernetes clusters, databases, secret stores, or live user accounts. Do not use local ambient credentials for tests. Future acceptance tests must create a disposable local container stack with test-only accounts and destroy only that stack.

Keep the automation bootstrap identity and its own role outside Terraform-managed resources. Reject attempts to remove or change that identity through membership management. Preserve an independent recovery administrator and guard against removing the last administrator or workspace member. Do not evict undeclared members.

Never commit credentials, session tokens, state, saved plans, real member lists, or private deployment details. Do not use server signing secrets to mint credentials. Do not weaken MFA, CAPTCHA, SSO, or email-verification settings on a live instance.

No releases, version tags, signing secrets, or infrastructure changes are authorized by the scaffold stage.

## Commands

Run from the repository root:

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

- `make generate` is intentionally unavailable until stage 2 adds genqlient.
- `make testacc` is intentionally unavailable until stage 3 adds container tests.
- Use the tool versions in `go.mod`, not global linters or generators.
- CI pins Terraform 1.14.7. If using another version locally, record it in the handoff.
- Use single-line Conventional Commit messages with no scope, body, or footer.

## Generated files

Do not edit `docs/` directly. Edit prose in `templates/`, Terraform examples in `examples/`, and schema descriptions in `internal/provider/`. Run `make generate-docs` and `make validate-docs`, then commit sources and generated pages together.

Stage 2 should place the pinned SDL and selected operations in `graphql/`, configure genqlient there, and commit generated code under `internal/client/`. Generated client files must have generator headers and be reproducible with `make generate`. Do not use live introspection. Preserve upstream schema licensing and record its source and checksum.

Never bypass the no-configuration early return in `TwentyProvider.Configure`. Schema tools must work without credentials and must not make network requests. The current `providerConfig` is an in-memory placeholder, not an authenticated client. Replace it only when implementing and testing authentication in stage 3.

## Upstream target and references

Intended container target:

```text
twentycrm/twenty:v2.44.0@sha256:01fb6d2c00397976fd7613dbeb9703b514b52fb6270339b7a326a2a975d15b26
```

Stage 3 must use this image for both server and worker and pin its supporting services in a new `integration/compose.yml`. Do not start a Twenty stack during stage 1 or generate a client during this stage.

The upstream source pin is `twentyhq/twenty` commit `f7a4720eb4d479bfa3f6634bcdd703bb4de66600`, corresponding to v2.44.0. The [Metadata SDL](https://github.com/twentyhq/twenty/blob/f7a4720eb4d479bfa3f6634bcdd703bb4de66600/packages/twenty-client-sdk/src/metadata/generated/schema.graphql) is the planned genqlient input. Follow the resolver links in [DEVELOPMENT.md](DEVELOPMENT.md) when choosing operations.

Use the public [Kaneo provider](https://github.com/glitchedmob/terraform-provider-kaneo) as the structural reference. Do not import its REST client, resource implementations, acceptance stack, or unused dependencies.
