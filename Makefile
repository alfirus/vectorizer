.PHONY: build run test docker-build docker-up docker-down clean

build:
	@echo "Building vectorizer..."
	go build -o vectorizer.exe .

run:
	@echo "Running vectorizer..."
	go run main.go

test:
	@echo "Running tests..."
	go test ./... -v

docker-build:
	@echo "Building Docker image..."
	docker compose build

docker-up:
	@echo "Starting services with docker-compose..."
	docker compose up -d

docker-down:
	@echo "Stopping services..."
	docker compose down

clean:
	@echo "Cleaning up..."
	rm -f vectorizer.exe
	go clean
