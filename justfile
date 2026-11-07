# Build the binary
build:
    go build -trimpath -ldflags='-s -w' -o powerlog .

# Remove build output
clean:
    rm -f powerlog

# Run locally, printing CSV rows to the terminal (Ctrl+C to stop)
run *args: build
    ./powerlog {{args}}

# Install and (re)start the system service (asks for sudo)
install: build
    sudo ./install.sh
