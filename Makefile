.PHONY: test-cert test build clean

# Run only the cert package tests with race detector
test-cert:
	go test -v -race ./internal/cert/...

# Run all tests
test:
	go test -v -race ./...

# Build the binary
build:
	go build -o shiv .

clean:
	rm -f shiv
