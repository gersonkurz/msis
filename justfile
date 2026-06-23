# msis build automation

set windows-shell := ["powershell.exe", "-NoProfile", "-Command"]

version := "3.0.3"
binary := "msis"
cmd_path := "./cmd/msis"
bootstrap_dir := "bootstrap"
build_time := datetime_utc("%Y-%m-%dT%H:%M:%SZ")
hook_project := "native/msi-simplica/msi-simplica.vcxproj"

# Default recipe: show available commands
default:
    @just --list

# Install/repair the WiX toolset + extensions msis needs (msis owns the version)
setup-wix:
    go run {{cmd_path}} /SETUP-WIX

# Fail early when not run from a Visual Studio 2026 Developer shell (msbuild on PATH).
[windows]
_require-devshell:
    @if ($env:VisualStudioVersion -ne "18.0") { Write-Error "build-hooks needs a Visual Studio 2026 Developer shell (Developer Command Prompt/PowerShell for VS 2026): expected VisualStudioVersion=18.0, found '$env:VisualStudioVersion'."; exit 1 }

# Compile + run the dependency-free unit test for the hook retain/cleanup core (hookcore.h).
[windows]
test-hooks: _require-devshell
    @New-Item -ItemType Directory -Force native/msi-simplica/test/bin | Out-Null
    cl /nologo /std:c++17 /EHsc /W4 native/msi-simplica/test/hookcore_test.cpp /Fe:native/msi-simplica/test/bin/hookcore_test.exe /Fo:native/msi-simplica/test/bin/ /link /SUBSYSTEM:CONSOLE
    native/msi-simplica/test/bin/hookcore_test.exe

[unix]
test-hooks:
    @echo "test-hooks targets Windows (MSVC); run from a VS 2026 Developer shell on Windows."

# Build the native installer-hook DLL (msi-simplica.dll) for x86/x64/arm64 and stage into templates/.
[windows]
build-hooks: _require-devshell test-hooks
    msbuild {{hook_project}} /t:Restore /p:Configuration=Release /p:Platform=x64 /nologo /v:minimal
    msbuild {{hook_project}} /t:Build /p:Configuration=Release /p:Platform=Win32 /m /nologo /v:minimal
    msbuild {{hook_project}} /t:Build /p:Configuration=Release /p:Platform=x64 /m /nologo /v:minimal
    msbuild {{hook_project}} /t:Build /p:Configuration=Release /p:Platform=ARM64 /m /nologo /v:minimal
    @just _stage-hooks

# Copy the freshly built hook DLLs into the template tree (Win32->x86).
[windows]
_stage-hooks:
    @New-Item -ItemType Directory -Force templates/x86, templates/x64, templates/arm64 | Out-Null
    @Copy-Item -Force native/msi-simplica/bin/Win32/Release/msi-simplica.dll templates/x86/msi-simplica.dll
    @Copy-Item -Force native/msi-simplica/bin/x64/Release/msi-simplica.dll templates/x64/msi-simplica.dll
    @Copy-Item -Force native/msi-simplica/bin/ARM64/Release/msi-simplica.dll templates/arm64/msi-simplica.dll
    @Write-Host "Staged msi-simplica.dll into templates/{x86,x64,arm64}"

[unix]
build-hooks:
    @echo "build-hooks targets Windows (MSVC); run from a VS 2026 Developer shell on Windows."

# Build for current platform
build:
    go build -ldflags "-s -w -X main.Version={{version}} -X main.BuildTime={{build_time}}" -o {{binary}}{{ext}} {{cmd_path}}
    @just _install-if-exists

# Copy exe + refresh templates if installed (requires elevation on Windows for Program Files)
[windows]
_install-if-exists:
    @if (Test-Path "C:\Program Files\MSIS") { Copy-Item -Force {{binary}}.exe "C:\Program Files\MSIS\msis.exe"; Write-Host "Updated installed version at C:\Program Files\MSIS\msis.exe" }
    @if (Test-Path "$env:LOCALAPPDATA\MSIS\templates") { $d = "$env:LOCALAPPDATA\MSIS\templates"; robocopy templates "$d" /MIR /XF msi-simplica.dll /NFL /NDL /NJH /NJS /NP /NS /NC | Out-Null; if ($LASTEXITCODE -ge 8) { throw "template sync (robocopy) failed: $LASTEXITCODE" }; foreach ($a in 'x86','x64','arm64') { if (Test-Path "templates\$a\msi-simplica.dll") { New-Item -ItemType Directory -Force "$d\$a" | Out-Null; Copy-Item -Force "templates\$a\msi-simplica.dll" "$d\$a\msi-simplica.dll" } }; Write-Host "Refreshed installed templates at $d (hook DLLs preserved; staged DLLs copied when present)" }; exit 0

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

# Run tests, write JUnit XML + JSON log
# Windows uses cmd /c: PowerShell mangles flags after `--` for external commands.
[windows]
test:
    cmd /c "go run gotest.tools/gotestsum@latest --jsonfile msis-test.log --junitfile msis-junit.xml --junitfile-hide-empty-pkg -- -p=1 ./..."

[unix]
test:
    go run gotest.tools/gotestsum@latest \
        --jsonfile msis-test.log \
        --junitfile msis-junit.xml \
        --junitfile-hide-empty-pkg \
        -- -p=1 ./...

# Run tests with verbose (testdox) output
[windows]
test-verbose:
    cmd /c "go run gotest.tools/gotestsum@latest --jsonfile msis-test.log --junitfile msis-junit.xml --junitfile-hide-empty-pkg --format testdox -- -p=1 -v ./..."

[unix]
test-verbose:
    go run gotest.tools/gotestsum@latest \
        --jsonfile msis-test.log \
        --junitfile msis-junit.xml \
        --junitfile-hide-empty-pkg \
        --format testdox \
        -- -p=1 -v ./...

# Run tests with coverage and generate Cobertura XML report
[windows]
coverage:
    cmd /c "go run gotest.tools/gotestsum@latest --jsonfile msis-test.log --junitfile msis-junit.xml --junitfile-hide-empty-pkg -- -p=1 -coverpkg=./... -coverprofile=msis-coverage.out ./..."
    cmd /c "go run github.com/boumenot/gocover-cobertura@latest < msis-coverage.out > msis-coverage.xml"

[unix]
coverage:
    go run gotest.tools/gotestsum@latest \
        --jsonfile msis-test.log \
        --junitfile msis-junit.xml \
        --junitfile-hide-empty-pkg \
        -- -p=1 -coverpkg=./... -coverprofile=msis-coverage.out ./...
    go run github.com/boumenot/gocover-cobertura@latest < msis-coverage.out > msis-coverage.xml

# Clean build artifacts
[unix]
clean:
    rm -f {{binary}} {{binary}}.exe

[windows]
clean:
    foreach ($f in '{{binary}}', '{{binary}}.exe') { if (Test-Path $f) { Remove-Item -Force $f } }

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
[unix]
fmt-check:
    @gofmt -l . | grep -q . && echo "Code not formatted. Run 'just fmt'" && exit 1 || echo "Code is formatted"

[windows]
fmt-check:
    @$files = gofmt -l .; if ($files) { Write-Host "Code not formatted. Run 'just fmt'"; Write-Host $files; exit 1 } else { Write-Host "Code is formatted" }

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
release: clean-bootstrap build-hooks build-windows-x64
    @echo "Preparing x64 release build..."
    cp {{bootstrap_dir}}/{{binary}}-x64.exe {{bootstrap_dir}}/msis.exe
    @echo "Building x64 MSI package..."
    cd {{bootstrap_dir}} && ./msis.exe {{msis_flags}} --template=../templates/minimal/template.wxs setup.msis
    @echo "Release build complete: {{bootstrap_dir}}/dist/msis-{{version}}-x64.msi"

[windows]
release: clean-bootstrap build-hooks build-windows-x64
    @echo "Preparing x64 release build..."
    Copy-Item {{bootstrap_dir}}\{{binary}}-x64.exe {{bootstrap_dir}}\msis.exe
    @echo "Building x64 MSI package..."
    Push-Location {{bootstrap_dir}}; try { .\msis.exe {{msis_flags}} --template=..\templates\minimal\template.wxs setup.msis } finally { Pop-Location }
    @echo "Release build complete: {{bootstrap_dir}}\dist\msis-{{version}}-x64.msi"

# Build release for x86, x64, and arm64, then create bundle
[unix]
release-all: clean-bootstrap build-hooks build-all
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
release-all: clean-bootstrap build-hooks build-all
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
