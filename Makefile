.PHONY: test build tailwind seed

test:
	go test ./...

build:
	go build ./...

seed:
	go run ./cmd/seed

tailwind:
	tailwindcss -i static/css/input.css -o static/css/dist.css --minify
