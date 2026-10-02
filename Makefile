default: fmt test build

build:
	go build -v ./...

# GraphQL client generation belongs to stage 2, not this scaffold.
generate:
	@echo "GraphQL client generation is not implemented yet. See DEVELOPMENT.md." >&2
	@exit 1

generate-docs:
	go tool tfplugindocs generate --provider-name twenty --rendered-provider-name Twenty

validate-docs:
	go tool tfplugindocs validate --provider-name twenty

fmt:
	go tool golangci-lint fmt
	terraform fmt -recursive examples/

fmt-check:
	go tool golangci-lint fmt --diff
	test -z "$$(gofmt -s -l .)"
	terraform fmt -check -recursive examples/

lint:
	go tool golangci-lint run ./...

test:
	go test -v -cover ./...

# No acceptance tests or container stack exist in stage 1.
testacc:
	@echo "Acceptance tests are not implemented yet. See DEVELOPMENT.md." >&2
	@exit 1

.PHONY: default build generate generate-docs validate-docs fmt fmt-check lint test testacc
