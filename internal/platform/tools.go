package platform

import (
	"os/exec"
	"runtime"
)

type Urgency int

const (
	Normal Urgency = iota
	Critical
	Sticky // notify-send -t 0: a notification daemon may ignore it
)

func Notify(app, title, body string, u Urgency) {
	if argv := notifyCommand(runtime.GOOS, app, title, body, u); argv != nil {
		if path, err := exec.LookPath(argv[0]); err == nil {
			_ = exec.Command(path, argv[1:]...).Run()
		}
	}
}

func notifyCommand(goos, app, title, body string, u Urgency) []string {
	switch goos {
	case "windows":
		return nil
	case "darwin":
		return []string{"osascript", "-e", "on run argv\ndisplay notification (item 2 of argv) with title (item 1 of argv)\nend run", title, body}
	default:
		argv := []string{"notify-send", "-a", app}
		if u >= Critical {
			argv = append(argv, "-u", "critical")
		}
		if u == Sticky {
			argv = append(argv, "-t", "0")
		}
		return append(argv, title, body)
	}
}

func OpenCommand(target string) []string { return openCommand(runtime.GOOS, target) }

func openCommand(goos, target string) []string {
	switch goos {
	case "windows":
		return []string{"rundll32", "url.dll,FileProtocolHandler", target}
	case "darwin":
		return []string{"open", target}
	default:
		return []string{"xdg-open", target}
	}
}

func ShellCommand(line string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/C", line}
	}
	if _, err := exec.LookPath("bash"); err == nil {
		return []string{"bash", "-c", line}
	}
	return []string{"sh", "-c", line}
}
