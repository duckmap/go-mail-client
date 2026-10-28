.PHONY: all build test vet demo clean

BINARY := gomail

all: build

build:
	go build -o $(BINARY) .

test:
	go test ./...

vet:
	go vet ./...

demo: build
	./$(BINARY) -demo

clean:
	rm -f $(BINARY)
