<#
.SYNOPSIS
    Ensures the correct WiX Toolset version and the extensions msis needs are installed.

.DESCRIPTION
    msis builds MSI/bundle packages by invoking the WiX dotnet global tool
    (~/.dotnet/tools/wix.exe -- the one msis resolves in internal/wix/builder.go).

    WiX extensions live in a single global per-user store that is SHARED across
    WiX major versions and tagged by version. Having e.g. both 5.x and 6.x of an
    extension registered makes every `wix` command emit WIX6101 "compatible with
    WiX vN?" warnings and mark the non-matching copies "(damaged)". This is the
    usual source of "I can't get the extensions working" confusion.

    This script:
      1. Verifies the .NET SDK is available.
      2. Installs / updates the WiX dotnet global tool to the pinned version.
      3. Registers exactly the 4 extensions msis needs, pinned to that version.
      4. (-Clean) Removes mismatched-version copies of those 4 extensions to
         silence the WIX6101 noise.
      5. Verifies and reports the result.

.PARAMETER Version
    WiX version to pin (tool + extensions). Defaults to the supported version.

.PARAMETER Clean
    Remove any other-version copies of the managed extensions from the global store.
    NOTE: those copies are used by other WiX installs (e.g. a standalone
    "WiX Toolset v5.0"); only use -Clean if you don't rely on those.

.NOTES
    msis works with WiX 6 or 7 -- it detects the installed major version at build
    time. This script installs ONE version (default 7.0.0). To stay on v6, pass
    -Version 6.0.2. The WiX 7 EULA does not need pre-accepting here: msis passes
    `-acceptEula wix7` automatically on every build.

.EXAMPLE
    .\ensure-wix.ps1
    .\ensure-wix.ps1 -Version 6.0.2 -Clean
#>
[CmdletBinding()]
param(
    [string]$Version = "7.0.0",
    [switch]$Clean
)

$ErrorActionPreference = "Stop"

# Extensions msis passes to `wix build` (see internal/wix/builder.go):
#   MSI:    WixToolset.UI.wixext, WixToolset.Util.wixext
#   Bundle: WixToolset.BootstrapperApplications.wixext, WixToolset.Util.wixext, WixToolset.Netfx.wixext
$Extensions = @(
    'WixToolset.UI.wixext',
    'WixToolset.Util.wixext',
    'WixToolset.BootstrapperApplications.wixext',
    'WixToolset.Netfx.wixext'
)

function Step($msg) { Write-Host "==> $msg" -ForegroundColor Cyan }
function Ok($msg)   { Write-Host "  OK  $msg" -ForegroundColor Green }
function Warn($msg) { Write-Host "  !!  $msg" -ForegroundColor Yellow }
function Fail($msg) { Write-Host "FAIL  $msg" -ForegroundColor Red; exit 1 }

# --- 1. .NET SDK ----------------------------------------------------------
Step ".NET SDK"
$dotnet = Get-Command dotnet -ErrorAction SilentlyContinue
if (-not $dotnet) {
    Fail ".NET SDK not found. Install it from https://dotnet.microsoft.com/download then re-run."
}
Ok "dotnet $((& dotnet --version))"

# --- 2. WiX dotnet global tool -------------------------------------------
Step "WiX tool (dotnet global tool, version $Version)"

function Get-WixToolVersion {
    # Parse `dotnet tool list --global` for the wix package id.
    $lines = & dotnet tool list --global 2>$null
    foreach ($line in $lines) {
        $cols = ($line -split '\s+') | Where-Object { $_ -ne '' }
        if ($cols.Count -ge 2 -and $cols[0].ToLower() -eq 'wix') { return $cols[1] }
    }
    return $null
}

$current = Get-WixToolVersion
if (-not $current) {
    Step "Installing wix $Version ..."
    & dotnet tool install --global wix --version $Version
} elseif ($current -ne $Version) {
    Step "Updating wix $current -> $Version ..."
    # `update` upserts and handles up/down-grade on current SDKs; fall back hard.
    & dotnet tool update --global wix --version $Version
    if ((Get-WixToolVersion) -ne $Version) {
        Warn "update did not land $Version; reinstalling"
        & dotnet tool uninstall --global wix
        & dotnet tool install --global wix --version $Version
    }
} else {
    Ok "wix $current already installed"
}

# Resolve the exact binary msis uses (dotnet tool path), with a PATH fallback.
$wixExe = Join-Path $HOME ".dotnet\tools\wix.exe"
if (-not (Test-Path $wixExe)) {
    $cmd = Get-Command wix -ErrorAction SilentlyContinue
    if ($cmd) { $wixExe = $cmd.Source } else { Fail "wix.exe not found after install." }
}
Ok "using $wixExe"

# --- 3. Extensions (pinned to $Version) ----------------------------------
Step "Extensions (pinned to $Version)"
foreach ($ext in $Extensions) {
    & $wixExe extension add -g "$ext/$Version" *> $null
    if ($LASTEXITCODE -eq 0) { Ok "$ext/$Version" }
    else { Fail "could not add $ext/$Version (check network / nuget access)" }
}

# --- 4. Optional cleanup of mismatched versions --------------------------
if ($Clean) {
    Step "Removing mismatched-version copies of managed extensions"
    $listed = & $wixExe extension list -g 2>$null
    foreach ($line in $listed) {
        # lines look like: "WixToolset.UI.wixext 6.0.2" or "... 5.0.2 (damaged)"
        $m = [regex]::Match($line, '^\s*(?<name>WixToolset\.\S+)\s+(?<ver>\d[\w\.\-\+]*)')
        if (-not $m.Success) { continue }
        $name = $m.Groups['name'].Value
        $ver  = $m.Groups['ver'].Value
        if (($Extensions -contains $name) -and ($ver -ne $Version)) {
            & $wixExe extension remove -g "$name/$ver" *> $null
            if ($LASTEXITCODE -eq 0) { Ok "removed $name/$ver" } else { Warn "could not remove $name/$ver" }
        }
    }
}

# --- 5. Verify ------------------------------------------------------------
Step "Result"
$toolVer = & $wixExe --version
Ok "wix tool: $toolVer"
$pathWix = Get-Command wix -ErrorAction SilentlyContinue
if ($pathWix -and ($pathWix.Source -ne $wixExe)) {
    $pathVer = & $pathWix.Source --version 2>$null
    Warn "`wix` on PATH is a DIFFERENT install ($pathVer at $($pathWix.Source))."
    Warn "That's fine -- msis ignores PATH and uses $wixExe. Run 'msis /STATUS' to confirm."
}
Write-Host ""
Write-Host "WiX $Version is ready with the extensions msis needs." -ForegroundColor Green
if (-not $Clean) {
    Write-Host "Tip: re-run with -Clean to silence WIX6101 warnings from other-version extensions." -ForegroundColor DarkGray
}
