# Transparent WSL app installation script for Windows
#
# Default (installs latest version to /usr/local/bin in WSL):
#   Set-ExecutionPolicy Bypass -Scope Process -Force; iex "& { $(irm https://raw.githubusercontent.com/OWNER/REPO/main/scripts/install.ps1) }"


# Template variables ----------------------------------------------------------

$OWNER="Data-Corruption"
$REPO="goweb"
$APP_NAME="goweb"

# Startup ---------------------------------------------------------------------

$LINUX_INSTALL_CMD = "curl -sSfL https://raw.githubusercontent.com/$OWNER/$REPO/main/scripts/install.sh | sudo bash"
$dataDir = [Environment]::GetFolderPath([Environment+SpecialFolder]::ApplicationData)
$appDir = Join-Path -Path $dataDir -ChildPath $APP_NAME
$bridgeScript = Join-Path -Path $appDir -ChildPath "$APP_NAME.ps1"

# ensure admin perms
if (-NOT ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole] "Administrator")) {
  Write-Warning "You do not have Administrator rights to run this script! Please re-run as Administrator."
  exit 1
}

# ensure app dir exists
if (-not (Test-Path $appDir)) {
  New-Item -ItemType Directory -Path $appDir | Out-Null
}

# ensure app dir is in system PATH
$currentPATH = [Environment]::GetEnvironmentVariable("PATH", "Machine")
$pathItems = $currentPATH -split ';' | ForEach-Object { $_.TrimEnd('\') }
if (-not ($pathItems -contains $appDir.TrimEnd('\'))) {
  $newPath = "$currentPATH;$appDir"
  [Environment]::SetEnvironmentVariable("PATH", $newPath, "Machine")
}

# write the bridge file to the app dir and unblock it
$bridgeScriptContent = @"
# Auto-generated script that bridges Windows to WSL for the app '`$APP_NAME'
param(`$args)
`$cwd = (Get-Location).Path
`$wslCwd = wsl -- bash -c "wslpath '`$cwd'"
wsl -- bash -c "cd `$wslCwd; $APP_NAME `$args"
"@

Set-Content -Path $bridgeScript -Value $bridgeScriptContent
Unblock-File -Path $bridgeScript

# execute linux install command in WSL
wsl -- bash -c "$LINUX_INSTALL_CMD"