package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// A launcher title watchdog rereads WindowTitle after the visible caption has
// been set. Save a reviewed title to the record for this process generation.
func watchRenameWindow(s watchSample, name string) error {
	if err := watchDoRename(s, name); err != nil || s.Tool != "codex" {
		return err
	}
	dir := launchInfoDir()
	path := filepath.Clean(envVar(s.AgentPID, "CODEX_LAUNCH_INFO"))
	if !strings.EqualFold(filepath.Dir(path), filepath.Clean(dir)) || !strings.HasSuffix(strings.ToLower(path), ".launch-info.json") {
		path = ""
		if s.Task != "" && filepath.IsLocal(s.Task) && filepath.Base(s.Task) == s.Task {
			path = filepath.Join(dir, s.Task+".launch-info.json")
		}
	}
	if path != "" {
		matched, err := updateLauncherTitle(path, s, name)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("Window renamed, but saving its launcher title failed: %w", err)
		}
		if !matched && s.Task != "" {
			return errors.New("Window renamed, but its launcher record changed. Reopen Rename.")
		}
	}
	// The standalone Codex rename helper can also start a watchdog.
	overrides, err := filepath.Glob(`C:\scripts\state\codex-title-overrides\*.launch-info.json`)
	if err != nil {
		return err
	}
	for _, path := range overrides {
		// Old override files can outlive their consoles. Only a failure
		// writing a positively identified override concerns this rename.
		if matched, err := updateLauncherTitle(path, s, name); matched && err != nil {
			return fmt.Errorf("Window renamed, but checking its title override failed: %w", err)
		}
	}
	return nil
}

func updateLauncherTitle(path string, s watchSample, name string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	bom := []byte{0xef, 0xbb, 0xbf}
	body := bytes.TrimPrefix(raw, bom)
	var info launchInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return false, err
	}
	start, err := time.Parse(time.RFC3339Nano, info.ProcessStartTimeUtc)
	if err != nil || start.UTC().Format(time.RFC3339Nano) != s.RootStarted || info.PID.String() != strconv.FormatUint(uint64(s.PID), 10) || !strings.EqualFold(info.Tool, "codex") {
		return false, nil
	}
	if info.WindowTitle == name {
		return true, nil
	}
	return true, replaceLauncherTitle(path, raw, body, name)
}

func replaceLauncherTitle(path string, raw, body []byte, name string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return err
	}
	title, err := json.Marshal(name)
	if err != nil {
		return err
	}
	fields["WindowTitle"] = title
	updated, err := json.MarshalIndent(fields, "", "    ")
	if err != nil {
		return err
	}
	// Windows PowerShell 5 reads non-ASCII JSON correctly with a UTF-8 BOM.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".launcher-title-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(append([]byte{0xef, 0xbb, 0xbf}, updated...))
	closeErr := tmp.Close()
	if err := errors.Join(err, closeErr); err != nil {
		return err
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, raw) {
		return errors.New("launcher metadata changed during rename; retry")
	}
	return os.Rename(tmp.Name(), path)
}
