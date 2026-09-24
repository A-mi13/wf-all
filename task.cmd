@echo off
rem Обёртка для PowerShell и cmd: .\task dev — то же, что ./task dev в bash.
rem Задачи Taskfile вызывают bash; без Git Bash первым в PATH Windows находит
rem bash.exe из WSL (C:\Windows\System32), а WSL на машинах проекта не работает.
setlocal
for /f "delims=" %%G in ('git --exec-path 2^>nul') do set "WF_GIT_EXEC=%%G"
if not defined WF_GIT_EXEC (
  echo task.cmd: git not found - install Git for Windows ^(Git Bash^) 1>&2
  exit /b 1
)
for %%R in ("%WF_GIT_EXEC%\..\..\..") do set "WF_GIT_ROOT=%%~fR"
set "PATH=%WF_GIT_ROOT%\usr\bin;%PATH%"
"%~dp0.tools\bin\task.exe" %*
exit /b %ERRORLEVEL%
