# runr

Run several local programs at once — a database, a few services, a frontend — and watch
all their output in one terminal UI.

You register each project once (directory + command), optionally put projects into named
groups, and then start any combination with one command. runr starts dependencies first,
waits until they are ready, interleaves everyone's output, and shuts everything down in
reverse order when you quit.

```
 runr · backend + web                                          combined · following
 ──────────────┬────────────────────────────────────────────────────────────────────
 ▸ ● db        │ db     database system is ready to accept connections
   ● api       │ api    listening on :8080
   ✗ worker  1 │ worker panic: missing QUEUE_URL
   ◐ web       │ web    waiting for api
 ──────────────┴────────────────────────────────────────────────────────────────────
 ↑↓ select  tab view  r restart  s stop  + start  / search  f follow  q quit
```

Works on Linux, macOS and Windows.

## Install

runr is built from source and needs Go 1.27 or newer.

```bash
go install github.com/Skaanning/runr/cmd/runr@latest
```

This puts `runr` in `$(go env GOPATH)/bin`, usually `~/go/bin`; make sure that directory is
on your `PATH`.

Or build from a clone:

```bash
git clone https://github.com/Skaanning/runr.git && cd runr
go install ./cmd/runr        # or: make build → bin/runr
```

`make cross` builds binaries for Linux, macOS and Windows into `dist/`.

## Quick start

Register your projects. `--dir` defaults to the current directory; everything after `--`
is the command.

```bash
runr add db --dir ~/code/infra --ready-log 'ready to accept connections' -- docker compose up postgres
runr add api --dir ~/code/api --depends-on db --env PORT=8080 -- go run ./cmd/server
cd ~/code/web && runr add web --depends-on api -- npm run dev
```

Group them, if you like:

```bash
runr group create backend db api
```

Then start what you need:

```bash
runr up backend        # db first, api once db printed "ready to accept connections"
runr up web            # pulls in api and db automatically
runr                   # or pick from a list in the launcher
```

Press `q` to stop everything. See [examples/config.toml](examples/config.toml) for a
complete config file.

## Commands

| Command | Description |
|---|---|
| `runr` | Open the launcher |
| `runr up <name...>` | Start groups and/or projects (plus their dependencies) and open the running view |
| `runr add <name> [flags] -- <cmd>` | Register a project |
| `runr edit <name> [flags] [-- <cmd>]` | Change a project; only the given flags change |
| `runr rm <name> [--force]` | Remove a project (and from its groups); refuses if others depend on it unless `--force` |
| `runr rename <name> <new-name>` | Rename a project or group; groups and `depends_on` are updated |
| `runr ls` | List projects and groups, with warnings such as missing directories |
| `runr group create <group> <project...>` | Create a group |
| `runr group add <group> <project...>` | Add projects to a group |
| `runr group rm <group> <project...>` | Remove projects from a group |
| `runr group delete <group>` | Delete a group (its projects are kept) |
| `runr group ls` | List groups |
| `runr theme [name]` | List the color themes (the current one marked with `*`), or set one |
| `runr completion <shell>` | Shell completion script (bash, zsh, fish, powershell), completes project and group names |

Flags for `add` and `edit`:

| Flag | Field |
|---|---|
| `--dir <path>` | `dir` (`add`: defaults to the current directory) |
| `--env KEY=VALUE` | `env`, repeatable; `edit` sets/overrides single variables |
| `--unset-env KEY` | `edit` only: remove a variable, repeatable |
| `--env-file <path>` | `env_file` |
| `--depends-on <name>` | `depends_on`, repeatable or comma-separated; `edit` replaces the list, `--depends-on=` clears it |
| `--ready-log <regex>` | `ready_log` |
| `--color <color>` | `color` |
| `--display-name <label>` | `display_name` |

With `edit`, a flag given an empty value (e.g. `--color=`) clears that field.

If `runr up` isn't connected to a terminal (piped, or run from a script), it prints the
interleaved output with a name prefix instead of opening the UI, and exits once all
processes have finished — with exit code 1 if any crashed. Ctrl+C stops everything.

## Configuration

Everything lives in one file:

| OS | Path |
|---|---|
| Linux | `~/.config/runr/config.toml` (or `$XDG_CONFIG_HOME/runr/config.toml`) |
| macOS | `~/Library/Application Support/runr/config.toml` |
| Windows | `%AppData%\runr\config.toml` |

Set `RUNR_CONFIG=/path/to/config.toml` to use a different file. The file is created on the
first change and is readable only by you, since `env` values may hold secrets.

You can edit it by hand, but `runr add`/`edit`, `runr group …` and the launcher's forms all
validate before saving and are usually easier. runr rewrites the whole file when it saves,
so **comments in a hand-edited file are lost** after the next change made through runr.

```toml
[settings]
shutdown_timeout = "5s"
scrollback_lines = 10000
show_all = true
theme = "tokyo-night"

[projects.api]
dir = "~/code/api"
cmd = "go run ./cmd/server"
env = { PORT = "8080" }
env_file = ".env"
depends_on = ["db"]
ready_log = 'listening on :\d+'
color = "cyan"
display_name = "API"

[groups]
backend = ["db", "api"]
```

### Settings

| Key | Default | Description |
|---|---|---|
| `shutdown_timeout` | `"5s"` | How long a stopping process gets before it is killed (Go duration: `500ms`, `10s`, `1m`) |
| `scrollback_lines` | `10000` | Lines of output kept per process |
| `theme` | `"terminal"` | Colors of the TUI: `terminal` (your terminal's own colors), `catppuccin`, `tokyo-night`, `gruvbox` or `nord`. See [Themes](#themes). |
| `theme_background` | `false` | Paint the theme's background and text color behind the whole TUI instead of letting your terminal's show through. See [Themes](#themes). |
| `show_all` | `true` | List the registered projects that aren't running in the running view's sidebar, below the session's (`a` toggles it for a session) |

### Themes

`terminal` uses your terminal's 16 colors, so runr matches whatever scheme the terminal has.
The others bring their own colors: `catppuccin` (mocha on dark terminals, latte on light
ones), `tokyo-night` (night / day), `gruvbox` (dark / light) and `nord` (dark only; on a
light terminal runr uses the `terminal` theme instead). runr asks the terminal whether its
background is light or dark when it starts, and assumes dark if it doesn't answer. Themes
color the text only and your terminal's background shows through, unless you set
`theme_background = true`: then runr sets the terminal's background and text color to the
theme's while it runs (so output that resets its colors falls back to the theme's too) and
restores them when it exits. That needs a terminal that answers the background query and lets
programs set its colors, which most do. Named project colors
(`cyan`, `bright-red`, …) come from the theme; numbers and hex codes are used as they are.
Themes need a 256-color or true-color terminal; with 16 colors runr uses the `terminal`
theme.

To try them, press `t` in the launcher: it shows each theme as you move through the list and
saves the one you pick. `runr theme` lists them and `runr theme <name>` sets one.

### Project fields

| Field | Required | Description |
|---|---|---|
| `dir` | yes | Working directory. `~` is expanded. |
| `cmd` | yes | Command line, run with `sh -c` (Unix) or `cmd /c` (Windows), so pipes, `&&` and variables work |
| `env` | no | Environment variables, e.g. `{ PORT = "8080" }` |
| `env_file` | no | A `.env` file, relative to `dir` (absolute and `~` paths work too) |
| `depends_on` | no | Projects that must be ready before this one starts |
| `ready_log` | no | Regular expression ([Go syntax](https://pkg.go.dev/regexp/syntax)); the project is ready once an output line matches. Colors are stripped before matching. Without it, a project is ready as soon as it has started. |
| `color` | no | Color of the name in the TUI: `black`, `red`, `green`, `yellow`, `blue`, `magenta`, `cyan`, `white`, their `bright-` variants (`bright-red`, …), an ANSI number `0`–`255`, or a hex code `#f80` / `#ff8800`. Projects without one get a color from a fixed palette, in name order, so a project has the same color in the launcher and every session. |
| `display_name` | no | Label shown in the TUI instead of the project name |

Names of projects and groups may contain letters, digits, `.`, `_` and `-` (starting with a
letter or digit), and share one namespace so `runr up <name>` is never ambiguous.

### Validation

runr checks the config when it loads it and before every save:

- group members and `depends_on` entries must exist, and dependencies can't form a cycle
  (reported as e.g. `api → auth → api`)
- `ready_log` must be a valid regex, `color` a known color
- `dir` and `env_file` should exist — only a warning in `runr ls` and the forms, but
  `runr up` refuses to start a project whose directory is missing

## The launcher

`runr` without arguments lists your groups and projects. Select some and press `enter` to
start them in the running view, or manage the registry with in-TUI forms. Groups are shown as
cards with a colored dot per member, side by side as far as the terminal is wide; projects
are listed below them with their color, directory, branch, command and dependencies.

| Key | Action |
|---|---|
| `↑` `↓`, `home` `end` | Move: through the group cards row by row, then down the project list |
| `←` `→` | Move between the group cards in a row |
| `space` | Select / unselect; on a group, selects all its projects (or unselects them if all are selected) |
| `enter` | Start the selection, or the item under the cursor if nothing is selected |
| `n` | New project |
| `e` | Edit the project (or group) under the cursor; changing the name renames it |
| `g` | On a group: edit it. On a project: create a group, preselecting the selected projects |
| `d` | Delete, after confirmation that lists the affected groups and dependents |
| `b` | Switch the git branch of the project under the cursor (see [Git](#git)) |
| `p` | Pull the git repository of the project under the cursor (`git pull --ff-only`), after confirming |
| `t` | Pick a color theme; the launcher previews the theme under the cursor, `enter` saves it, `esc` keeps the old one |
| `q`, `esc` | Quit |

In forms: `tab`/`enter` (or `↑↓`) next field, `shift+tab` back, `←→` and `space` pick
dependencies or group members, `↑↓` on the color cycles through the named colors, `enter` in
the environment starts a new line. `ctrl+s` saves from any field, `esc` cancels. Errors show
under their field once you've edited or left it, and are counted in the status bar; the form
can't be saved until they are fixed. The directory and env file are checked as you type
(`✓ exists · git: main`), and a preview shows the project's row as the running view will.

## The running view

The sidebar lists the processes with their state; the main area shows their output —
either all of them interleaved (the combined view) or just the selected one. Below them,
under `not running`, the sidebar lists the other registered projects, dimmed; select one and
press `s` or `enter` to start it (with its dependencies). Turn that list off with
`show_all = false` in `[settings]`, or toggle it for a session with `a`. When something
crashes or is blocked, a toast in the corner of the output says so for 5 seconds.

| Icon | State |
|---|---|
| `○` | pending |
| `◐` | waiting for dependencies, or started but its `ready_log` hasn't matched yet |
| `●` | running (ready) |
| `✓` | exited with code 0 |
| `✗` | crashed — non-zero exit code shown next to it |
| `■` | stopped by you |
| `⊘` | blocked: a dependency ended before it became ready |

| Key | Action |
|---|---|
| `↑` `↓` | Select a process |
| `tab` | Toggle between the combined view and the selected process's output |
| `r` | Restart the selected process |
| `s` | Stop the selected process, or start it (with its dependencies) if it isn't running |
| `enter` | Start the selected project if it's one of those not running |
| `+` | Start a group, or another project when the sidebar doesn't list them all (type to filter, `enter` to start, `esc` to close) |
| `a` | Show or hide the projects that aren't running |
| `!` | Select the project of the newest crash toast |
| `b` | Switch the selected project's git repository to another branch (see [Git](#git)) |
| `p` | Pull the selected project's git repository (`git pull --ff-only`), after confirming |
| `/` | Search the output; `enter` jumps to the newest match, `esc` cancels |
| `n` / `N` | Next newer / older match |
| `esc` | Clear the search |
| `PgUp` / `PgDn` | Scroll a page |
| `g` / `G` (`home` / `end`) | Jump to the top / bottom |
| `f` | Toggle follow (auto-scroll); scrolling to the bottom resumes it |
| `q`, `Ctrl+C` | Stop everything and quit; a second `Ctrl+C` kills everything immediately |

Search is plain text (no regex) and smart-case: an all-lowercase query ignores case, any
uppercase letter makes it case-sensitive.

runr adds its own lines to each process's log, such as `starting: <cmd>`,
`waiting for db`, `ready` and `crashed (exit code 1)`, so you can see what happened when.

## Git

When `git` is installed, the sidebar and the launcher show the branch of the repository
each project's `dir` is in, with markers:

```
 ▸ ● api     feat/login * ↓2
   ● web     main ↑1
   ✗ worker  (a1b2c3d) !
```

| Marker | Meaning |
|---|---|
| `*` | uncommitted changes to tracked files (staged or unstaged) |
| `↑n` / `↓n` | commits ahead of / behind the upstream, as of the last fetch or pull |
| `!` | a merge, rebase, cherry-pick, revert or `git am` in progress, or unresolved conflicts |
| `(a1b2c3d)` | detached HEAD |
| `?` (faint) | the state can't be read, e.g. git refuses the directory ("dubious ownership") |

Projects outside a repository show nothing. The state is read every 5 seconds and after
each action; runr never fetches on its own.

`b` (switch branch) and `p` (pull) work in the launcher and the running view. They act on the
selected project's repository, and only in the simple cases — anything that needs a decision
is left to you:

- Both are refused up front while the repository has uncommitted changes to tracked files,
  unresolved conflicts, or an operation in progress. Untracked files don't count, but git
  still refuses to overwrite them.
- `b` lists local branches and remote branches without a local one (most recent first),
  each with how far it is ahead/behind its upstream (`↑2 ↓1`), its last commit's subject and
  date; picking a remote branch creates a local branch that tracks it. The bottom of the list
  shows the git command `enter` runs. Type to filter; the matching part is highlighted.
- `p` fast-forwards the current branch from its upstream and fails if the branch has
  diverged — runr never merges, rebases, stashes or resets. It is refused on a detached
  HEAD or a branch without an upstream.
- If other projects are in the same repository, the picker (`also switches: …`) or the
  confirmation says so,
  since they change too (in the launcher: any registered project; in the running view:
  projects in the session).
- In the running view, git's output goes to the selected project's log, and a short summary
  or error to the bottom line. In the launcher, git's output is shown when it fails. Running processes are not restarted; press `r` if your dev server doesn't
  reload by itself.
- git never prompts: it gets no input, credential prompts are off and `ssh` runs in
  batch mode, so a pull that needs a password or passphrase fails instead of hanging — use
  an SSH agent or a credential helper. A `GIT_SSH_COMMAND` of your own is used as is; a
  `core.sshCommand` too, with `-o BatchMode=yes` added when it runs `ssh`. Each git command
  times out after 60 seconds.

## How processes are run

- **Dependencies.** Starting a project starts everything in its `depends_on`, transitively.
  Independent projects start in parallel; a project starts once all its dependencies are
  ready. A dependency that exits with code 0 (a migration, a build step) counts as ready. If
  a dependency exits or crashes *before* it was ready, its dependents are blocked (`⊘`);
  restarting the dependency unblocks them. A crash *after* it was ready leaves dependents
  running.
- **Environment**, later entries win: runr's own environment → `FORCE_COLOR=1`,
  `CLICOLOR_FORCE=1`, `PYTHONUNBUFFERED=1` → `env_file` → `env`.
- **Output** is read from pipes, not a terminal. The variables above get most tools to keep
  colors and not buffer their output; tools that ignore them may print without colors or
  in bursts. Processes get no input (stdin is empty). Lines longer than 64 KiB are split.
- **Stopping** (`s`, `r`, `q`): each process and everything it started gets SIGTERM
  (`CTRL_BREAK` on Windows) and `shutdown_timeout` to exit before it is killed. On quit,
  projects stop in reverse dependency order: dependents first, then what they depend on.
- **Processes left behind.** If a command exits but something it started keeps running in
  the background, runr stops that too (on Unix it gets SIGTERM and `shutdown_timeout`
  first). Programs that fully detach themselves (daemons) are not affected.
- runr never restarts a process on its own.

## Troubleshooting

- **No colors, or output arrives late:** the program thinks it isn't writing to a terminal.
  Look for a flag or variable that forces colors / line buffering (e.g. `--color=always`)
  and add it to `cmd` or `env`.
- **`stopped (signal: terminated)` although the program shut down cleanly:** wrappers like
  `go run` report the signal rather than the program's own exit code. Harmless.
- **npm prints an update notice when stopping:** add
  `--env NPM_CONFIG_UPDATE_NOTIFIER=false`.
- **`theme_background = true` has no effect:** the terminal didn't answer runr's question
  about its background color (inside tmux this happens when tmux doesn't know the outer
  terminal's colors), or it ignores programs setting its colors. The theme is still used for
  the text. With 16 colors, and with the `terminal` theme, nothing is painted.
- **The terminal kept runr's background after runr was killed:** runr restores the colors when
  it exits, but not when it's killed with `SIGKILL`. Run `reset`, or
  `printf '\e]110\a\e]111\a'`.
- **A project is stuck at `◐`:** it is waiting for a dependency, or its `ready_log` hasn't
  matched. Select it and press `tab` to see its log; runr's lines say what it's waiting for.
