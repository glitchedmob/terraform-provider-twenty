# Agent instructions

## Scope and safety

Stages 1, 2, 3, and steps 4A/4B/4C are complete. Initial IAM work continues only under assigned authorization. The current role-invitation review fix permits parallel independent work in isolated worktrees. Step 4C implemented `twenty_workspace_member` invitation and accepted-member management. Implement only the assigned work and keep each branch's file ownership separate; the parent agent manages the PR stack. Read the serial checklist and tested handoff in [DEVELOPMENT.md](DEVELOPMENT.md) before continuing. Workspace settings and object/field permission ownership are outside this authorization.

The provider uses Twenty's Metadata GraphQL endpoint, `/metadata`. Implemented types are the role/member resources and the role/workspace data sources. Workspace lookup reads only the current authenticated workspace, with no selectors or mutations. Do not add workspace resources or workspace mutation calls. Membership manages only declared normalized emails in the authenticated workspace; existing access requires explicit import. Keep global users/passwords outside resources. Do not add CRM record CRUD, Core GraphQL `/graphql` calls, or Core REST record operations.

Do not access production Twenty instances, infrastructure repositories, Kubernetes clusters, databases, secret stores, or live user accounts. Do not use local ambient credentials for tests. Acceptance tests must create a disposable local container stack with test-only automation and recovery accounts and destroy only that stack.

Keep the automation bootstrap identity and its own role outside Terraform-managed resources. Reject attempts to remove or change that identity through membership management. Preserve an independent recovery administrator and guard against removing the last administrator or workspace member. Do not evict undeclared members.

Never commit credentials, session tokens, state, saved plans, real member lists, or private deployment details. Do not use server signing secrets to mint credentials. Do not weaken MFA, CAPTCHA, SSO, or email-verification settings on a live instance.

Implementation-stage authorization does not authorize releases, version tags, signing secrets, or infrastructure changes.

## Commands

Run from the repository root:

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

- `make generate` verifies the committed SDL/license checksums and runs the pinned genqlient tool offline. See `graphql/README.md` for provenance and optional-input contracts.
- `make testacc` requires Docker Compose and Terraform. It sets `TF_ACC=1`, removes ambient Twenty credentials and Terraform logging settings, and uses a bounded timeout. Never point it at a live deployment.
- Documentation targets remove Twenty credential environment variables. Schema tools must not log in, even if credentials exist in the caller's environment.
- Acceptance diagnostics go under ignored `_artifacts/`. Redact credentials, session tokens, and private member data before writing or uploading them.
- Use the tool versions in `go.mod`, not global linters or generators.
- CI pins Terraform 1.14.7. If using another version locally, record it in the handoff.
- Use single-line Conventional Commit messages with no scope, body, or footer.

## Generated files

Do not edit `docs/` directly. Edit prose in `templates/`, Terraform examples in `examples/`, and schema descriptions in `internal/provider/`. Run `make generate-docs` and `make validate-docs`, then commit sources and generated pages together.

The pinned SDL, genqlient configurations, and selected operations live in `graphql/`; generated code lives under `internal/client/`. Keep disposable onboarding operations in the separate `testbootstrap` package and out of provider code. Generated client files must have generator headers and be reproducible with `make generate`. Do not use live introspection. Preserve upstream schema licensing and record its source and checksum.

Never bypass the absent or raw-null configuration early return in `TwentyProvider.Configure`. Schema tools must work without credentials and must not make network requests. An explicit empty provider block may use environment fallbacks and authenticate. Shared `ClientData` holds an in-memory `client.Session`, not plaintext credentials or persisted tokens.

## Upstream target and references

Authorized disposable container target:

```text
twentycrm/twenty:v2.44.0@sha256:01fb6d2c00397976fd7613dbeb9703b514b52fb6270339b7a326a2a975d15b26
```

Use this image for both server and worker and pin supporting services in `integration/compose.yml`. Container testing is authorized only for the disposable IAM acceptance stack. It does not authorize deployment or infrastructure changes.

The upstream source pin is `twentyhq/twenty` commit `f7a4720eb4d479bfa3f6634bcdd703bb4de66600`, corresponding to v2.44.0. The [Metadata SDL](https://github.com/twentyhq/twenty/blob/f7a4720eb4d479bfa3f6634bcdd703bb4de66600/packages/twenty-client-sdk/src/metadata/generated/schema.graphql) is the committed genqlient input. Follow the resolver links in [DEVELOPMENT.md](DEVELOPMENT.md) when choosing operations.

Use the public [Kaneo provider](https://github.com/glitchedmob/terraform-provider-kaneo) as the structural reference. Do not import its REST client, resource implementations, acceptance stack, or unused dependencies.
