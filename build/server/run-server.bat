@echo off
setlocal
rem Direct-start launcher for the Windows-hosted server: double-click, or run
rem from a terminal, to start beresta-server-windows-amd64.exe with its data
rem directory alongside this script. This is the primary deployment path
rem (see docs/server-operations.md); the scheduled task
rem (install-scheduled-task.ps1) and the systemd unit are optional
rem always-on alternatives, not a replacement for this.
set SCRIPT_DIR=%~dp0
set BERESTA_EXE=%SCRIPT_DIR%beresta-server-windows-amd64.exe
set BERESTA_DATA=%SCRIPT_DIR%data

if not exist "%BERESTA_EXE%" (
    echo beresta-server-windows-amd64.exe was not found next to this script.
    echo Build it first with: build.cmd server-cross-build
    exit /b 1
)

"%BERESTA_EXE%" --data "%BERESTA_DATA%" %*
