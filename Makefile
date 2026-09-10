.PHONY: default check test build image

IMAGE_NAME := ghcr.io/tpaulus/external-dns-bunny-webhook
BINARY_NAME=external-dns-bunny-webhook
CMD_PATH=./cmd/webhook

default: test build

build:
	CGO_ENABLED=0 go build -a --trimpath --installsuffix cgo --ldflags="-s" -o $(BINARY_NAME) $(CMD_PATH)

test:
	go test -v -cover ./...

check:
	golangci-lint run

image:
	docker build -t $(IMAGE_NAME) .

protoc:
	protoc --proto_path . ./grpc.proto --go-grpc_out=./ --go_out=./