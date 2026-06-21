@echo off
REM ============================================================================
REM ensure-wix.cmd - install the correct WiX version + extensions for msis.
REM
REM Thin wrapper around ensure-wix.ps1 so it can be double-clicked or run from
REM cmd.exe without worrying about PowerShell execution policy.
REM
REM Usage:
REM   ensure-wix.cmd                       (default version 7.0.0, no cleanup)
REM   ensure-wix.cmd -Clean                (also remove mismatched extensions)
REM   ensure-wix.cmd -Version 6.0.2 -Clean (stay on WiX 6)
REM ============================================================================
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0ensure-wix.ps1" %*
exit /b %ERRORLEVEL%
