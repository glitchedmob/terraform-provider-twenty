default: fmt test build

build:
	go build -v ./...

# Offline generation from the committed, checksum-verified Metadata SDL.
generate:
	cd graphql && sha256sum --check SHA256SUMS
	go tool genqlient graphql/genqlient.yaml
	go tool genqlient graphql/genqlient.testbootstrap.yaml

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

# Acceptance tests and a disposable container stack belong to stage 3.
testacc:
	@echo "Acceptance tests are not implemented yet. See DEVELOPMENT.md." >&2
	@exit 1

.PHONY: default build generate generate-docs validate-docs fmt fmt-check lint test testacc
