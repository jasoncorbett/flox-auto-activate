package main

import (
	"fmt"
	"io"
	"strings"
)

type decisionKind int

const (
	decideNothing decisionKind = iota
	decideExit
	decideActivate
	decideNeedPrompt
)

type Decision struct {
	Kind decisionKind
	// Path is the .flox root to activate (decideActivate / decideNeedPrompt).
	Path string
	// Target is the directory to record before exiting (decideExit).
	Target string
	// TmpFile is the path the managed subshell writes Target into before exit.
	TmpFile string
	// StartServices, when true on decideActivate, causes emit to include
	// `-s` in the flox activate command line.
	StartServices bool
}

// decide is pure: given environment + pwd + state, return what should happen.
// state may be nil for tests that don't need allow/deny lookups; in that case
// any "would activate" outcome becomes decideNeedPrompt.
func decide(env map[string]string, pwd string, state *State) Decision {
	root := env["FLOX_AUTO_ACTIVATE_ROOT"]
	tmpfile := env["FLOX_AUTO_ACTIVATE_TMPFILE"]

	if root != "" {
		// Managed subshell mode.
		if !isUnder(pwd, root) {
			return Decision{Kind: decideExit, Target: pwd, TmpFile: tmpfile}
		}
		nearest, ok := findFloxRoot(pwd)
		if !ok || nearest == root {
			return Decision{Kind: decideNothing}
		}
		return gateActivation(nearest, state)
	}

	if env["FLOX_ENV"] != "" {
		// Foreign flox env — user activated manually. Stay out of it.
		return Decision{Kind: decideNothing}
	}

	nearest, ok := findFloxRoot(pwd)
	if !ok {
		return Decision{Kind: decideNothing}
	}
	return gateActivation(nearest, state)
}

func gateActivation(path string, state *State) Decision {
	if state == nil {
		return Decision{Kind: decideNeedPrompt, Path: path}
	}
	switch state.Status(path) {
	case StatusAllowed:
		enabled, _ := state.ServicesPref(path)
		return Decision{Kind: decideActivate, Path: path, StartServices: enabled}
	case StatusDenied:
		return Decision{Kind: decideNothing}
	default:
		return Decision{Kind: decideNeedPrompt, Path: path}
	}
}

// emit renders a Decision as shell code. activate/exit blocks are valid bash
// and zsh.
func emit(d Decision) string {
	switch d.Kind {
	case decideNothing, decideNeedPrompt:
		return ""
	case decideExit:
		var b strings.Builder
		if d.TmpFile != "" {
			fmt.Fprintf(&b, "printf %%s %s > %s\n", shQuote(d.Target), shQuote(d.TmpFile))
		}
		b.WriteString("exit 0\n")
		return b.String()
	case decideActivate:
		p := shQuote(d.Path)
		servicesFlag := ""
		if d.StartServices {
			servicesFlag = " -s"
		}
		var b strings.Builder
		b.WriteString("_flox_auto_activate_tmpfile=$(mktemp)\n")
		fmt.Fprintf(&b,
			"FLOX_AUTO_ACTIVATE_ROOT=%s FLOX_AUTO_ACTIVATE_TMPFILE=\"$_flox_auto_activate_tmpfile\" flox activate%s -d %s\n",
			p, servicesFlag, p)
		b.WriteString("if [ -s \"$_flox_auto_activate_tmpfile\" ]; then\n")
		b.WriteString("  cd -- \"$(cat \"$_flox_auto_activate_tmpfile\")\"\n")
		b.WriteString("fi\n")
		b.WriteString("rm -f \"$_flox_auto_activate_tmpfile\"\n")
		b.WriteString("unset _flox_auto_activate_tmpfile\n")
		return b.String()
	}
	return ""
}

// runExport wires decide() to real I/O for the `export` subcommand.
func runExport(env map[string]string, pwd string, state *State, out io.Writer) error {
	d := decide(env, pwd, state)
	if d.Kind == decideNeedPrompt {
		ok, err := confirmAtTTY(fmt.Sprintf("flox-auto-activate: allow auto-activation for %s? [y/N]", d.Path))
		if err != nil {
			// Can't ask — treat as one-shot skip. Persist deny so we don't
			// block again on the next pwd change.
			state.Deny(d.Path)
			if saveErr := state.save(); saveErr != nil {
				return saveErr
			}
			fmt.Fprintf(out, "# flox-auto-activate: no tty available, recorded deny for %s\n", d.Path)
			return nil
		}
		if ok {
			state.Allow(d.Path)
			if err := state.save(); err != nil {
				return err
			}
			enabled, _ := state.ServicesPref(d.Path)
			d = Decision{Kind: decideActivate, Path: d.Path, StartServices: enabled}
		} else {
			state.Deny(d.Path)
			if err := state.save(); err != nil {
				return err
			}
			d = Decision{Kind: decideNothing}
		}
	}
	if s := emit(d); s != "" {
		if _, err := io.WriteString(out, s); err != nil {
			return err
		}
	}
	return nil
}
