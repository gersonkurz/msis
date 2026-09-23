// Command repin checks the prerequisite pins in internal/prereqcache against what Microsoft
// serves today, and produces the replacement entries when a pin has moved.
//
// Every download msis knows is pinned to a version-specific URL and a SHA-256 (decisions D5):
// msis never follows the mutable aka.ms / fwlink aliases at build time, so a newer
// redistributable reaches bundles only when a release re-pins. This is the routine that says
// WHEN that is due and does the evidence-gathering HOW (#49). It performs the same checks the
// original pins were taken with, so a re-pin is a reviewable diff of the table and nothing else:
//
//  1. resolve each pin's Alias and compare the address it serves today with the pinned URL;
//     also confirm the pinned URL itself still answers;
//  2. for a pin that has moved, download what the alias serves now, hash it, read its
//     Authenticode signature (must be Valid, signer CN=Microsoft Corporation) and its file
//     version, and for the Visual Studio CDN confirm that the hash segment in the URL path
//     equals the digest;
//  3. print the replacement table entry, in the table's own literal form, and the reminders
//     that go with it (the "Pinned installer" columns in docs/prerequisites.md, the date in the
//     DownloadURLs comment, the tests).
//
// Nothing is rewritten in place: the operator pastes the entries and reviews the diff, and
// TestEveryDownloadIsPinned holds the result to the table's rules.
//
// Usage: repin [-check] [-timeout D]
//
//	-check   resolve and compare only; download and verify nothing. Exit status says whether
//	         every pin is current, so it can run on a schedule.
//
// Exit status: 0 every pin is current; 1 at least one pin has moved, its pinned URL no longer
// answers, or a downloaded replacement failed verification; 2 a network or tool failure kept a
// pin from being checked at all.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gersonkurz/msis/internal/prereqcache"
)

func main() {
	check := flag.Bool("check", false, "resolve and compare only; download and verify nothing")
	timeout := flag.Duration("timeout", 10*time.Minute, "HTTP timeout per request (downloads are large)")
	flag.Parse()

	httpTimeout = *timeout
	findings := run(sortedPins(prereqcache.DownloadURLs), *check, os.Stdout)
	os.Exit(exitCode(findings))
}

// status classifies one pin against what its alias serves today.
type status string

const (
	statusCurrent     status = "current"     // the alias serves exactly the pinned URL
	statusMoved       status = "MOVED"       // the alias serves a different URL: a newer build
	statusPinnedGone  status = "PINNED GONE" // the alias still names the pinned URL, and it no longer answers
	statusUnreachable status = "UNREACHABLE" // the alias could not be resolved at all
)

// finding is the result for one pin.
type finding struct {
	Pin         prereqcache.PrerequisiteURL
	Status      status
	AliasTarget string // where the alias resolved to, when it did
	Err         error  // for UNREACHABLE and PINNED GONE

	// PinnedGone says the pinned URL itself no longer answers 200 - msis downloads from it,
	// not from the alias, so builds are failing today. It is a diagnostic on top of the
	// status, not a status of its own when the alias has moved: a retired old build is the
	// usual reason a pin has moved, and it must not stop the replacement from being verified
	// (round-1 review).
	PinnedGone bool
	PinnedErr  error

	// Filled for a MOVED pin when verification ran (not in -check mode).
	Verified   bool
	VerifyErr  error
	NewSHA256  string
	NewVersion string
	Signer     string
	SigStatus  string
	Size       int64
}

// Seams for tests: the three things that touch the network or the machine.
var (
	httpTimeout = 10 * time.Minute

	// resolveFinal follows redirects from url and returns the final address and status code.
	resolveFinal = func(url string) (final string, code int, err error) {
		client := &http.Client{Timeout: httpTimeout}
		resp, err := client.Head(url)
		if err == nil && resp.StatusCode >= 400 {
			// Some hosts refuse HEAD; a GET whose body is discarded is the same question.
			resp.Body.Close()
			resp, err = client.Get(url)
		}
		if err != nil {
			return "", 0, err
		}
		defer resp.Body.Close()
		return resp.Request.URL.String(), resp.StatusCode, nil
	}

	// download fetches url into dest and returns the byte count.
	download = func(url, dest string) (int64, error) {
		client := &http.Client{Timeout: httpTimeout}
		resp, err := client.Get(url)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return 0, fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
		}
		f, err := os.Create(dest)
		if err != nil {
			return 0, err
		}
		defer f.Close()
		return io.Copy(f, resp.Body)
	}

	// inspectSigned reads the Authenticode status, the signer's subject and the file version
	// of an executable, through PowerShell — the same evidence the original pins recorded.
	inspectSigned = func(path string) (sigStatus, signer, version string, err error) {
		script := fmt.Sprintf(
			`$p = '%s'; $s = Get-AuthenticodeSignature -LiteralPath $p; `+
				`$v = (Get-Item -LiteralPath $p).VersionInfo.FileVersion; `+
				`Write-Output ($s.Status.ToString() + '|' + $s.SignerCertificate.Subject + '|' + $v)`,
			strings.ReplaceAll(path, "'", "''"))
		out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).Output()
		if err != nil {
			return "", "", "", fmt.Errorf("powershell: %w", err)
		}
		parts := strings.SplitN(strings.TrimSpace(string(out)), "|", 3)
		if len(parts) != 3 {
			return "", "", "", fmt.Errorf("unexpected PowerShell output: %q", strings.TrimSpace(string(out)))
		}
		return parts[0], parts[1], strings.TrimSpace(parts[2]), nil
	}
)

// sortedPins flattens the table in a defined order: type, version, architecture.
func sortedPins(table map[string]map[string]map[string]prereqcache.PrerequisiteURL) []prereqcache.PrerequisiteURL {
	var pins []prereqcache.PrerequisiteURL
	for _, versions := range table {
		for _, arches := range versions {
			for _, p := range arches {
				pins = append(pins, p)
			}
		}
	}
	sort.Slice(pins, func(i, j int) bool {
		a, b := pins[i], pins[j]
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if a.Version != b.Version {
			return a.Version < b.Version
		}
		return a.Arch < b.Arch
	})
	return pins
}

// run checks every pin and writes the report; it returns the findings for the exit status.
func run(pins []prereqcache.PrerequisiteURL, checkOnly bool, w io.Writer) []finding {
	var findings []finding
	for _, pin := range pins {
		f := examine(pin)
		if f.Status == statusMoved && !checkOnly {
			verify(&f)
		}
		report(w, f)
		findings = append(findings, f)
	}
	summary(w, findings, checkOnly)
	return findings
}

// examine resolves the alias and the pinned URL and classifies the pin.
func examine(pin prereqcache.PrerequisiteURL) finding {
	f := finding{Pin: pin}
	target, code, err := resolveFinal(pin.Alias)
	if err != nil {
		f.Status, f.Err = statusUnreachable, err
		return f
	}
	f.AliasTarget = target
	// A non-200 at the END of the alias's redirect chain is about the target, not the alias:
	// if that target is the pinned URL, the pin is gone; if it is somewhere new, the new
	// address is dead and there is nothing to verify.
	if code != http.StatusOK && target != pin.URL {
		f.Status, f.Err = statusUnreachable, fmt.Errorf("the alias resolves to %s, which answered HTTP %d", target, code)
		return f
	}

	// The pinned URL has to keep answering: msis downloads from it, not from the alias. Its
	// absence is recorded, and only decides the status when the alias has NOT moved - when it
	// has, the move is the finding and the retired old URL is the explanation.
	if _, pcode, perr := resolveFinal(pin.URL); perr != nil || pcode != http.StatusOK {
		f.PinnedGone = true
		if perr != nil {
			f.PinnedErr = perr
		} else {
			f.PinnedErr = fmt.Errorf("pinned URL answered HTTP %d", pcode)
		}
	}
	switch {
	case target != pin.URL:
		f.Status = statusMoved
	case f.PinnedGone:
		f.Status, f.Err = statusPinnedGone, f.PinnedErr
	default:
		f.Status = statusCurrent
	}
	return f
}

// vsCDNHash matches the SHA-256 the Visual Studio CDN carries in its download paths.
var vsCDNHash = regexp.MustCompile(`/download/pr/[^/]+/([0-9A-Fa-f]{64})/`)

// verify downloads what the alias serves now and gathers the evidence a re-pin needs.
func verify(f *finding) {
	dir, err := os.MkdirTemp("", "repin-*")
	if err != nil {
		f.VerifyErr = err
		return
	}
	defer os.RemoveAll(dir)
	dest := filepath.Join(dir, f.Pin.FileName)

	f.Size, err = download(f.AliasTarget, dest)
	if err != nil {
		f.VerifyErr = fmt.Errorf("downloading %s: %w", f.AliasTarget, err)
		return
	}
	sum, err := fileSHA256(dest)
	if err != nil {
		f.VerifyErr = err
		return
	}
	f.NewSHA256 = sum

	f.SigStatus, f.Signer, f.NewVersion, err = inspectSigned(dest)
	if err != nil {
		f.VerifyErr = err
		return
	}

	var problems []string
	if f.SigStatus != "Valid" {
		problems = append(problems, fmt.Sprintf("Authenticode status is %q, want Valid", f.SigStatus))
	}
	if !strings.Contains(f.Signer, "CN=Microsoft Corporation") {
		problems = append(problems, fmt.Sprintf("signer is %q, want CN=Microsoft Corporation", f.Signer))
	}
	if f.NewVersion == "" {
		problems = append(problems, "the file carries no version resource")
	}
	if m := vsCDNHash.FindStringSubmatch(f.AliasTarget); m != nil && !strings.EqualFold(m[1], sum) {
		problems = append(problems, fmt.Sprintf("the CDN path carries %s but the bytes hash to %s", m[1], sum))
	}
	if len(problems) > 0 {
		f.VerifyErr = fmt.Errorf("%s", strings.Join(problems, "; "))
		return
	}
	f.Verified = true
}

func fileSHA256(path string) (string, error) {
	fh, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer fh.Close()
	h := sha256.New()
	if _, err := io.Copy(h, fh); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func label(p prereqcache.PrerequisiteURL) string {
	arch := p.Arch
	if arch == "" {
		arch = "(neutral)"
	}
	return fmt.Sprintf("%-8s %-6s %-9s", p.Type, p.Version, arch)
}

// report writes one pin's line, and for a moved pin the evidence and the replacement entry.
func report(w io.Writer, f finding) {
	switch f.Status {
	case statusCurrent:
		fmt.Fprintf(w, "%s  current      %s\n", label(f.Pin), f.Pin.FileVersion)
	case statusUnreachable:
		fmt.Fprintf(w, "%s  UNREACHABLE  %s: %v\n", label(f.Pin), f.Pin.Alias, f.Err)
	case statusPinnedGone:
		fmt.Fprintf(w, "%s  PINNED GONE  %s: %v\n", label(f.Pin), f.Pin.URL, f.Err)
		fmt.Fprintf(w, "%s  the alias still names that URL, so there is nothing newer to pin; builds using this prerequisite fail today\n", strings.Repeat(" ", 26))
	case statusMoved:
		fmt.Fprintf(w, "%s  MOVED        alias now serves %s\n", label(f.Pin), f.AliasTarget)
		if f.PinnedGone {
			fmt.Fprintf(w, "%s  the pinned URL no longer answers (%v): builds using this prerequisite fail today, so re-pin promptly\n", strings.Repeat(" ", 26), f.PinnedErr)
		}
		switch {
		case f.VerifyErr != nil:
			fmt.Fprintf(w, "%s  verification FAILED: %v\n", strings.Repeat(" ", 26), f.VerifyErr)
			fmt.Fprintf(w, "%s  do not pin this file\n", strings.Repeat(" ", 26))
		case f.Verified:
			fmt.Fprintf(w, "%s  %d bytes, sha256 %s\n", strings.Repeat(" ", 26), f.Size, f.NewSHA256)
			fmt.Fprintf(w, "%s  signature %s, %s; file version %s\n", strings.Repeat(" ", 26), f.SigStatus, f.Signer, f.NewVersion)
			fmt.Fprintf(w, "%s  replacement entry for DownloadURLs:\n\n%s\n", strings.Repeat(" ", 26), entryLiteral(f))
		}
	}
}

// entryLiteral prints the replacement in the table's own form, so it pastes over the old one.
func entryLiteral(f finding) string {
	p := f.Pin
	return fmt.Sprintf(
		"\t\t\t%q: {\n"+
			"\t\t\t\tType: %q, Version: %q, Arch: %q,\n"+
			"\t\t\t\tURL:         %q,\n"+
			"\t\t\t\tFileName:    %q,\n"+
			"\t\t\t\tSHA256:      %q,\n"+
			"\t\t\t\tFileVersion: %q,\n"+
			"\t\t\t\tAlias:       %q,\n"+
			"\t\t\t},",
		p.Arch, p.Type, p.Version, p.Arch, f.AliasTarget, p.FileName, f.NewSHA256, f.NewVersion, p.Alias)
}

func summary(w io.Writer, findings []finding, checkOnly bool) {
	var current, moved, gone, unreachable, failed int
	for _, f := range findings {
		switch f.Status {
		case statusCurrent:
			current++
		case statusMoved:
			moved++
			if !checkOnly && !f.Verified {
				failed++
			}
		case statusPinnedGone:
			gone++
		case statusUnreachable:
			unreachable++
		}
	}
	fmt.Fprintf(w, "\n%d pin(s): %d current, %d moved, %d pinned URL gone, %d unreachable\n",
		len(findings), current, moved, gone, unreachable)
	if moved > 0 && checkOnly {
		fmt.Fprintln(w, "run without -check to download and verify the moved ones and print their replacement entries")
	}
	if moved > 0 && !checkOnly && failed == 0 {
		fmt.Fprintln(w, "after pasting the entries: update the date in the DownloadURLs comment, the")
		fmt.Fprintln(w, "\"Pinned installer\" columns in docs/prerequisites.md, then run the Verify chain -")
		fmt.Fprintln(w, "TestEveryDownloadIsPinned holds the table to its rules")
	}
	if failed > 0 {
		fmt.Fprintf(w, "%d moved pin(s) FAILED verification; investigate before re-pinning\n", failed)
	}
}

// exitCode: 0 all current; 2 something could not be checked; 1 drift or a failed verification.
func exitCode(findings []finding) int {
	code := 0
	for _, f := range findings {
		switch f.Status {
		case statusUnreachable:
			return 2
		case statusMoved, statusPinnedGone:
			code = 1
		}
	}
	return code
}
