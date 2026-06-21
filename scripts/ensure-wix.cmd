@echo off
REM ============================================================================
REM ensure-wix.cmd - install the correct WiX version + extensions for msis.
REM
REM Thin wrapper around ensure-wix.ps1 so it can be double-clicked or run from
REM cmd.exe without worrying about PowerShell execution policy.
REM
REM Usage:
REM   ensure-wix.cmd                       (pinned version, no cleanup)
REM   ensure-wix.cmd -Clean                (also remove mismatched extensions)
REM   ensure-wix.cmd -Version 7.0.0 -Clean -AcceptEula
REM ============================================================================
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0ensure-wix.ps1" %*
exit /b %ERRORLEVEL%
