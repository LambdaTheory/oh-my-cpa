@echo off
rem Experience environment for the capabilities navigation: the in-process fake CPA (two Codex cards) plus the built
rem Windows binary, on 127.0.0.1:18491 only. Build the binary first (pnpm build, then the container cross-compile into tmp\omc-nav.exe).
rem It runs in the foreground; a launcher such as experience.py starts it hidden and stops it by killing the process tree.
set "ROOT=%~dp0..\..\.."
set "OMCPA_BROWSER_BINARY=%ROOT%\tmp\omc-nav.exe"
set "OMC_LENDING_APP_PORT=18491"
set "OMC_LENDING_CPA_PORT=18492"
set "OMC_LENDING_CONTROL_PORT=18493"
set "OMC_LENDING_DATA_DIR=%ROOT%\tmp\experience-data"
cd /d "%ROOT%"
node scripts\acceptance\lending-experience-env.mjs
