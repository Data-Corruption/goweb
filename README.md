# goweb

Template project for go cli tools that includes self http daemon management, changelog driven CI/CD, updating, etc.

If you have questions that this readme doesn't answer, try browsing the source, it's small, simple, and readable (~1.3k loc total).

---

After cloning:
- edit your repo settings, allowing actions to write.
- edit template vars. They are near the top of the following files:
    - `scripts/install.sh`
    - `scripts/install.ps1`
    - `scripts/build.sh`
    - `go/main/main.go`
    - `go/commands/update/update.go`

Template vars will be clearly wrapped like:
```go
// Template variables ---------------------------

const Name = "goweb"

// ----------------------------------------------
```

<br>

You then can add github.com/urfave/cli/v3 commands to the app in `go/main/main.go`, modify the daemon server in `go/server/server.go`, etc. Use -h to see commands.

After making edits add an entry to your changlog like:

```markdown
# Changelog

## [v0.0.2] - 2025-07-10

Yoooo whatup, just adding a bunch of new shizle, peep it.
Everything between the version header lines count as the body.
Markdown formatting will persist to the draft release body.

## [v0.0.1] - 2025-07-09

First version

```

↑ When you include a new version entry like that in a push to main on a Github repo the workflow will build a release, zip it, and upload it to a draft release. You then can review it, the body / name will match the body / version from the latest new version added to the changelog. When you click publish it will tag that commit with the version. Now when people run your one liner install cmds, it will install that latest release!

Once a day, when run, installed bins will check if a new version is available and notify the user. The user can update with a single command using the installed cli app itself. They can also toggle update notifications with a sub-command. When you make a build locally it's version is set to vX.X.X and the update logic is all skipped.

For daemon management you have (start/status/restart/stop). There is also run, which runs the daemon in the foreground (still logs to disc). So if you wanna have a separate thing manage this, you would start it with `goweb daemon run`, replacing goweb with your app name, or the path to wherever you installed the bin. By default the daemon is an http server, should be on 8080 as per config default.

## One-liner installs

### Linux

Default (installs latest version to /usr/local/bin) (recommended):
```sh
curl -sSfL https://raw.githubusercontent.com/Data-Corruption/goweb/main/scripts/install.sh | bash
```
With version and install dir override:
```sh
curl -sSfL https://raw.githubusercontent.com/Data-Corruption/goweb/main/scripts/install.sh | bash -s -- [VERSION] [INSTALL_DIR]
```

### Windows With WSL

Open a powershell terminal as administrator:
```powershell
Set-ExecutionPolicy Bypass -Scope Process -Force; iex "& { $(irm https://raw.githubusercontent.com/Data-Corruption/goweb/main/scripts/install.ps1) }"
```

↑ will run linux install in wsl with sudo. It also creates a little script that bridges the gap between powershell and wsl, then adds it to PATH. After running this, you can run the tool from powershell and it will just run in wsl passing args if any, pretty neato if i sayo myselfo. Sudo is needed otherwise the bridge script won't work.

<3 xoxo :3 <- that last bit is a cat, his name is sebastian and he is ultra fancy. Like, i'm not kidding, more than you initially imagined reading that. Pinky up, drinks tea... you have no idea. Crazy.

## Notes / Yappin

### Why lmdb for config? Lemme tall ya:
I'm already using it for another part of a project that will use this template.  
It's atomic and multiple app instances can use it at the same time.  
Single dependency, need i say more.

It has a thin wrapper around it. You can add DBIs to it in `go/commands/database/database.go`. By doing so you can also use it for other things. The `database.go` also includes a func to get the db from a context. It will be in the urfave app context so you can get it in any commands you add and do whatever with it.

### How tf does the update command work? also how does the win install work?

Now you're asking the good questions. Welp... they both just run the linux one liner install in a terminal lmao. For update, the app/process passes in latest as [VERSION] and it's current install dir as [INSTALL_DIR]. For the windows install it just starts wsl and runs the linux install command in it as root. Also makes a little convenience bridge but that's about it. Simple and might sound hacky but it works super damn well.