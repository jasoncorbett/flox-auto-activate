package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const githubRepo = "jasoncorbett/flox-auto-activate"

var version = "dev"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, errUpdateAvailable) {
			os.Exit(10)
		}
		fmt.Fprintln(os.Stderr, "flox-auto-activate: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stderr)
		return fmt.Errorf("missing subcommand")
	}
	switch args[0] {
	case "hook":
		return cmdHook(args[1:], stdout)
	case "export":
		return cmdExport(args[1:], stdout)
	case "allow":
		return cmdAllow(args[1:], stdout)
	case "deny":
		return cmdDeny(args[1:], stdout)
	case "status":
		return cmdStatus(args[1:], stdout)
	case "services":
		return cmdServices(args[1:], stdout)
	case "self-update":
		return cmdSelfUpdate(args[1:], stdout)
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, version)
		return nil
	case "help", "--help", "-h":
		return cmdHelp(args[1:], stdout)
	default:
		printUsage(stderr)
		return fmt.Errorf("unknown subcommand: %s", args[0])
	}
}

func isHelpFlag(arg string) bool {
	return arg == "-h" || arg == "--help" || arg == "help"
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `usage: flox-auto-activate <command> [args]

Auto-runs `+"`flox activate`"+` in a subshell when you cd into a directory
containing .flox/. On cd out of that tree, the subshell exits and the
parent shell cd's where you wanted to go. First-time entry into an
unknown .flox directory prompts interactively for approval.

By default `+"`-s`"+` (--start-services) is not passed; set it per-env with
`+"`services on <path>`"+`.

install:
  add to ~/.zshrc:  eval "$(flox-auto-activate hook zsh)"
  add to ~/.bashrc: eval "$(flox-auto-activate hook bash)"

commands:
  hook <bash|zsh>     emit the shell hook (eval this in ~/.bashrc or ~/.zshrc)
  export <bash|zsh>   emit shell code; called by the hook on every cd
  allow [path]        approve auto-activation for the .flox dir at path
  deny  [path]        block auto-activation for the .flox dir at path
  status [path]       show allow/deny + services pref for the .flox dir
  services <on|off|default> [path]
                      set per-path service-start preference; default
                      means "respect the env manifest"
  self-update [flags] download and install the latest release in place
  version             print version
  help [command]      print this help, or detailed help for <command>

environment:
  FLOX_AUTO_ACTIVATE_STATE_FILE  override path to the allow/deny JSON
                                  (default: $XDG_DATA_HOME/flox-auto-activate/state.json
                                  or ~/.local/share/flox-auto-activate/state.json)

Run `+"`flox-auto-activate help <command>`"+` for detailed help on a command.
`)
}

var subcommandHelp = map[string]string{
	"hook": `usage: flox-auto-activate hook <bash|zsh>

Print the shell function and registration that hooks into your shell's
prompt/chdir machinery. Eval the output once at shell startup:

  eval "$(flox-auto-activate hook zsh)"     # in ~/.zshrc
  eval "$(flox-auto-activate hook bash)"    # in ~/.bashrc

The zsh hook registers a chpwd_functions entry and fires once at install
time (so opening a terminal already inside a .flox dir activates). The
bash hook registers a PROMPT_COMMAND entry that fires before each prompt,
gated on PWD change so it only acts on real cd's.
`,

	"export": `usage: flox-auto-activate export <bash|zsh>

Print shell code to be eval'd by the hook. You normally do not call this
directly — the hook does on every cd.

Reads PWD plus the FLOX_AUTO_ACTIVATE_ROOT, FLOX_AUTO_ACTIVATE_TMPFILE,
and FLOX_ENV env vars to decide what to emit:

  - In a managed subshell (FLOX_AUTO_ACTIVATE_ROOT set), pwd outside the
    activated tree -> emit code that writes pwd to the tmpfile and exits.
  - In a managed subshell, pwd under the same root -> emit nothing.
  - In a managed subshell, pwd under a different .flox -> emit a nested
    activation block (allow/deny prompt may fire first).
  - With FLOX_ENV set but no FLOX_AUTO_ACTIVATE_ROOT (user activated
    manually) -> emit nothing.
  - Otherwise, walk up for a .flox dir. Activate if allowed; do nothing
    if denied; prompt interactively if unknown.
`,

	"allow": `usage: flox-auto-activate allow [--preapprove] [path]

Approve auto-activation for the .flox-containing directory at <path>.
With no argument, uses the nearest .flox walking up from the current
directory.

By default <path> must already exist. To approve a path that does not
exist yet (dotfile bootstrap, before you clone the repo), pass
--preapprove. Be careful: preapproving a path means anyone who can
create that directory before you do can plant a .flox there and have it
auto-activate without prompting. Only preapprove paths inside locations
you exclusively control.

Paths are cleaned and made absolute before being stored. Any matching
deny record is removed.
`,

	"deny": `usage: flox-auto-activate deny [path]

Block auto-activation for the .flox-containing directory at <path>.
With no argument, uses the nearest .flox walking up from the current
directory.

The hook silently skips activation for denied paths. Run
`+"`flox-auto-activate allow <path>`"+` to lift a deny.
`,

	"status": `usage: flox-auto-activate status [path]

Print whether the .flox-containing directory at <path> is allowed,
denied, or unknown, along with its services preference (on / off /
default-from-manifest). With no argument, uses the nearest .flox
walking up from the current directory.

Exit status is always 0; the state is printed to stdout.
`,

	"services": `usage: flox-auto-activate services <on|off|default> [path]

Set whether ` + "`flox activate`" + ` should be invoked with -s
(--start-services) when auto-activating the .flox dir at <path>.

  on        always pass -s for this path (start services).
  off       never pass -s for this path (do not start services).
  default   remove any explicit preference; flox respects the env's
            manifest (options.activate.start_services).

With no path, uses the nearest .flox walking up from the current
directory. The path does not need to exist yet.

By default (no preference set, no manifest opt-in), services are not
auto-started. Use ` + "`services on`" + ` per-path to opt in.
`,

	"version": `usage: flox-auto-activate version

Print the version of the binary and exit.
`,

	"self-update": `usage: flox-auto-activate self-update [--check] [--force] [--version vX.Y.Z]

Download the latest release of flox-auto-activate from GitHub and
replace the running binary in place. The replacement is atomic
(os.Rename over the executable), so any currently-running process
keeps its existing binary until it exits.

The integrity of the download is verified against the per-asset
.sha256 file published in the release. Network requests go through
Go's net/http directly; the gh CLI is not required.

flags:
  --check              print whether an update is available; exit 0
                       if up to date, 10 if newer is available
  --force              proceed even when the running binary is a dev
                       build, the same version, or newer than the
                       target
  --version vX.Y.Z     install a specific release tag instead of the
                       latest

Dev builds (` + "`go build`" + ` without the release ldflags) refuse to update
unless --force is given.
`,

	"help": `usage: flox-auto-activate help [command]

With no argument, print the top-level usage. With a command name, print
detailed help for that command.
`,
}

func cmdHelp(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		printUsage(stdout)
		return nil
	}
	if len(args) > 1 {
		return fmt.Errorf("help takes at most one command name")
	}
	text, ok := subcommandHelp[args[0]]
	if !ok {
		return fmt.Errorf("unknown command: %s", args[0])
	}
	fmt.Fprint(stdout, text)
	return nil
}

func selfPath() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved, nil
	}
	return p, nil
}

func cmdHook(args []string, stdout io.Writer) error {
	if len(args) == 1 && isHelpFlag(args[0]) {
		fmt.Fprint(stdout, subcommandHelp["hook"])
		return nil
	}
	if len(args) != 1 {
		return fmt.Errorf("hook requires exactly one argument: bash or zsh")
	}
	self, err := selfPath()
	if err != nil {
		return err
	}
	switch args[0] {
	case "zsh":
		_, err = io.WriteString(stdout, hookZsh(self))
	case "bash":
		_, err = io.WriteString(stdout, hookBash(self))
	default:
		return fmt.Errorf("unsupported shell: %s (expected bash or zsh)", args[0])
	}
	return err
}

func cmdExport(args []string, stdout io.Writer) error {
	if len(args) == 1 && isHelpFlag(args[0]) {
		fmt.Fprint(stdout, subcommandHelp["export"])
		return nil
	}
	if len(args) != 1 {
		return fmt.Errorf("export requires exactly one argument: bash or zsh")
	}
	if args[0] != "bash" && args[0] != "zsh" {
		return fmt.Errorf("unsupported shell: %s (expected bash or zsh)", args[0])
	}
	pwd, err := os.Getwd()
	if err != nil {
		return err
	}
	state, err := loadState()
	if err != nil {
		return err
	}
	env := envMap()
	return runExport(env, pwd, state, stdout)
}

func envMap() map[string]string {
	m := make(map[string]string, 8)
	for _, k := range []string{
		"FLOX_AUTO_ACTIVATE_ROOT",
		"FLOX_AUTO_ACTIVATE_TMPFILE",
		"FLOX_ENV",
	} {
		if v := os.Getenv(k); v != "" {
			m[k] = v
		}
	}
	return m
}

func resolvePathArg(args []string) (string, error) {
	if len(args) > 1 {
		return "", fmt.Errorf("expected at most one path argument")
	}
	if len(args) == 1 {
		abs, err := filepath.Abs(args[0])
		if err != nil {
			return "", err
		}
		return filepath.Clean(abs), nil
	}
	pwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	root, ok := findFloxRoot(pwd)
	if !ok {
		return "", fmt.Errorf("no .flox directory found at or above %s; pass a path explicitly", pwd)
	}
	return root, nil
}

func cmdAllow(args []string, stdout io.Writer) error {
	var (
		preapprove bool
		positional []string
	)
	for _, a := range args {
		if isHelpFlag(a) {
			fmt.Fprint(stdout, subcommandHelp["allow"])
			return nil
		}
		if a == "--preapprove" {
			preapprove = true
			continue
		}
		if strings.HasPrefix(a, "-") {
			return fmt.Errorf("unknown flag: %s", a)
		}
		positional = append(positional, a)
	}
	path, err := resolvePathArg(positional)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(path); statErr != nil {
		if !os.IsNotExist(statErr) {
			return statErr
		}
		if !preapprove {
			return fmt.Errorf("path does not exist: %s\n"+
				"  pass --preapprove if you really want to approve a path before it exists.\n"+
				"  see `flox-auto-activate help allow` for the squatting risk this implies.", path)
		}
	}
	state, err := loadState()
	if err != nil {
		return err
	}
	state.Allow(path)
	if err := state.save(); err != nil {
		return err
	}
	if preapprove {
		fmt.Fprintf(stdout, "preapproved: %s\n", path)
	} else {
		fmt.Fprintf(stdout, "allowed: %s\n", path)
	}
	return nil
}

func cmdDeny(args []string, stdout io.Writer) error {
	if len(args) == 1 && isHelpFlag(args[0]) {
		fmt.Fprint(stdout, subcommandHelp["deny"])
		return nil
	}
	path, err := resolvePathArg(args)
	if err != nil {
		return err
	}
	state, err := loadState()
	if err != nil {
		return err
	}
	state.Deny(path)
	if err := state.save(); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "denied: %s\n", path)
	return nil
}

func cmdStatus(args []string, stdout io.Writer) error {
	if len(args) == 1 && isHelpFlag(args[0]) {
		fmt.Fprint(stdout, subcommandHelp["status"])
		return nil
	}
	path, err := resolvePathArg(args)
	if err != nil {
		return err
	}
	state, err := loadState()
	if err != nil {
		return err
	}
	switch state.Status(path) {
	case StatusAllowed:
		fmt.Fprintf(stdout, "allowed: %s\n", path)
	case StatusDenied:
		fmt.Fprintf(stdout, "denied: %s\n", path)
	default:
		fmt.Fprintf(stdout, "unknown: %s\n", path)
	}
	if enabled, set := state.ServicesPref(path); set {
		if enabled {
			fmt.Fprintln(stdout, "services: on")
		} else {
			fmt.Fprintln(stdout, "services: off")
		}
	} else {
		fmt.Fprintln(stdout, "services: default (manifest)")
	}
	return nil
}

func cmdServices(args []string, stdout io.Writer) error {
	if len(args) >= 1 && isHelpFlag(args[0]) {
		fmt.Fprint(stdout, subcommandHelp["services"])
		return nil
	}
	if len(args) == 0 {
		return fmt.Errorf("services requires a mode: on, off, or default")
	}
	mode := args[0]
	if mode != "on" && mode != "off" && mode != "default" {
		return fmt.Errorf("unknown services mode: %s (expected on, off, or default)", mode)
	}
	path, err := resolvePathArg(args[1:])
	if err != nil {
		return err
	}
	state, err := loadState()
	if err != nil {
		return err
	}
	switch mode {
	case "on":
		state.SetServices(path, true)
		fmt.Fprintf(stdout, "services on: %s\n", path)
	case "off":
		state.SetServices(path, false)
		fmt.Fprintf(stdout, "services off: %s\n", path)
	case "default":
		state.UnsetServices(path)
		fmt.Fprintf(stdout, "services default: %s\n", path)
	}
	return state.save()
}

func cmdSelfUpdate(args []string, stdout io.Writer) error {
	var opts selfUpdateOpts
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case isHelpFlag(a):
			fmt.Fprint(stdout, subcommandHelp["self-update"])
			return nil
		case a == "--check":
			opts.Check = true
		case a == "--force":
			opts.Force = true
		case a == "--version":
			if i+1 >= len(args) {
				return fmt.Errorf("--version requires a tag argument (e.g. v1.0.0)")
			}
			i++
			opts.Version = args[i]
		case strings.HasPrefix(a, "--version="):
			opts.Version = strings.TrimPrefix(a, "--version=")
		default:
			return fmt.Errorf("unknown flag: %s", a)
		}
	}
	if opts.Version != "" && !strings.HasPrefix(opts.Version, "v") {
		opts.Version = "v" + opts.Version
	}

	self, err := selfPath()
	if err != nil {
		return err
	}

	s := &selfUpdate{
		apiBase:    "https://api.github.com",
		repo:       githubRepo,
		http:       &http.Client{Timeout: 120 * time.Second},
		currentVer: version,
		goos:       runtime.GOOS,
		goarch:     runtime.GOARCH,
		selfPath:   self,
		out:        stdout,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	return s.run(ctx, opts)
}
