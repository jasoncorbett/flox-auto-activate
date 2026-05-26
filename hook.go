package main

import (
	"fmt"
	"strings"
)

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func hookZsh(self string) string {
	q := shQuote(self)
	return fmt.Sprintf(`_flox_auto_activate_hook() {
  trap -- '' SIGINT
  eval "$(%s export zsh)"
  trap - SIGINT
}
typeset -ag chpwd_functions
if (( ! ${chpwd_functions[(I)_flox_auto_activate_hook]} )); then
  chpwd_functions=(_flox_auto_activate_hook $chpwd_functions)
fi
_flox_auto_activate_hook
`, q)
}

func hookBash(self string) string {
	q := shQuote(self)
	return fmt.Sprintf(`_flox_auto_activate_hook() {
  local previous_exit_status=$?
  if [[ "${_FLOX_AUTO_ACTIVATE_LAST_PWD-}" != "$PWD" ]]; then
    _FLOX_AUTO_ACTIVATE_LAST_PWD="$PWD"
    trap -- '' SIGINT
    eval "$(%s export bash)"
    trap - SIGINT
  fi
  return $previous_exit_status
}
if [[ ";${PROMPT_COMMAND[*]:-};" != *";_flox_auto_activate_hook;"* ]]; then
  if [[ "$(declare -p PROMPT_COMMAND 2>&1)" == "declare -a"* ]]; then
    PROMPT_COMMAND=(_flox_auto_activate_hook "${PROMPT_COMMAND[@]}")
  else
    PROMPT_COMMAND="_flox_auto_activate_hook${PROMPT_COMMAND:+;$PROMPT_COMMAND}"
  fi
fi
`, q)
}
