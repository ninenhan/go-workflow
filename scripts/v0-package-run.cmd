@echo off
setlocal

set "PACKAGE_DIRECTORY=%~dp0"
if not defined WORKFLOW_WEB_DIR set "WORKFLOW_WEB_DIR=%PACKAGE_DIRECTORY%web"
if not defined WORKFLOW_DATA_DIR (
  if not defined LOCALAPPDATA (
    echo LOCALAPPDATA is unavailable; set WORKFLOW_DATA_DIR explicitly. 1>&2
    exit /b 1
  )
  set "WORKFLOW_DATA_DIR=%LOCALAPPDATA%\go-workflow"
)

"%PACKAGE_DIRECTORY%bin\workflow-server.exe" %*
exit /b %ERRORLEVEL%
