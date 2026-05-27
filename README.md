# flox-auto-activate

Auto-runs `flox activate` in a subshell when you `cd` into a directory
containing a `.flox/` env, and exits that subshell when you `cd` out.
Inspired by [direnv](https://direnv.net/), adapted to flox's
subshell-based activation model.

`-s` (`--start-services`) is opt-in per env; see [Services](#services--s).

## Why this exists

The obvious alternative is to use direnv with flox's built-in direnv
integration. That works for env vars, but it has a real problem with
flox **services**:

- direnv's integration does not auto-start services.
- Worse, while the env is active through direnv, manually running
  `flox services start` fails — services cannot be started at all
  in a direnv-activated environment. See
  [flox/flox#2378](https://github.com/flox/flox/issues/2378).

That bug is the original reason this tool exists. By running a real
`flox activate` subshell (instead of pulling env vars into the
current shell the way direnv does), `flox services start` works
normally — and you can opt into automatic `-s` per-env with
[`services on`](#services--s).

## What it does

- `cd` into `~/proj` (where `~/proj/.flox/` exists) → prompts the first
  time, then runs `flox activate -d ~/proj`. You are now in an
  activated subshell.
- `cd` into a subdirectory of `~/proj` → nothing happens, you stay in
  the activated subshell.
- `cd` into a sibling directory or up out of `~/proj` → the subshell
  exits, and your parent shell `cd`s to wherever you tried to go. If
  that destination is itself a `.flox` env, a new activation kicks in.
- `cd` into a *nested* `.flox` from within an activation → stacks a
  new subshell on top. `exit` (or `cd` further out) pops one level at
  a time.
- Type `exit` manually → drops back to the parent shell at the same
  directory, **un-activated**. The next `cd` re-evaluates whether to
  activate.
- Activate `flox` yourself outside this tool (so `FLOX_ENV` is set
  but our marker isn't) → the hook does nothing. You're in control.

## Install

### Download a release (recommended)

Grab the right binary for your OS and arch from the
[latest release](https://github.com/jasoncorbett/flox-auto-activate/releases/latest) —
`flox-auto-activate-<linux|darwin>-<amd64|arm64>` — then install and
wire up the hook:

```sh
# example: macOS arm64
curl -fSL -o flox-auto-activate \
  https://github.com/jasoncorbett/flox-auto-activate/releases/latest/download/flox-auto-activate-darwin-arm64
chmod +x flox-auto-activate
mv flox-auto-activate ~/.local/bin/    # or anywhere on $PATH

# add to ~/.zshrc:
echo 'eval "$(flox-auto-activate hook zsh)"'  >> ~/.zshrc

# add to ~/.bashrc:
echo 'eval "$(flox-auto-activate hook bash)"' >> ~/.bashrc
```

Each release also publishes a matching `.sha256` file you can verify
with `shasum -a 256 -c flox-auto-activate-darwin-arm64.sha256`.

### Build from source

```sh
git clone https://github.com/jasoncorbett/flox-auto-activate.git
cd flox-auto-activate
go build -o flox-auto-activate ./...
```

### Hook placement

The hook eval needs to be late enough in your rc file that
`chpwd_functions` / `PROMPT_COMMAND` exist as you expect, and early
enough that subshells inherit it. Anywhere near where you put `direnv`
is fine.

## Updating

```sh
flox-auto-activate self-update                # install latest release in place
flox-auto-activate self-update --check        # exit 0 if up to date, 10 if newer is available
flox-auto-activate self-update --version v1.1.0   # pin to a specific tag
flox-auto-activate self-update --force        # update a dev build, reinstall same version, or downgrade
```

`self-update` queries the GitHub releases API directly via `net/http`
(no `gh` CLI dependency), picks the asset matching your `GOOS`/`GOARCH`,
verifies the published `.sha256`, and atomically replaces the running
binary in place. The old inode stays valid for any process that's
already executing, so it's safe to update while shells are open.

`--check` exits 0 / 10 / 1 (up-to-date / update-available / error) —
handy for cron, shell-prompt nags, or a CI gate.

Dev builds (`go build` without the release ldflags) refuse to update
unless you pass `--force`, so iterating locally won't silently
clobber your in-progress binary.

## Commands

```
hook <bash|zsh>     emit the shell hook (eval this in ~/.bashrc or ~/.zshrc)
export <bash|zsh>   emit shell code; called by the hook on every cd
allow [path]        approve auto-activation for the .flox dir at path
deny  [path]        block auto-activation for the .flox dir at path
status [path]       show allow/deny + services pref for the .flox dir
services <on|off|default> [path]
                    set per-path service-start preference
version             print version
help [command]      print top-level or per-command help
```

Run `flox-auto-activate help <command>` (or `<command> --help`) for
detailed help on each.

## Allow / deny

The first time you `cd` into an unknown `.flox` directory the hook
prompts on `/dev/tty`:

```
flox-auto-activate: allow auto-activation for /Users/you/proj? [y/N]
```

- `y` / `yes` → recorded as **allowed**, activation proceeds.
- anything else (or empty, or Ctrl-C) → recorded as **denied**, no
  activation. Future entries are silent until you `allow` it.

Pre-approve from your dotfiles:

```sh
flox-auto-activate allow ~/code/some-project              # path must exist
flox-auto-activate allow --preapprove ~/code/another      # path may not exist yet
```

`--preapprove` is required when the target path doesn't exist. Be
careful: preapproving a path means anyone who can create that directory
before you do can plant a `.flox` there and have it auto-activate
without prompting. Only preapprove paths inside locations you
exclusively control (e.g. your own `~/code/` tree).

Revoke a previous decision:

```sh
flox-auto-activate deny  ~/code/some-project   # block it
flox-auto-activate allow ~/code/some-project   # un-block it
flox-auto-activate status                       # in the dir: prints allowed/denied/unknown
```

If `/dev/tty` is unavailable (CI, piped invocation), the hook records
a deny instead of hanging, then emits nothing.

## Services (`-s`)

By default the hook runs `flox activate -d <path>` **without** the
`-s` / `--start-services` flag, so flox respects whatever the env's
manifest says (`options.activate.start_services`).

To force services on or off for a specific env:

```sh
flox-auto-activate services on  ~/code/api    # always pass -s
flox-auto-activate services off ~/code/lib    # never pass -s (override manifest opt-in)
flox-auto-activate services default ~/code/api  # remove the pref, manifest decides
```

With no path arg, `services` uses the nearest `.flox` walking up from
the current directory, same as `allow`/`deny`/`status`. Preferences
live in `state.json` (see below) and persist across shells.

`status` prints the current services pref alongside the allow/deny
state.

## How it works

`flox activate` spawns a subshell whenever you don't pass a command.
The hook can't just `eval` the activation in the current shell the
way direnv does, so we use a small parent ↔ subshell protocol:

1. On `cd` (zsh `chpwd_functions`, bash `PROMPT_COMMAND` gated on
   `$PWD` change), the hook runs `flox-auto-activate export <shell>`.
2. In the **outer** shell, if the nearest `.flox` walking up from
   `$PWD` is allowed, `export` emits something like:
   ```sh
   _flox_auto_activate_tmpfile=$(mktemp)
   FLOX_AUTO_ACTIVATE_ROOT='/path' \
     FLOX_AUTO_ACTIVATE_TMPFILE="$_flox_auto_activate_tmpfile" \
     flox activate -d '/path'
   if [ -s "$_flox_auto_activate_tmpfile" ]; then
     cd -- "$(cat "$_flox_auto_activate_tmpfile")"
   fi
   rm -f "$_flox_auto_activate_tmpfile"
   unset _flox_auto_activate_tmpfile
   ```
   (`-s` is added to the `flox activate` line iff you've run
   `flox-auto-activate services on <path>` for this env — see
   [Services](#services--s).)
3. The spawned subshell inherits `FLOX_AUTO_ACTIVATE_ROOT` and
   `FLOX_AUTO_ACTIVATE_TMPFILE`. Its hook sees `ROOT` set, so it is
   in "managed-subshell mode" — it won't try to re-activate the same
   root.
4. When the user `cd`s out of the activated tree, the managed-mode
   hook writes the new pwd to the tmpfile and emits `exit 0`. The
   subshell terminates; the parent reads the tmpfile and `cd`s there.

`ROOT` is set via prefix-assignment (`VAR=val cmd`) rather than
`export`, so it lives only in the subshell — the outer shell's env is
never polluted.

### Recursion guards

- The outer hook never sets `FLOX_AUTO_ACTIVATE_ROOT` in its own env,
  so it always sees "outer mode" and can be re-invoked safely.
- The subshell hook in managed mode skips activation when the nearest
  `.flox` matches its own `ROOT`, so cd-ing between subdirs of the
  same env is a no-op.
- The bash hook short-circuits when `$PWD` is unchanged since the
  last invocation, so `PROMPT_COMMAND` firing on every prompt doesn't
  re-activate.
- Foreign flox activations (`FLOX_ENV` set, no `ROOT`) are passed
  through — we never try to manage a subshell the user started.

## State file

Allow/deny records live in a single JSON file:

- `$XDG_DATA_HOME/flox-auto-activate/state.json`, or
- `~/.local/share/flox-auto-activate/state.json` if `XDG_DATA_HOME`
  is unset.

Override with `FLOX_AUTO_ACTIVATE_STATE_FILE=/path/to/state.json` —
useful for tests, dotfile management, or per-machine variants. Writes
are atomic (`os.Rename` from a temp sibling), so concurrent shells
won't corrupt the file (last writer wins on race).

The format is intentionally simple and editable by hand:

```json
{
  "version": 2,
  "allowed": ["/Users/you/code/proj"],
  "denied":  ["/Users/you/code/scary"],
  "services": {
    "/Users/you/code/api": true,
    "/Users/you/code/lib": false
  }
}
```

`version: 1` state files (no `services` block) are read transparently.

## Non-goals / not yet supported

- Shells other than bash and zsh (no fish, no nushell).
- Re-prompting on `.flox/` content change — once allowed, always
  allowed for that absolute path until you explicitly `deny` it.
  (If you want direnv-like content hashing, that's a future change.)
- Symlink-resolved path comparison. A symlinked path that doesn't
  match the literal stored path will be treated as a different
  directory.
- `flox activate` failures aren't intercepted; the parent shell sees
  whatever flox printed and continues.

## Development

```sh
go test ./...           # 16 unit tests across paths, state, decide
go build -o flox-auto-activate ./...
```

`decide()` in `export.go` is a pure function — env map + pwd + state
→ Decision — and is tested directly without spawning shells.
