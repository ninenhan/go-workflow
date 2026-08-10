@echo off
setlocal EnableExtensions EnableDelayedExpansion

set "PACKAGE_DIRECTORY=%~dp0"
if not defined WORKFLOW_WEB_DIR set "WORKFLOW_WEB_DIR=%PACKAGE_DIRECTORY%web"
set "CONFIGURATION_SUPPLIED="
if defined WORKFLOW_CONFIG set "CONFIGURATION_SUPPLIED=1"
for %%A in (%*) do (
  set "ARGUMENT=%%~A"
  if /I "!ARGUMENT!"=="--config" set "CONFIGURATION_SUPPLIED=1"
  if /I "!ARGUMENT:~0,9!"=="--config=" set "CONFIGURATION_SUPPLIED=1"
)
if not defined WORKFLOW_DATA_DIR if not defined CONFIGURATION_SUPPLIED (
  if not defined LOCALAPPDATA (
    echo LOCALAPPDATA is unavailable; set WORKFLOW_DATA_DIR explicitly. 1>&2
    exit /b 1
  )
  set "WORKFLOW_DATA_DIR=%LOCALAPPDATA%\go-workflow"
)

"%PACKAGE_DIRECTORY%bin\workflow-server.exe" --mode=server %*
exit /b %ERRORLEVEL%
