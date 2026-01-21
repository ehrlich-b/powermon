.PHONY: build clean

build:
	@mkdir -p bin
	go build -o bin/powermon .

clean:
	rm -rf bin
