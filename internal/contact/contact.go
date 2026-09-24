// Package contact decides whether a value is a contact BSI TR-03183-2 v2.1.0 accepts for a
// creator: an email address, or failing that a URL (§5.2.1, §5.2.2). The .msis variables that
// declare such contacts, the reading of them back out of an artifact, and the conformance check
// all ask the same question, so it is answered once (#64).
package contact

import (
	"net/mail"
	"net/url"
	"strings"
)

// IsURL reports whether s is an absolute http or https URL with a host.
func IsURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" &&
		!strings.ContainsAny(s, " \t\r\n")
}

// IsEmail reports whether s is a bare email address - "someone@example.com", not
// "Someone <someone@example.com>" and not free text that happens to contain an @.
func IsEmail(s string) bool {
	a, err := mail.ParseAddress(s)
	return err == nil && a.Name == "" && a.Address == s
}
