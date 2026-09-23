# msis build automation

set windows-shell := ["powershell.exe", "-NoProfile", "-Command"]

version := "3.0.5"
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
    just vet-cross
    just vet-tools

# The build fixtures are //go:build windows because they drive the real wix CLI, so a helper
# defined among them compiles nowhere else. go vet type-checks test files, which makes this the
# cheapest check that the platform-independent tests still build off Windows.
[unix]
vet-cross:
    GOOS=linux go vet ./...

# One line, with the native command LAST: just runs each recipe line in its own shell, so the
# variable would not survive to a second line, and a PowerShell recipe only reports a native
# command's failure when that command is the last thing it runs.
[windows]
vet-cross:
    $env:GOOS = 'linux'; go vet ./...

# go vet and go test are MODULE-scoped, so ./... at the root does not reach a nested module.
# tools/sbom-index is its own module on purpose (#29 D11: the SQLite driver must stay out of the
# root module's requirement graph), which means its checks have to be invoked separately or they
# would simply never run.
#
# `go -C` rather than a cd/Push-Location wrapper, and the difference is not cosmetic: a
# PowerShell recipe ending in `finally { Pop-Location }` exits 0 even when the command inside
# failed, so `just vet` and `just check` would have passed with a failing nested module. `go -C`
# is one native command whose exit status just sees directly, and it needs no platform variants.
vet-tools:
    go -C tools/sbom-index vet ./...

test-tools:
    go -C tools/sbom-index test -count=1 ./...

# Build the SBOM index over a corpus of CycloneDX documents (see tools/sbom-index/README.md).
#
# The corpus is made absolute - the tool runs inside its own module directory, so a path
# relative to the repository root would resolve somewhere else entirely - and then QUOTED. An
# unquoted path split at its spaces, which a checkout under "C:\My Projects" produces without
# anyone passing an odd argument; the tail then arrived as a positional argument, and Go's flag
# package stops parsing at the first of those, so a later -query was ignored and the command
# exited 0 having done nothing. The tool now rejects positional arguments outright, so a slip
# like that is loud rather than silent, but the quoting is the actual fix.
#
# The two shells escape a literal quote differently - PowerShell doubles it, sh backslashes it -
# so the recipe is split rather than papered over with one form that is wrong on one of them.
[windows]
sbom-index corpus=(bootstrap_dir / "dist"):
    go -C tools/sbom-index run . -corpus '{{replace(absolute_path(corpus), "'", "''")}}'

[unix]
sbom-index corpus=(bootstrap_dir / "dist"):
    go -C tools/sbom-index run . -corpus {{quote(absolute_path(corpus))}}

# Ask the index one of its documented questions: `just sbom-query coverage`, or
# `just sbom-query match-digest "-arg sha256=69202d..."`. Run `just sbom-query` for the list.
#
# `args` is passed to the shell as written, because it carries several tokens by design.
[windows]
sbom-query query="" args="" corpus=(bootstrap_dir / "dist"):
    go -C tools/sbom-index run . -db '{{replace(absolute_path(corpus) / "sbom-index.db", "'", "''")}}' {{ if query == "" { "-queries" } else { "-query " + query } }} {{args}}

[unix]
sbom-query query="" args="" corpus=(bootstrap_dir / "dist"):
    go -C tools/sbom-index run . -db {{quote(absolute_path(corpus) / "sbom-index.db")}} {{ if query == "" { "-queries" } else { "-query " + query } }} {{args}}

# Run all checks
check: fmt-check vet test test-tools

# Platform extension helper
ext := if os() == "windows" { ".exe" } else { "" }

# Common msis flags for bootstrap builds
msis_flags := "--build --templatefolder=../templates /SET:PRODUCT_VERSION=" + version

# This file is the only place a real version number lives - the binary gets it via -ldflags,
# the MSI via /SET:PRODUCT_VERSION, and cmd/msis/main.go falls back to "dev" rather than to a
# number that can go stale. Writing the release notes and tagging stay manual on purpose.
#
# NEW is exported ($-prefixed) rather than interpolated as {{NEW}}: just splices {{...}} into
# the recipe as source text, so `just set-version '$(3).0.5'` would be evaluated by the shell -
# validated as 3.0.5, then stored literally as $(3).0.5 and fed to -ldflags. Read as an
# environment variable the argument is data, and the same value is validated and written.
#
# just --list shows the LAST comment line, so the summary stays at the bottom of this block:
# Set the version: rewrites `version :=` above and adds a README.md changelog stub
[windows]
set-version $NEW:
    @if ($env:NEW -notmatch '^\d+\.\d+\.\d+$') { Write-Error "version must be X.Y.Z, got '$env:NEW'"; exit 1 }
    @if ((Get-Content README.md -Raw) -match ('(?m)^\*\*' + [regex]::Escape($env:NEW) + '\*\* ')) { Write-Error "README.md already has a changelog entry for $env:NEW"; exit 1 }
    @$utf8 = New-Object Text.UTF8Encoding $false; $j = [IO.File]::ReadAllText("justfile", $utf8); $n = [regex]::new('(?m)^version := "[^"]*"$').Replace($j, 'version := "' + $env:NEW + '"'); if ($n -eq $j) { Write-Error "no 'version := \"...\"' line found in justfile"; exit 1 }; [IO.File]::WriteAllText("justfile", $n, $utf8)
    @$utf8 = New-Object Text.UTF8Encoding $false; $r = [IO.File]::ReadAllText("README.md", $utf8); $v = $env:NEW; $stub = "**$v** $([char]0x2014) $(Get-Date -Format yyyy-MM-dd) (tag [``v$v``](../../releases/tag/v$v))`n- TODO: release notes`n`n"; $re = [regex]::new('(?m)^(\*\*\d+\.\d+\.\d+\*\* )'); if (-not $re.IsMatch($r)) { Write-Error "no changelog entry found in README.md to insert before"; exit 1 }; [IO.File]::WriteAllText("README.md", $re.Replace($r, ($stub.Replace('$', '$$') + '$1'), 1), $utf8)
    @Write-Host "Version set to $env:NEW. Next: write the README.md notes, just check, just release-all, then git tag v$env:NEW"

[unix]
set-version $NEW:
    @printf '%s' "$NEW" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' || { echo "version must be X.Y.Z, got '$NEW'" >&2; exit 1; }
    @grep -q "^\*\*$NEW\*\* " README.md && { echo "README.md already has a changelog entry for $NEW" >&2; exit 1; } || true
    @grep -q '^version := "' justfile || { echo "no 'version := \"...\"' line found in justfile" >&2; exit 1; }
    @sed "s/^version := \".*\"$/version := \"$NEW\"/" justfile > justfile.tmp && mv justfile.tmp justfile
    @awk -v stub="**$NEW** — $(date +%Y-%m-%d) (tag [\`v$NEW\`](../../releases/tag/v$NEW))\n- TODO: release notes\n" '/^\*\*[0-9]+\.[0-9]+\.[0-9]+\*\* / && !done { print stub; done = 1 } { print }' README.md > README.tmp && mv README.tmp README.md
    @echo "Version set to $NEW. Next: write the README.md notes, just check, just release-all, then git tag v$NEW"

# Go stamps every binary with vcs.revision and vcs.modified, and `go version -m` reads them back
# out of the shipped .exe. Building with uncommitted changes stamps vcs.modified=true and a
# "+dirty" module version, so the artifacts on the release page name no identifiable commit -
# which is exactly what happened to 3.0.5. Commit (or stash) first, then build.
#
# -uall is load-bearing: plain `git status --porcelain` honours status.showUntrackedFiles=no, and
# a developer with that set would get a clean bill of health while untracked sources sit in the
# tree. The exit status is checked too, so a git that fails (not a repo, broken index) cannot be
# read as "nothing changed".
#
# just --list shows the LAST comment line, so the summary stays at the bottom of this block:
# Fail unless the working tree is clean (release builds must be identifiable)
[unix]
require-clean-tree:
    @status=$(git status --porcelain -uall) || { echo "git status failed; cannot tell whether the tree is clean" >&2; exit 1; }; test -z "$status" || { echo "working tree has uncommitted changes; commit or stash before a release build" >&2; echo "$status" >&2; exit 1; }

[windows]
require-clean-tree:
    @$dirty = git status --porcelain -uall; if ($LASTEXITCODE -ne 0) { Write-Host "git status failed; cannot tell whether the tree is clean"; exit 1 }; if ($dirty) { Write-Host "working tree has uncommitted changes; commit or stash before a release build"; Write-Host $dirty; exit 1 }

# The SBOM is bound to one release run by a packaging manifest, written in two steps by the same
# Go program so neither shell dialect grows its own hashing logic:
#
#   sbom-capture  after the binaries are built, BEFORE packaging - hashes them and records the
#                 WiX version observed at that moment
#   sbom-seal     after packaging succeeds - re-checks those hashes and adds the artifacts'
#
# release-all runs both (capture as a dependency, seal afterwards), so `just sbom` on its own can
# only describe a run that recorded one, and fails if any recorded file has changed since.
# Timestamps are deliberately not consulted: refreshing an mtime must not make a substituted file
# acceptable.
#
# just --list shows the LAST comment line, so the summary stays at the bottom of this block:
# Record the binaries and build toolchain before packaging (release-all runs this)
sbom-capture:
    go run ./tools/sbom -capture -version {{version}} -dist {{bootstrap_dir}}/dist -bin {{bootstrap_dir}}

# Record the packaged artifacts once the build has succeeded (release-all runs this)
sbom-seal:
    go run ./tools/sbom -seal -version {{version}} -dist {{bootstrap_dir}}/dist -bin {{bootstrap_dir}}

# Write the release SBOM (CycloneDX) next to the artifacts in bootstrap/dist
sbom:
    go run ./tools/sbom -version {{version}} -dist {{bootstrap_dir}}/dist -bin {{bootstrap_dir}}

# The prerequisite pins (internal/prereqcache, decisions D5) are version-specific URLs plus
# SHA-256; a newer redistributable reaches bundles only when a release re-pins (#49). Both
# recipes need the network and are NOT part of `just check`. repin-check GATES `release` and
# `release-all` (product owner's decision, 2026-09-23): a release stops when a pin has moved
# (exit 1) or could not be checked (exit 2), so a release build needs the network and cannot
# ship a stale pin by oversight. Its exit status is 0 when every pin is current.
#
# The tool is BUILT and then run, not `go run`: `go run` reports any non-zero child status as
# 1, and a PowerShell recipe reports 1 too unless it ends with `exit $LASTEXITCODE` - either
# would collapse the 1/2 distinction a scheduled caller relies on.
# Resolve every pin's alias and report whether Microsoft has moved on (no downloads)
[windows]
repin-check:
    go build -o {{bootstrap_dir}}\repin.exe ./tools/repin; if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }; & .\{{bootstrap_dir}}\repin.exe -check; exit $LASTEXITCODE

[unix]
repin-check:
    go build -o {{bootstrap_dir}}/repin ./tools/repin && ./{{bootstrap_dir}}/repin -check

# Download, hash and signature-check what each moved alias serves, and print the replacement table entries
[windows]
repin:
    go build -o {{bootstrap_dir}}\repin.exe ./tools/repin; if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }; & .\{{bootstrap_dir}}\repin.exe; exit $LASTEXITCODE

[unix]
repin:
    go build -o {{bootstrap_dir}}/repin ./tools/repin && ./{{bootstrap_dir}}/repin

# Build release MSI package (x64 only)
[unix]
release: require-clean-tree repin-check clean-bootstrap build-hooks build-windows-x64
    @echo "Preparing x64 release build..."
    cp {{bootstrap_dir}}/{{binary}}-x64.exe {{bootstrap_dir}}/msis.exe
    @echo "Building x64 MSI package..."
    cd {{bootstrap_dir}} && ./msis.exe {{msis_flags}} --template=../templates/minimal/template.wxs setup.msis
    @echo "Release build complete: {{bootstrap_dir}}/dist/msis-{{version}}-x64.msi"

[windows]
release: require-clean-tree repin-check clean-bootstrap build-hooks build-windows-x64
    @echo "Preparing x64 release build..."
    Copy-Item {{bootstrap_dir}}\{{binary}}-x64.exe {{bootstrap_dir}}\msis.exe
    @echo "Building x64 MSI package..."
    Push-Location {{bootstrap_dir}}; try { .\msis.exe {{msis_flags}} --template=..\templates\minimal\template.wxs setup.msis } finally { Pop-Location }
    @echo "Release build complete: {{bootstrap_dir}}\dist\msis-{{version}}-x64.msi"

# Build release for x86, x64, and arm64, then create bundle
[unix]
release-all: require-clean-tree repin-check clean-bootstrap build-hooks build-all sbom-capture && sbom-seal sbom
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
release-all: require-clean-tree repin-check clean-bootstrap build-hooks build-all sbom-capture && sbom-seal sbom
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
