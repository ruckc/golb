golb: clean tidy fmt sec vet test lint
	mkdir dist ; go build -o dist/golb cmd/golb/main.go

lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run ./...

sec:
	go run github.com/securego/gosec/v2/cmd/gosec@latest -r .

test:
	go test -v ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

tidy:
	go mod tidy

clean:
	rm -rf dist

.PHONY: clean
