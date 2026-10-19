<#
.SYNOPSIS
    Reports whether an MSI changes the permissions of, or deletes, Windows-owned registry keys
    (gersonkurz/msis#97).

.DESCRIPTION
    Reads the MSI's tables; it never installs or runs anything. For each MSI it reports:

    - PERMISSIONS: a MsiLockPermissionsEx or LockPermissions row that sets the permissions of a
      Windows-owned registry key. Windows Installer replaces that key's permissions at install
      time, and uninstalling does not restore them.
    - DELETE: a Registry row that deletes a Windows-owned key, with all its subkeys, when the
      package is uninstalled (ForceDeleteOnUninstall).
    - REVIEW: any other key whose permissions the package sets although it writes no value of
      its own there. These are the keys on the way to the package's keys, or keys the package
      creates empty. Check that each one belongs to the product; a key that is not the
      product's own has had its permissions replaced too. REVIEW lines do not change the exit
      code.

    A key counts as Windows-owned if it is directly under a hive (HKLM\SOFTWARE, HKLM\SYSTEM,
    HKCR\<name>, ...), is HKLM\SOFTWARE\WOW6432Node or HKLM\SOFTWARE\Classes, or lies under
    HKLM\SOFTWARE\Microsoft, HKLM\SOFTWARE\WOW6432Node\Microsoft, HKLM\SOFTWARE\Policies or
    HKLM\SYSTEM - including a key the package creates there for itself, such as an Event Log
    source, whose permissions matter for the same reason.

    msis 3.0.0 to 3.0.7 wrote such rows for most packages that import a .reg file; msis 2 for
    keys like HKLM\SYSTEM\CurrentControlSet\Services. To repair a machine on which such a
    package was installed, use msis-registry-acl.ps1 from the same folder.

.PARAMETER Path
    One or more .msi files.

.OUTPUTS
    Exit code 0: no MSI touches a Windows-owned key. 1: at least one does. 2: an MSI could not
    be read.

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File .\msis-check-msi.ps1 "C:\Setups\Product 1.0.msi"
#>
[CmdletBinding()]
param([Parameter(Mandatory = $true, ValueFromRemainingArguments = $true)][string[]]$Path)

$ErrorActionPreference = 'Stop'

$Roots = @{ '-1' = 'HKLM'; '0' = 'HKCR'; '1' = 'HKCU'; '2' = 'HKLM'; '3' = 'HKU' } # -1 (HKMU) is HKLM in a per-machine package
$WindowsKeys = @('HKLM\SOFTWARE\WOW6432Node', 'HKLM\SOFTWARE\Classes')
$WindowsTrees = @('HKLM\SOFTWARE\Microsoft\', 'HKLM\SOFTWARE\WOW6432Node\Microsoft\', 'HKLM\SOFTWARE\Policies\', 'HKLM\SYSTEM\')

function Test-WindowsKey([string]$Key) {
    $Key = $Key.TrimEnd('\')
    if ($Key.Split('\').Count -eq 2 -or $WindowsKeys -contains $Key) { return $true } # -contains ignores case
    foreach ($tree in $WindowsTrees) {
        if ("$Key\".StartsWith($tree, [StringComparison]::OrdinalIgnoreCase)) { return $true } # the tree's own root key too
    }
    $false
}

function Invoke-Method($Object, [string]$Name, [object[]]$Arguments) {
    $Object.GetType().InvokeMember($Name, 'InvokeMethod', $null, $Object, $Arguments)
}

# Rows of a query, or $null when the table does not exist in this MSI.
function Get-Rows($Database, [string]$Table, [string]$Columns) {
    $exists = Invoke-Method $Database 'OpenView' @("SELECT ``Name`` FROM ``_Tables`` WHERE ``Name``='$Table'")
    Invoke-Method $exists 'Execute' $null | Out-Null
    $found = Invoke-Method $exists 'Fetch' $null
    Invoke-Method $exists 'Close' $null | Out-Null
    if (-not $found) { return $null }

    $names = $Columns.Split(',')
    $quoted = ($names | ForEach-Object { '`' + $_ + '`' }) -join ','
    $view = Invoke-Method $Database 'OpenView' @("SELECT $quoted FROM ``$Table``")
    Invoke-Method $view 'Execute' $null | Out-Null
    $rows = @()
    while ($record = Invoke-Method $view 'Fetch' $null) {
        $row = @{}
        for ($i = 0; $i -lt $names.Count; $i++) {
            $row[$names[$i]] = $record.GetType().InvokeMember('StringData', 'GetProperty', $null, $record, @($i + 1))
        }
        $rows += $row
    }
    Invoke-Method $view 'Close' $null | Out-Null
    $rows # unrolled into the pipeline; callers collect it with @()
}

$installer = New-Object -ComObject WindowsInstaller.Installer
$anyFound = $false
$anyError = $false

foreach ($msi in $Path) {
    Write-Output "== $msi"
    try {
        $full = (Resolve-Path -LiteralPath $msi).ProviderPath
        $db = Invoke-Method $installer 'OpenDatabase' @($full, 0) # 0: read-only

        $registry = @{}
        $hasValues = @{} # keys the package writes a value into
        $findings = @()
        $review = @()
        foreach ($r in @(Get-Rows $db 'Registry' 'Registry,Root,Key,Name,Value')) {
            if (-not $r) { continue }
            $key = "$($Roots[$r.Root])\$($r.Key)"
            $registry[$r.Registry] = $key
            if ($r.Value -or ($r.Name -and $r.Name -notin '+', '-', '*')) { $hasValues[$key] = $true }
            # Name '-' or '*' deletes the key on uninstall only when Value is NULL (read back as
            # ''); with a value, it is an ordinary registry value of that name.
            if (($r.Name -eq '-' -or $r.Name -eq '*') -and -not $r.Value -and (Test-WindowsKey $key)) {
                $findings += "DELETE       $key  (with all subkeys, on uninstall)"
            }
        }
        foreach ($t in @(@{ Table = 'MsiLockPermissionsEx'; Columns = 'LockObject,Table,SDDLText' },
                         @{ Table = 'LockPermissions'; Columns = 'LockObject,Table,Domain,User,Permission' })) {
            foreach ($r in @(Get-Rows $db $t.Table $t.Columns)) {
                if (-not $r -or $r.Table -ne 'Registry') { continue }
                $key = $registry[$r.LockObject]
                if (-not $key) { continue }
                $what = if ($r.SDDLText) { $r.SDDLText } else { "$($r.Domain)\$($r.User) 0x$('{0:X}' -f [int64]$r.Permission)" }
                if (Test-WindowsKey $key) {
                    $findings += "PERMISSIONS  $key  $what"
                } elseif (-not $hasValues[$key]) {
                    $review += "REVIEW       $key  $what"
                }
            }
        }

        if ($findings.Count -eq 0) {
            Write-Output 'OK: changes no Windows-owned registry key.'
        } else {
            $findings | Sort-Object -Unique | ForEach-Object { Write-Output "  $_" }
            $anyFound = $true
        }
        if ($review.Count -gt 0) {
            Write-Output '  Also sets the permissions of these keys, which hold no value of the package - check that each is the product''s own:'
            $review | Sort-Object -Unique | ForEach-Object { Write-Output "  $_" }
        }
    } catch {
        Write-Warning "$msi : $($_.Exception.Message)"
        $anyError = $true
    }
}

if ($anyError) { exit 2 }
if ($anyFound) { exit 1 }
exit 0
