@echo off
REM longbox-run.bat — auto-relaunching wrapper for longbox.exe.
REM
REM Usage: drop this file next to longbox.exe on the server, double-click
REM (or "Run as administrator" if you want it to bind privileged ports),
REM and leave the cmd window open. Whenever longbox.exe exits — e.g.
REM after a deploy script POSTs /api/v1/admin/shutdown — this loop
REM relaunches the binary automatically.
REM
REM Hit Ctrl+C in the window to stop the loop.

cd /d "%~dp0"

:loop
echo [run] starting longbox.exe...
longbox.exe
echo [run] longbox.exe exited with code %ERRORLEVEL% — relaunching in 2s
timeout /t 2 /nobreak >nul
goto loop
