# msis build automation

set windows-shell := ["powershell.exe", "-NoProfile", "-Command"]

version := "3.0.1"
binary := "msis"
cmd_path := "./cmd/msis"
bootstrap_dir := "bootstrap"
build_time := datetime_utc("%Y-%m-%dT%H:%M:%SZ")

# Default recipe: show available commands
default:
    @just --list

# Build for current platform
build:
    go build -ldflags "-s -w -X main.Version={{version}} -X main.BuildTime={{build_time}}" -o {{binary}}{{ext}} {{cmd_path}}
    @just _install-if-exists

# Copy to Program Files if installed (requires elevation on Windows)
[windows]
_install-if-exists:
    @if (Test-Path "C:\Program Files\MSIS") { Copy-Item {{binary}}.exe "C:\Program Files\MSIS\msis.exe"; Write-Host "Updated installed version at C:\Program Files\MSIS\msis.exe" }

[unix]
_install-if-exists:
    @echo "Install location check skipped (not Windows)"

# Build for Windows x64 (amd64)
[unix]
build-windows-x64:
    GOOS=windows GOARCH=amd64 go build -ldflags "-s -w -X main.Version={{version}} -X main.BuildTime={{build_time}}" -o {{bootstrap_dir}}/{{binary}}-x64.exe {{cmd_path}}

[windows]
build-windows-x64:
    $env:GOARCH='amd64'; go build -ldflags "-s -w -X main.Version={{version}} -X main.BuildTime={{build_time}}" -o {{bootstrap_dir}}\{{binary}}-x64.exe {{cmd_path}}

# Build for Windows x86 (32-bit)
[unix]
build-windows-x86:
    GOOS=windows GOARCH=386 go build -ldflags "-s -w -X main.Version={{version}} -X main.BuildTime={{build_time}}" -o {{bootstrap_dir}}/{{binary}}-x86.exe {{cmd_path}}

[windows]
build-windows-x86:
    $env:GOARCH='386'; go build -ldflags "-s -w -X main.Version={{version}} -X main.BuildTime={{build_time}}" -o {{bootstrap_dir}}\{{binary}}-x86.exe {{cmd_path}}

# Build for Windows ARM64
[unix]
build-windows-arm64:
    GOOS=windows GOARCH=arm64 go build -ldflags "-s -w -X main.Version={{version}} -X main.BuildTime={{build_time}}" -o {{bootstrap_dir}}/{{binary}}-arm64.exe {{cmd_path}}

[windows]
build-windows-arm64:
    $env:GOARCH='arm64'; go build -ldflags "-s -w -X main.Version={{version}} -X main.BuildTime={{build_time}}" -o {{bootstrap_dir}}\{{binary}}-arm64.exe {{cmd_path}}

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
[unix]
clean:
    rm -f {{binary}} {{binary}}.exe

[windows]
clean:
    Remove-Item -Force -ErrorAction SilentlyContinue {{binary}}, {{binary}}.exe

# Clean bootstrap directory (binaries and dist)
[unix]
clean-bootstrap:
    rm -f {{bootstrap_dir}}/*.exe
    rm -rf {{bootstrap_dir}}/dist
    mkdir -p {{bootstrap_dir}}/dist

[windows]
clean-bootstrap:
    Remove-Item -Force -ErrorAction SilentlyContinue {{bootstrap_dir}}\*.exe
    if (Test-Path {{bootstrap_dir}}\dist) { Remove-Item -Recurse -Force {{bootstrap_dir}}\dist }
    New-Item -ItemType Directory -Force {{bootstrap_dir}}\dist | Out-Null

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

# Common msis flags for bootstrap builds
msis_flags := "--build --templatefolder=../templates /SET:PRODUCT_VERSION=" + version

# Build release MSI package (x64 only)
[unix]
release: clean-bootstrap build-windows-x64
    @echo "Preparing x64 release build..."
    cp {{bootstrap_dir}}/{{binary}}-x64.exe {{bootstrap_dir}}/msis.exe
    @echo "Building x64 MSI package..."
    cd {{bootstrap_dir}} && ./msis.exe {{msis_flags}} --template=../templates/minimal/template.wxs setup.msis
    @echo "Release build complete: {{bootstrap_dir}}/dist/msis-{{version}}-x64.msi"

[windows]
release: clean-bootstrap build-windows-x64
    @echo "Preparing x64 release build..."
    Copy-Item {{bootstrap_dir}}\{{binary}}-x64.exe {{bootstrap_dir}}\msis.exe
    @echo "Building x64 MSI package..."
    Push-Location {{bootstrap_dir}}; try { .\msis.exe {{msis_flags}} --template=..\templates\minimal\template.wxs setup.msis } finally { Pop-Location }
    @echo "Release build complete: {{bootstrap_dir}}\dist\msis-{{version}}-x64.msi"

# Build release for x86, x64, and arm64, then create bundle
[unix]
release-all: clean-bootstrap build-all
    @echo "=== Building x64 MSI ==="
    cp {{bootstrap_dir}}/{{binary}}-x64.exe {{bootstrap_dir}}/msis.exe
    cd {{bootstrap_dir}} && ./msis.exe {{msis_flags}} --template=../templates/minimal/template.wxs setup.msis
    @echo "=== Building x86 MSI ==="
    cp {{bootstrap_dir}}/{{binary}}-x86.exe {{bootstrap_dir}}/msis.exe
    cd {{bootstrap_dir}} && ./{{binary}}-x64.exe {{msis_flags}} --template=../templates/minimal-x86/template.wxs /SET:PLATFORM=x86 setup.msis
    @echo "=== Building ARM64 MSI ==="
    cp {{bootstrap_dir}}/{{binary}}-arm64.exe {{bootstrap_dir}}/msis.exe
    cd {{bootstrap_dir}} && ./{{binary}}-x64.exe {{msis_flags}} --template=../templates/minimal/template.wxs /SET:PLATFORM=arm64 setup.msis
    @echo "=== Building Bundle ==="
    cd {{bootstrap_dir}} && ./{{binary}}-x64.exe {{msis_flags}} setup-bundle.msis
    @echo "=== All release builds complete ==="
    @echo "  - {{bootstrap_dir}}/dist/msis-{{version}}-x64.msi"
    @echo "  - {{bootstrap_dir}}/dist/msis-{{version}}-x86.msi"
    @echo "  - {{bootstrap_dir}}/dist/msis-{{version}}-arm64.msi"
    @echo "  - {{bootstrap_dir}}/dist/msis-{{version}}-setup.exe"

[windows]
release-all: clean-bootstrap build-all
    @echo "=== Building x64 MSI ==="
    Copy-Item {{bootstrap_dir}}\{{binary}}-x64.exe {{bootstrap_dir}}\msis.exe
    Push-Location {{bootstrap_dir}}; try { .\msis.exe {{msis_flags}} --template=..\templates\minimal\template.wxs setup.msis } finally { Pop-Location }
    @echo "=== Building x86 MSI ==="
    Copy-Item {{bootstrap_dir}}\{{binary}}-x86.exe {{bootstrap_dir}}\msis.exe
    Push-Location {{bootstrap_dir}}; try { .\{{binary}}-x64.exe {{msis_flags}} --template=..\templates\minimal-x86\template.wxs /SET:PLATFORM=x86 setup.msis } finally { Pop-Location }
    @echo "=== Building ARM64 MSI ==="
    Copy-Item {{bootstrap_dir}}\{{binary}}-arm64.exe {{bootstrap_dir}}\msis.exe
    Push-Location {{bootstrap_dir}}; try { .\{{binary}}-x64.exe {{msis_flags}} --template=..\templates\minimal\template.wxs /SET:PLATFORM=arm64 setup.msis } finally { Pop-Location }
    @echo "=== Building Bundle ==="
    Push-Location {{bootstrap_dir}}; try { .\{{binary}}-x64.exe {{msis_flags}} setup-bundle.msis } finally { Pop-Location }
    @echo "=== All release builds complete ==="
    @echo "  - {{bootstrap_dir}}\dist\msis-{{version}}-x64.msi"
    @echo "  - {{bootstrap_dir}}\dist\msis-{{version}}-x86.msi"
    @echo "  - {{bootstrap_dir}}\dist\msis-{{version}}-arm64.msi"
    @echo "  - {{bootstrap_dir}}\dist\msis-{{version}}-setup.exe"
