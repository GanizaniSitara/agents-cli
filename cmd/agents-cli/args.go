package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// parsed holds positional arguments and --flags from a verb's argument list.
// Flags may appear anywhere ("send 1234 hello --no-enter" is the common shape),
// which the standard flag package does not allow.
type parsed struct {
	pos   []string
	flags map[string]string
}

func parseArgs(args []string, boolFlags ...string) (parsed, error) {
	isBool := map[string]bool{}
	for _, b := range boolFlags {
		isBool[b] = true
	}
	p := parsed{flags: map[string]string{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			p.pos = append(p.pos, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "--") {
			p.pos = append(p.pos, a)
			continue
		}
		name := strings.TrimPrefix(a, "--")
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			p.flags[name[:eq]] = name[eq+1:]
			continue
		}
		if isBool[name] {
			p.flags[name] = "true"
			continue
		}
		if i+1 >= len(args) {
			return p, fmt.Errorf("flag --%s needs a value", name)
		}
		p.flags[name] = args[i+1]
		i++
	}
	return p, nil
}

func (p parsed) has(name string) bool { _, ok := p.flags[name]; return ok }

func (p parsed) str(name, def string) string {
	if v, ok := p.flags[name]; ok {
		return v
	}
	return def
}

func (p parsed) boolean(name string) bool { return p.flags[name] == "true" }

func (p parsed) intFlag(name string, def int) (int, error) {
	v, ok := p.flags[name]
	if !ok {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("--%s: %v", name, err)
	}
	return n, nil
}

// durationFlag accepts Go durations plus a 'd' suffix for days ("7d").
func (p parsed) durationFlag(name string, def time.Duration) (time.Duration, error) {
	v, ok := p.flags[name]
	if !ok {
		return def, nil
	}
	return parseDuration(v)
}

func parseDuration(v string) (time.Duration, error) {
	if strings.HasSuffix(v, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(v, "d"))
		if err != nil {
			return 0, fmt.Errorf("bad duration %q", v)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("bad duration %q", v)
	}
	return d, nil
}
