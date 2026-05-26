package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

func confirmAtTTY(question string) (bool, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return false, fmt.Errorf("open /dev/tty: %w", err)
	}
	defer tty.Close()

	if _, err := fmt.Fprintf(tty, "%s ", question); err != nil {
		return false, err
	}
	scanner := bufio.NewScanner(tty)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return false, err
		}
		return false, errors.New("eof on /dev/tty")
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer == "y" || answer == "yes", nil
}
