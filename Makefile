default: fmt test build

build:
	go build -v ./...

# Offline generation from the committed, checksum-verified Metadata SDL.
generate:
	cd graphql && sha256sum --check SHA256SUMS
	go tool genqlient graphql/genqlient.yaml
	go tool genqlient graphql/genqlient.testbootstrap.yaml

# Documentation tools inspect schemas only. Never use ambient Twenty credentials.
generate-docs:
	env -u TWENTY_ENDPOINT -u TWENTY_EMAIL -u TWENTY_PASSWORD go tool tfplugindocs generate --provider-name twenty --rendered-provider-name Twenty

validate-docs:
	env -u TWENTY_ENDPOINT -u TWENTY_EMAIL -u TWENTY_PASSWORD go tool tfplugindocs validate --provider-name twenty

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

# Docker Compose and Terraform are required. Tests create and destroy their own stack.
# Do not forward instance credentials or Terraform logging settings into the suite.
testacc:
	env -u TWENTY_ENDPOINT -u TWENTY_EMAIL -u TWENTY_PASSWORD -u TF_LOG -u TF_LOG_CORE -u TF_LOG_PROVIDER -u TF_LOG_SDK -u TF_LOG_SDK_HELPER_RESOURCE -u TF_LOG_PATH -u TF_LOG_PATH_MASK -u TF_ACC_LOG -u TF_ACC_LOG_PATH -u TF_ACC_PERSIST_WORKING_DIR TF_ACC=1 go test -count=1 -v -timeout 25m -artifacts -outputdir="$(CURDIR)" ./internal/provider -run '^TestAcc'

.PHONY: default build generate generate-docs validate-docs fmt fmt-check lint test testacc
