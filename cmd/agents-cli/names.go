package main

import (
	"regexp"
	"strings"
	"unicode"
)

// TECH-273: a session is named by its ticket ID when its slug/label carries
// one (CSF-166, never the full task slug); otherwise by a short word-boundary
// prefix. The full slug stays in Session.Task. The CSF gateway applies the
// same rule (gateway/app/session_names.py) so every surface agrees.
const sessionNameMax = 24

var sessionTicketRe = regexp.MustCompile(`^(?:[a-z]+:)?([A-Z][A-Z0-9]+-\d+(?:\.\d+)?)(?:$|[^A-Za-z0-9])`)

func sessionTicketID(s string) string {
	s = strings.TrimLeftFunc(strings.Join(strings.Fields(s), " "), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if m := sessionTicketRe.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

func shortSessionName(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if id := sessionTicketID(s); id != "" {
		return id
	}
	r := []rune(s)
	if len(r) <= sessionNameMax {
		return s
	}
	if strings.ContainsRune(" -_", r[sessionNameMax]) {
		return string(r[:sessionNameMax])
	}
	cut := string(r[:sessionNameMax])
	for _, sep := range []string{" ", "-", "_"} {
		if i := strings.LastIndex(cut, sep); i >= 0 && len([]rune(cut[:i])) >= 8 {
			return strings.TrimRight(cut[:i], " -_")
		}
	}
	return cut
}
