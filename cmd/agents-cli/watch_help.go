package main

import (
	_ "embed"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

//go:embed help.html
var watchHelpHTML string

func writeWatchHelp(dir, commit string) (string, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "index.html")
	page := strings.ReplaceAll(watchHelpHTML, "{{BUILD}}", html.EscapeString(commit))
	if err := os.WriteFile(path, []byte(page), 0600); err != nil {
		return "", err
	}
	return path, nil
}

func openWatchHelp() error {
	base, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	path, err := writeWatchHelp(filepath.Join(base, "agents-cli", "help"), buildCommit)
	if err != nil {
		return err
	}
	// Prefer Brave where available; colleagues without it use their browser
	// association. No browser dependency or server is installed by this feature.
	for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA")} {
		if root == "" {
			continue
		}
		browser := filepath.Join(root, "BraveSoftware", "Brave-Browser", "Application", "brave.exe")
		if _, err := os.Stat(browser); err != nil {
			continue
		}
		cmd := exec.Command(browser, "--new-window", path)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	}
	r, _, _ := windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteW").Call(0,
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr("open"))), uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(path))), 0, 0, 1)
	if r <= 32 {
		return fmt.Errorf("cannot open help page (Windows code %d)", r)
	}
	return nil
}
