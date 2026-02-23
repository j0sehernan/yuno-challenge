.PHONY: build run test race-test demo clean seed coverage docker-up docker-down

BUILD_DIR := bin
BINARY := idempotency-shield

build:
	go build -o $(BUILD_DIR)/$(BINARY) ./cmd/server

run: build
	./$(BUILD_DIR)/$(BINARY)

test:
	go test ./... -v -count=1

race-test:
	go test ./... -race -count=5 -v

coverage:
	go test ./internal/... -coverprofile=coverage.out -covermode=atomic -count=1
	go tool cover -func=coverage.out | tail -1

coverage-html: coverage
	go tool cover -html=coverage.out -o coverage.html
	@echo "Open coverage.html in your browser"

demo:
	bash scripts/demo.sh

seed:
	go run scripts/seed_data.go

docker-up:
	docker-compose up --build -d

docker-down:
	docker-compose down -v

clean:
	rm -rf $(BUILD_DIR) coverage.out coverage.html
