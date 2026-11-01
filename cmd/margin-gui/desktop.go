package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// install puts margin-gui where the desktop finds applications: a launcher entry and icon on
// Linux, an app bundle in ~/Applications on macOS. uninstall takes them away again.
func install(remove bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	icon, err := assets.ReadFile("assets/icon.svg")
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "linux":
		data := os.Getenv("XDG_DATA_HOME")
		if data == "" {
			data = filepath.Join(home, ".local", "share")
		}
		entry := filepath.Join(data, "applications", "margin.desktop")
		iconPath := filepath.Join(data, "icons", "hicolor", "scalable", "apps", "margin.svg")
		// The launcher starts a login shell first, so the window gets the PATH the reader's terminal
		// has and finds the agent.
		launcher := filepath.Join(data, "margin", "margin-gui-launch")
		if remove {
			return errors.Join(os.Remove(entry), os.Remove(iconPath), os.Remove(launcher))
		}
		script := "#!/bin/sh\nexec \"${SHELL:-/bin/sh}\" -lc 'exec " + shellQuote(exe) + "'\n"
		if err := writeFile(launcher, []byte(script)); err != nil {
			return err
		}
		if err := os.Chmod(launcher, 0o755); err != nil {
			return err
		}
		desktop := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=margin\nGenericName=Code review\nComment=Review git changes with an agent beside you\n"+
			"Exec=%s\nIcon=margin\nTerminal=false\nCategories=Development;RevisionControl;\nStartupWMClass=margin\n", desktopQuote(launcher))
		if err := writeFile(iconPath, icon); err != nil {
			return err
		}
		if err := writeFile(entry, []byte(desktop)); err != nil {
			return err
		}
		fmt.Println("installed", entry)
	case "darwin":
		app := filepath.Join(home, "Applications", "margin.app")
		if remove {
			return os.RemoveAll(app)
		}
		plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleName</key><string>margin</string>
<key>CFBundleIdentifier</key><string>io.github.redrick.margin</string>
<key>CFBundleExecutable</key><string>margin</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>NSHighResolutionCapable</key><true/>
</dict></plist>
`
		launcher := "#!/bin/sh\nexec \"${SHELL:-/bin/zsh}\" -lc 'exec " + shellQuote(exe) + "'\n"
		if err := writeFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist)); err != nil {
			return err
		}
		if err := writeFile(filepath.Join(app, "Contents", "MacOS", "margin"), []byte(launcher)); err != nil {
			return err
		}
		if err := os.Chmod(filepath.Join(app, "Contents", "MacOS", "margin"), 0o755); err != nil {
			return err
		}
		fmt.Println("installed", app)
	default:
		return fmt.Errorf("installing a launcher is not supported on %s; run %s directly", runtime.GOOS, exe)
	}
	return nil
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func shellQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`").Replace(s) + `"`
}

// desktopQuote quotes an Exec argument the way the desktop entry spec asks, when it needs quoting.
func desktopQuote(s string) string {
	if !strings.ContainsAny(s, " \t\n\"'\\><~|&;$*?#()`") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\\\`, `"`, `\\"`, "`", "\\\\`", "$", `\\$`).Replace(s) + `"`
}
