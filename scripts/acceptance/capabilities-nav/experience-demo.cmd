@echo off
rem Demonstration-mode experience for the capabilities navigation: the built Windows binary with the demonstration switch,
rem on 127.0.0.1:18489 only (no sign-in, no gateway). Build the binary first (see experience-env.cmd).
rem It runs in the foreground; a launcher such as experience.py starts it hidden and stops it by killing the process tree.
set "ROOT=%~dp0..\..\.."
cd /d "%ROOT%"
node scripts\acceptance\capabilities-nav\demo-serve.mjs "%ROOT%\tmp\omc-nav.exe" "%ROOT%\tmp\experience-demo-data" 18489
