# msis build automation

version := "3.0.0"
binary := "msis"
cmd_path := "./cmd/msis"
dist_dir := "dist"

# Default recipe: show available commands
default:
    @just --list

# Build for current platform
build:
    go build -ldflags "-s -w -X main.Version={{version}}" -o {{binary}}{{ext}} {{cmd_path}}

# Build for Windows x64 (amd64)
build-windows-x64:
    GOOS=windows GOARCH=amd64 go build -ldflags "-s -w -X main.Version={{version}}" -o {{dist_dir}}/{{binary}}-x64.exe {{cmd_path}}

# Build for Windows x86 (32-bit)
build-windows-x86:
    GOOS=windows GOARCH=386 go build -ldflags "-s -w -X main.Version={{version}}" -o {{dist_dir}}/{{binary}}-x86.exe {{cmd_path}}

# Build for Windows ARM64
build-windows-arm64:
    GOOS=windows GOARCH=arm64 go build -ldflags "-s -w -X main.Version={{version}}" -o {{dist_dir}}/{{binary}}-arm64.exe {{cmd_path}}

# Build all Windows targets (x64 + x86 + arm64)
build-all: clean-dist build-windows-x64 build-windows-x86 build-windows-arm64
    @echo "Built all targets in {{dist_dir}}/"

# Run tests
test:
    go test ./...

# Run tests with verbose output
test-verbose:
    go test -v ./...

# Clean build artifacts
clean:
    rm -f {{binary}} {{binary}}.exe

# Clean dist directory
clean-dist:
    rm -rf {{dist_dir}}
    mkdir -p {{dist_dir}}

# Clean everything
clean-all: clean clean-dist

# Format code
fmt:
    gofmt -w .

# Check formatting
fmt-check:
    @gofmt -l . | grep -q . && echo "Code not formatted. Run 'just fmt'" && exit 1 || echo "Code is formatted"

# Run go vet
vet:
    go vet ./...

# Run all checks
check: fmt-check vet test

# Platform extension helper
ext := if os() == "windows" { ".exe" } else { "" }

# Build release MSI package (x64 only)
release: build-windows-x64
    @echo "Preparing x64 release build..."
    cp {{dist_dir}}/{{binary}}-x64.exe {{dist_dir}}/msis.exe
    @echo "Building x64 MSI package..."
    {{dist_dir}}/msis.exe --build --templatefolder=templates --template=templates/minimal/template.wxs setup.msis
    @echo "Release build complete: dist/msis-{{version}}-x64.msi"

# Build release for x86, x64, and arm64, then create bundle
release-all: build-all
    @echo "=== Building x64 MSI ==="
    cp {{dist_dir}}/{{binary}}-x64.exe {{dist_dir}}/msis.exe
    {{dist_dir}}/msis.exe --build --templatefolder=templates --template=templates/minimal/template.wxs setup.msis
    @echo "=== Building x86 MSI ==="
    cp {{dist_dir}}/{{binary}}-x86.exe {{dist_dir}}/msis.exe
    {{dist_dir}}/{{binary}}-x64.exe --build --templatefolder=templates --template=templates/minimal-x86/template.wxs setup-x86.msis
    @echo "=== Building ARM64 MSI ==="
    cp {{dist_dir}}/{{binary}}-arm64.exe {{dist_dir}}/msis.exe
    {{dist_dir}}/{{binary}}-x64.exe --build --templatefolder=templates --template=templates/minimal/template.wxs setup-arm64.msis
    @echo "=== Building Bundle ==="
    {{dist_dir}}/{{binary}}-x64.exe --build --templatefolder=templates setup-bundle.msis
    @echo "=== All release builds complete ==="
    @echo "  - dist/msis-{{version}}-x64.msi"
    @echo "  - dist/msis-{{version}}-x86.msi"
    @echo "  - dist/msis-{{version}}-arm64.msi"
    @echo "  - dist/msis-{{version}}-setup.exe"
