# goweb

## Linux

Default (installs latest version to /usr/local/bin):
```sh
curl -sSfL https://raw.githubusercontent.com/Data-Corruption/goweb/main/scripts/install.sh | bash
```
With version and install dir override:
```sh
curl -sSfL https://raw.githubusercontent.com/Data-Corruption/goweb/main/scripts/install.sh | bash -s -- [VERSION] [INSTALL_DIR]
```

## Windows With WSL

Open a powershell terminal as administrator:
```powershell
Set-ExecutionPolicy Bypass -Scope Process -Force; iex "& { $(irm https://raw.githubusercontent.com/Data-Corruption/goweb/main/scripts/install.ps1) }"
```