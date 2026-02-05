# msis build automation

version := "3.0.0"
binary := "msis"
cmd_path := "./cmd/msis"
bootstrap_dir := "bootstrap"
build_time := `date -u +"%Y-%m-%d %H:%M:%S UTC"`

# Default recipe: show available commands
default:
    @just --list

# Build for current platform
build:
    go build -ldflags "-s -w -X main.Version={{version}} -X 'main.BuildTime={{build_time}}'" -o {{binary}}{{ext}} {{cmd_path}}
    @just _install-if-exists

# Copy to Program Files if installed (requires elevation on Windows)
[windows]
_install-if-exists:
    @if [ -d "/c/Program Files/MSIS" ]; then cp {{binary}}.exe "/c/Program Files/MSIS/msis.exe" && echo "Updated installed version at C:\\Program Files\\MSIS\\msis.exe"; fi

[unix]
_install-if-exists:
    @echo "Install location check skipped (not Windows)"

# Build for Windows x64 (amd64)
build-windows-x64:
    GOOS=windows GOARCH=amd64 go build -ldflags "-s -w -X main.Version={{version}} -X 'main.BuildTime={{build_time}}'" -o {{bootstrap_dir}}/{{binary}}-x64.exe {{cmd_path}}

# Build for Windows x86 (32-bit)
build-windows-x86:
    GOOS=windows GOARCH=386 go build -ldflags "-s -w -X main.Version={{version}} -X 'main.BuildTime={{build_time}}'" -o {{bootstrap_dir}}/{{binary}}-x86.exe {{cmd_path}}

# Build for Windows ARM64
build-windows-arm64:
    GOOS=windows GOARCH=arm64 go build -ldflags "-s -w -X main.Version={{version}} -X 'main.BuildTime={{build_time}}'" -o {{bootstrap_dir}}/{{binary}}-arm64.exe {{cmd_path}}

# Build all Windows targets (x64 + x86 + arm64)
build-all: build-windows-x64 build-windows-x86 build-windows-arm64
    @echo "Built all targets in {{bootstrap_dir}}/"

# Run tests
test:
    go test ./...

# Run tests with verbose output
test-verbose:
    go test -v ./...

# Clean build artifacts
clean:
    rm -f {{binary}} {{binary}}.exe

# Clean bootstrap directory (binaries and dist)
clean-bootstrap:
    rm -f {{bootstrap_dir}}/*.exe
    rm -rf {{bootstrap_dir}}/dist
    mkdir -p {{bootstrap_dir}}/dist

# Clean everything
clean-all: clean clean-bootstrap

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
release: clean-bootstrap build-windows-x64
    @echo "Preparing x64 release build..."
    cp {{bootstrap_dir}}/{{binary}}-x64.exe {{bootstrap_dir}}/msis.exe
    @echo "Building x64 MSI package..."
    cd {{bootstrap_dir}} && ./msis.exe --build --templatefolder=../templates --template=../templates/minimal/template.wxs setup.msis
    @echo "Release build complete: {{bootstrap_dir}}/dist/msis-{{version}}-x64.msi"

# Build release for x86, x64, and arm64, then create bundle
release-all: clean-bootstrap build-all
    @echo "=== Building x64 MSI ==="
    cp {{bootstrap_dir}}/{{binary}}-x64.exe {{bootstrap_dir}}/msis.exe
    cd {{bootstrap_dir}} && ./msis.exe --build --templatefolder=../templates --template=../templates/minimal/template.wxs setup.msis
    @echo "=== Building x86 MSI ==="
    cp {{bootstrap_dir}}/{{binary}}-x86.exe {{bootstrap_dir}}/msis.exe
    cd {{bootstrap_dir}} && ./{{binary}}-x64.exe --build --templatefolder=../templates --template=../templates/minimal-x86/template.wxs setup-x86.msis
    @echo "=== Building ARM64 MSI ==="
    cp {{bootstrap_dir}}/{{binary}}-arm64.exe {{bootstrap_dir}}/msis.exe
    cd {{bootstrap_dir}} && ./{{binary}}-x64.exe --build --templatefolder=../templates --template=../templates/minimal/template.wxs setup-arm64.msis
    @echo "=== Building Bundle ==="
    cd {{bootstrap_dir}} && ./{{binary}}-x64.exe --build --templatefolder=../templates setup-bundle.msis
    @echo "=== All release builds complete ==="
    @echo "  - {{bootstrap_dir}}/dist/msis-{{version}}-x64.msi"
    @echo "  - {{bootstrap_dir}}/dist/msis-{{version}}-x86.msi"
    @echo "  - {{bootstrap_dir}}/dist/msis-{{version}}-arm64.msi"
    @echo "  - {{bootstrap_dir}}/dist/msis-{{version}}-setup.exe"
