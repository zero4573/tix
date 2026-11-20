package prompt

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/term"
)

var in = bufio.NewReader(os.Stdin)

func Interactive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
}

func readLine() string {
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(os.Stderr, "\ntix: no answer (end of input)")
		os.Exit(1)
	}
	return strings.TrimRight(line, "\r\n")
}

func Ask(question, def string) string {
	fmt.Fprintf(os.Stderr, "%s [%s] ", question, def)
	if a := readLine(); a != "" {
		return a
	}
	return def
}

func Line(question string) string {
	fmt.Fprintf(os.Stderr, "%s ", question)
	return readLine()
}

func YesNo(question, def string) bool {
	a := Ask(question+" (y/n)", def)
	return strings.HasPrefix(strings.ToLower(a), "y")
}

func ConfirmYes(question string) bool {
	fmt.Fprintf(os.Stderr, "%s Type yes to confirm: ", question)
	return readLine() == "yes"
}

func Pick(label string, items []string) (string, error) {
	if len(items) == 0 {
		return "", errors.New("nothing to pick from")
	}
	if _, err := exec.LookPath("fzf"); err == nil {
		cmd := exec.Command("fzf", "--prompt="+label+"> ", "--height=~10", "--reverse")
		cmd.Stdin = strings.NewReader(strings.Join(items, "\n") + "\n")
		cmd.Stderr = os.Stderr
		out, err := cmd.Output()
		if err != nil {
			return "", errors.New("nothing selected")
		}
		return strings.TrimSpace(string(out)), nil
	}
	for i, it := range items {
		fmt.Fprintf(os.Stderr, "  %d) %s\n", i+1, it)
	}
	a := Ask(label, "1")
	n, err := strconv.Atoi(a)
	if err != nil || n < 1 || n > len(items) {
		return "", fmt.Errorf("not a choice: %s", a)
	}
	return items[n-1], nil
}
