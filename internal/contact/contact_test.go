package contact

import "testing"

func TestIsURL(t *testing.T) {
	for s, want := range map[string]bool{
		"https://ng-branch-technology.com":      true,
		"http://example.com/support":            true,
		"example.com":                           false, // no scheme: not absolute
		"ftp://example.com":                     false,
		"https://":                              false,
		"mailto:someone@example.com":            false,
		"https://example.com/with space":        false,
		"Visit https://example.com for support": false,
	} {
		if got := IsURL(s); got != want {
			t.Errorf("IsURL(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestIsEmail(t *testing.T) {
	for s, want := range map[string]bool{
		"support@example.com":             true,
		"Support <support@example.com>":   false, // a display name is not a bare address
		"call us, or support@example.com": false,
		"example.com":                     false,
		"":                                false,
	} {
		if got := IsEmail(s); got != want {
			t.Errorf("IsEmail(%q) = %v, want %v", s, got, want)
		}
	}
}
