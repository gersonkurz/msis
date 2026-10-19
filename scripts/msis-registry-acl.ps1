<#
.SYNOPSIS
    Finds, and with -Fix repairs, Windows registry keys whose permissions an msis-built
    installer replaced (gersonkurz/msis#97).

.DESCRIPTION
    Installers built with msis 3.0.0 to 3.0.8 that import a .reg file set permissions on every
    key on the path to the imported keys, not just on the imported keys. For a .reg file under
    HKLM\SOFTWARE\Vendor that includes HKLM\SOFTWARE itself (HKLM\SOFTWARE\WOW6432Node for a
    32-bit installer on 64-bit Windows); for an Event Log source it includes HKLM\SYSTEM and
    the keys below it. The new permissions give standard users write access, they are
    inherited by every subkey that inherits from that key, and uninstalling does not undo them.

    Without -Fix the script only reads. It checks a fixed list of Windows-owned keys and
    reports each one whose OWN (not inherited) permissions let Users, Authenticated Users or
    Everyone write to it. Windows never grants that on these keys.

    With -Fix (run as administrator) it gives each reported key back the permissions Windows
    ships it with. Windows then recalculates the inherited permissions of every key below it,
    which repairs those keys too. Before changing anything it saves each key's current
    permissions to a backup file in the current directory.

    Keys the installer itself created (HKLM\SOFTWARE\Pergamon, ...) are not checked and not
    changed: their permissions are the ones the installer asked for. Neither are Windows keys
    deeper than the list below (an import under HKLM\SOFTWARE\Microsoft\... replaced theirs
    too): msis-check-msi.ps1 shows whether an MSI did that, and such a key needs repairing by
    hand, because its original permissions are not known on the affected machine.

.PARAMETER Fix
    Repair the reported keys. Requires an elevated PowerShell.

.OUTPUTS
    Exit code 0: nothing found (or everything repaired). 1: affected keys found (list mode).
    2: an error, or -Fix could not repair a key.

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File .\msis-registry-acl.ps1
    powershell -ExecutionPolicy Bypass -File .\msis-registry-acl.ps1 -Fix
#>
[CmdletBinding()]
param([switch]$Fix)

$ErrorActionPreference = 'Stop'

# The permissions (DACL, as SDDL) Windows ships each key with, read from clean installations.
# A key without an entry is still checked, but -Fix refuses to change it.
$Reference = [ordered]@{
    'SOFTWARE'                                              = $null
    'SOFTWARE\WOW6432Node'                                  = $null
    'SOFTWARE\Classes'                                      = $null
    'SYSTEM'                                                = $null
    'SYSTEM\CurrentControlSet'                              = $null
    'SYSTEM\CurrentControlSet\Services'                     = $null
    'SYSTEM\CurrentControlSet\Services\EventLog'            = $null
    'SYSTEM\CurrentControlSet\Services\EventLog\Application' = $null
}

# -Path, not -LiteralPath: Windows PowerShell 5.1's Get-Acl cannot find a registry key by
# -LiteralPath. The key names above contain no wildcard characters.
$WriteRights = 0x500D0006 # GENERIC_ALL|GENERIC_WRITE|WRITE_OWNER|WRITE_DAC|DELETE|KEY_CREATE_SUB_KEY|KEY_SET_VALUE
$Broad = @{ 'S-1-5-32-545' = 'Users'; 'S-1-5-11' = 'Authenticated Users'; 'S-1-1-0' = 'Everyone' }

# Grants on the key's own DACL that let a standard user write. Inherited entries are left out:
# they are repaired by repairing the key they come from.
function Get-BroadWriteGrant([string]$Sddl) {
    $sd = New-Object System.Security.AccessControl.RawSecurityDescriptor($Sddl)
    foreach ($ace in $sd.DiscretionaryAcl) {
        if ($ace.AceType -ne 'AccessAllowed' -or $ace.IsInherited) { continue }
        $who = $Broad[$ace.SecurityIdentifier.Value]
        if ($who -and ($ace.AccessMask -band $WriteRights)) { '{0} (0x{1:X8})' -f $who, $ace.AccessMask }
    }
}

# A 32-bit PowerShell on 64-bit Windows sees HKLM\SOFTWARE\WOW6432Node where it asks for
# HKLM\SOFTWARE (registry redirection), so it would check, and repair, the wrong key.
if ([Environment]::Is64BitOperatingSystem -and -not [Environment]::Is64BitProcess) {
    $host.UI.WriteErrorLine('Run this script in 64-bit PowerShell: a 32-bit one sees a redirected registry on 64-bit Windows.')
    exit 2
}

if ($Fix) {
    $principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        $host.UI.WriteErrorLine('-Fix needs an elevated PowerShell (Run as administrator).')
        exit 2
    }
}

# This script knows the keys above. A package may also have replaced the permissions of a
# deeper Windows key (an import below HKLM\SOFTWARE\Microsoft\...); msis-check-msi.ps1 shows
# which keys a given MSI changed. Say so with every result, so no result reads as "all clear".
$Scope = 'Checked only the keys above. Run msis-check-msi.ps1 on the MSIs installed on this machine to see whether they changed other keys.'

$affected = @()
foreach ($key in $Reference.Keys) {
    $path = "HKLM:\$key"
    if (-not (Test-Path -Path $path)) { continue }
    $sddl = (Get-Acl -Path $path).Sddl
    $grants = @(Get-BroadWriteGrant $sddl)
    if ($grants.Count -eq 0) {
        Write-Output "OK        HKLM\$key"
        continue
    }
    Write-Output "AFFECTED  HKLM\$key  -  writable by: $($grants -join ', ')"
    $affected += [pscustomobject]@{ Key = $key; Path = $path; Sddl = $sddl }
}

if ($affected.Count -eq 0) {
    Write-Output 'No affected keys found.'
    Write-Output $Scope
    exit 0
}
if (-not $Fix) {
    Write-Output "$($affected.Count) affected key(s). Keys below them that inherit their permissions are affected too."
    Write-Output 'Run again with -Fix, as administrator, to repair them.'
    Write-Output $Scope
    exit 1
}

$backup = Join-Path (Get-Location) ('msis-registry-acl-backup-{0:yyyyMMdd-HHmmss}.txt' -f (Get-Date))
$affected | ForEach-Object { "HKLM\$($_.Key)`t$($_.Sddl)" } | Set-Content -LiteralPath $backup -Encoding UTF8
Write-Output "Saved the current permissions to $backup"

$failed = 0
foreach ($a in $affected) {
    $ref = $Reference[$a.Key]
    if (-not $ref) {
        Write-Warning "HKLM\$($a.Key): no reference permissions for this key; not changed."
        $failed++
        continue
    }
    try {
        $acl = Get-Acl -Path $a.Path
        $acl.SetSecurityDescriptorSddlForm($ref, 'Access') # the DACL only; owner and group stay
        Set-Acl -Path $a.Path -AclObject $acl         # Windows re-propagates inheritance below
        if (@(Get-BroadWriteGrant (Get-Acl -Path $a.Path).Sddl).Count -gt 0) { throw 'still writable after the change' }
        Write-Output "REPAIRED  HKLM\$($a.Key)"
    } catch {
        Write-Warning "HKLM\$($a.Key): $($_.Exception.Message)"
        $failed++
    }
}
Write-Output $Scope
if ($failed -gt 0) { exit 2 }
exit 0
