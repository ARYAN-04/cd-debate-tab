.PHONY: test build tailwind

test:
	go test ./...

build:
	go build ./...

tailwind:
	tailwindcss -i static/css/input.css -o static/css/dist.css --minify
