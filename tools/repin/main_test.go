package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gersonkurz/msis/internal/prereqcache"
)

// cdn is a stand-in for Microsoft: aliases that redirect, and content-addressed download paths
// in the Visual Studio CDN's shape (/download/pr/<guid>/<SHA-256>/<file>), so the hash-segment
// check runs against something real.
type cdn struct {
	srv       *httptest.Server
	files     map[string][]byte // path -> bytes
	redirects map[string]string // alias path -> target path
	downloads int32
}

func newCDN(t *testing.T) *cdn {
	t.Helper()
	c := &cdn{files: map[string][]byte{}, redirects: map[string]string{}}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if to, ok := c.redirects[r.URL.Path]; ok {
			http.Redirect(w, r, to, http.StatusFound)
			return
		}
		body, ok := c.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodGet {
			atomic.AddInt32(&c.downloads, 1)
		}
		w.Write(body)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

// file publishes body under a CDN-shaped path whose hash segment is the real digest (or a
// wrong one, when the test wants the check to fail) and returns the absolute URL.
func (c *cdn) file(guid string, body []byte, hashSegment string) string {
	if hashSegment == "" {
		sum := sha256.Sum256(body)
		hashSegment = strings.ToUpper(hex.EncodeToString(sum[:]))
	}
	path := fmt.Sprintf("/download/pr/%s/%s/VC_redist.x64.exe", guid, hashSegment)
	c.files[path] = body
	return c.srv.URL + path
}

func (c *cdn) alias(name, targetURL string) string {
	path := "/alias/" + name
	c.redirects[path] = strings.TrimPrefix(targetURL, c.srv.URL)
	return c.srv.URL + path
}

func sha(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// stubSignature stands in for PowerShell: every downloaded file is "Valid, Microsoft, 14.99".
func stubSignature(t *testing.T, status, signer, version string) {
	t.Helper()
	orig := inspectSigned
	inspectSigned = func(path string) (string, string, string, error) { return status, signer, version, nil }
	t.Cleanup(func() { inspectSigned = orig })
}

func pin(alias, url string) prereqcache.PrerequisiteURL {
	return prereqcache.PrerequisiteURL{
		Type: "vcredist", Version: "2022", Arch: "x64",
		URL: url, FileName: "vc_redist.x64.exe",
		SHA256: "0000000000000000000000000000000000000000000000000000000000000000", FileVersion: "14.0.0.0",
		Alias: alias,
	}
}

// An alias that still serves the pinned URL is current, and nothing is downloaded.
func TestACurrentPinIsReportedCurrent(t *testing.T) {
	c := newCDN(t)
	stubSignature(t, "Valid", "CN=Microsoft Corporation", "14.0.0.0")
	pinned := c.file("g1", []byte("the pinned bytes"), "")
	p := pin(c.alias("vs17", pinned), pinned)

	var out bytes.Buffer
	findings := run([]prereqcache.PrerequisiteURL{p}, false, &out)
	if findings[0].Status != statusCurrent {
		t.Fatalf("status = %s, want current: %+v", findings[0].Status, findings[0])
	}
	if exitCode(findings) != 0 {
		t.Errorf("exit code %d, want 0 for an all-current table", exitCode(findings))
	}
	if atomic.LoadInt32(&c.downloads) != 0 {
		t.Errorf("%d download(s) for a current pin; nothing should be fetched", c.downloads)
	}
	if !strings.Contains(out.String(), "current") || !strings.Contains(out.String(), "1 current") {
		t.Errorf("report:\n%s", out.String())
	}
}

// An alias that now serves a different URL is a move: the new file is downloaded, hashed,
// signature-checked, its CDN hash segment confirmed, and the replacement entry printed in the
// table's literal form.
func TestAMovedPinIsVerifiedAndAReplacementEntryPrinted(t *testing.T) {
	c := newCDN(t)
	stubSignature(t, "Valid", "CN=Microsoft Corporation, O=Microsoft Corporation", "14.99.1.0")
	old := c.file("g1", []byte("old build"), "")
	newer := []byte("new build")
	newURL := c.file("g2", newer, "")
	p := pin(c.alias("vs17", newURL), old)

	var out bytes.Buffer
	findings := run([]prereqcache.PrerequisiteURL{p}, false, &out)
	f := findings[0]
	if f.Status != statusMoved || !f.Verified || f.VerifyErr != nil {
		t.Fatalf("status=%s verified=%v err=%v", f.Status, f.Verified, f.VerifyErr)
	}
	if f.NewSHA256 != sha(newer) || f.NewVersion != "14.99.1.0" || f.AliasTarget != newURL {
		t.Errorf("finding = %+v", f)
	}
	if exitCode(findings) != 1 {
		t.Errorf("exit code %d, want 1 when a pin has moved", exitCode(findings))
	}
	for _, want := range []string{
		"MOVED",
		`URL:         "` + newURL + `"`,
		`SHA256:      "` + sha(newer) + `"`,
		`FileVersion: "14.99.1.0"`,
		`Alias:       "` + p.Alias + `"`,
		`FileName:    "vc_redist.x64.exe"`,
		"Pinned installer",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}
}

// A moved file whose CDN hash segment does not match its bytes must not be offered as a pin.
func TestACDNHashMismatchFailsVerification(t *testing.T) {
	c := newCDN(t)
	stubSignature(t, "Valid", "CN=Microsoft Corporation", "14.99.1.0")
	old := c.file("g1", []byte("old build"), "")
	wrongHash := strings.Repeat("A", 64)
	newURL := c.file("g2", []byte("bytes that do not hash to A*64"), wrongHash)
	p := pin(c.alias("vs17", newURL), old)

	var out bytes.Buffer
	findings := run([]prereqcache.PrerequisiteURL{p}, false, &out)
	f := findings[0]
	if f.Status != statusMoved || f.Verified || f.VerifyErr == nil {
		t.Fatalf("a hash-segment mismatch was accepted: %+v", f)
	}
	if !strings.Contains(f.VerifyErr.Error(), "CDN path carries") {
		t.Errorf("error = %v, want the hash-segment mismatch named", f.VerifyErr)
	}
	if strings.Contains(out.String(), "replacement entry") || !strings.Contains(out.String(), "do not pin") {
		t.Errorf("a failed verification must not print a replacement entry:\n%s", out.String())
	}
}

// A signature that is not Microsoft's, or not valid, fails verification the same way.
func TestAWrongSignerFailsVerification(t *testing.T) {
	c := newCDN(t)
	stubSignature(t, "Valid", "CN=Someone Else", "1.0.0.0")
	old := c.file("g1", []byte("old"), "")
	p := pin(c.alias("vs17", c.file("g2", []byte("new"), "")), old)

	findings := run([]prereqcache.PrerequisiteURL{p}, false, &bytes.Buffer{})
	if f := findings[0]; f.Verified || f.VerifyErr == nil || !strings.Contains(f.VerifyErr.Error(), "signer") {
		t.Errorf("%+v", f)
	}
}

// -check resolves and compares only: a moved pin is reported, nothing is downloaded, and the
// exit status still says "drift", which is what a scheduled run needs.
func TestCheckModeReportsAMoveWithoutDownloading(t *testing.T) {
	c := newCDN(t)
	old := c.file("g1", []byte("old"), "")
	p := pin(c.alias("vs17", c.file("g2", []byte("new"), "")), old)

	var out bytes.Buffer
	findings := run([]prereqcache.PrerequisiteURL{p}, true, &out)
	if findings[0].Status != statusMoved || findings[0].Verified {
		t.Fatalf("%+v", findings[0])
	}
	if atomic.LoadInt32(&c.downloads) != 0 {
		t.Errorf("%d download(s) in -check mode", c.downloads)
	}
	if exitCode(findings) != 1 {
		t.Errorf("exit code %d, want 1", exitCode(findings))
	}
	if !strings.Contains(out.String(), "run without -check") {
		t.Errorf("report should say how to proceed:\n%s", out.String())
	}
}

// The usual reason a pin has moved is that Microsoft retired the old build: the pinned URL is
// gone AND the alias serves a newer one. That must not stop the replacement from being verified
// (round-1 review found it did): the move is verified and its entry printed, and the report says
// the pinned URL no longer answers, because builds are failing today.
func TestARetiredPinWithAMovedAliasIsStillVerified(t *testing.T) {
	c := newCDN(t)
	stubSignature(t, "Valid", "CN=Microsoft Corporation", "14.99.2.0")
	newer := []byte("the build that replaced the retired one")
	newURL := c.file("g2", newer, "")
	p := pin(c.alias("vs17", newURL), c.srv.URL+"/download/pr/g1/"+strings.Repeat("B", 64)+"/VC_redist.x64.exe")

	var out bytes.Buffer
	findings := run([]prereqcache.PrerequisiteURL{p}, false, &out)
	f := findings[0]
	if f.Status != statusMoved || !f.PinnedGone || !f.Verified || f.VerifyErr != nil {
		t.Fatalf("status=%s pinnedGone=%v verified=%v err=%v", f.Status, f.PinnedGone, f.Verified, f.VerifyErr)
	}
	if f.NewSHA256 != sha(newer) {
		t.Errorf("digest %s, want %s", f.NewSHA256, sha(newer))
	}
	for _, want := range []string{"MOVED", "pinned URL no longer answers", "HTTP 404", "replacement entry", `URL:         "` + newURL + `"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}
	if exitCode(findings) != 1 {
		t.Errorf("exit %d, want 1", exitCode(findings))
	}
}

// A pinned URL that no longer answers while the alias STILL names it: nothing newer exists to
// pin, and the report must say so rather than pretend there is a replacement.
func TestAPinnedURLThatIsGoneWithoutAMoveIsReported(t *testing.T) {
	c := newCDN(t)
	dead := c.srv.URL + "/download/pr/g1/" + strings.Repeat("B", 64) + "/VC_redist.x64.exe"
	p := pin(c.alias("vs17", dead), dead)

	var out bytes.Buffer
	findings := run([]prereqcache.PrerequisiteURL{p}, false, &out)
	if findings[0].Status != statusPinnedGone || findings[0].Verified {
		t.Fatalf("status = %s, want PINNED GONE: %+v", findings[0].Status, findings[0])
	}
	if exitCode(findings) != 1 || !strings.Contains(out.String(), "PINNED GONE") || !strings.Contains(out.String(), "nothing newer to pin") {
		t.Errorf("exit %d, report:\n%s", exitCode(findings), out.String())
	}
	if atomic.LoadInt32(&c.downloads) != 0 {
		t.Errorf("%d download(s); there is nothing to fetch", c.downloads)
	}
}

// An alias that cannot be resolved is not drift and not "current": it is a failed check, and
// the exit status says so distinctly.
func TestAnUnreachableAliasIsAFailedCheck(t *testing.T) {
	c := newCDN(t)
	pinned := c.file("g1", []byte("x"), "")
	p := pin("http://127.0.0.1:9/alias/vs17", pinned)

	findings := run([]prereqcache.PrerequisiteURL{p}, true, &bytes.Buffer{})
	if findings[0].Status != statusUnreachable || exitCode(findings) != 2 {
		t.Errorf("status=%s exit=%d", findings[0].Status, exitCode(findings))
	}
}

// The real table flattens in a defined order, so two runs print the same report.
func TestPinsAreCheckedInADefinedOrder(t *testing.T) {
	pins := sortedPins(prereqcache.DownloadURLs)
	if len(pins) != 8 {
		t.Fatalf("%d pins, want 8", len(pins))
	}
	for i := 1; i < len(pins); i++ {
		a, b := pins[i-1], pins[i]
		if a.Type > b.Type || (a.Type == b.Type && a.Version > b.Version) ||
			(a.Type == b.Type && a.Version == b.Version && a.Arch > b.Arch) {
			t.Errorf("out of order: %s %s %s before %s %s %s", a.Type, a.Version, a.Arch, b.Type, b.Version, b.Arch)
		}
	}
	// Every pin has the alias the tool needs (TestEveryDownloadIsPinned checks the shape; this
	// checks the tool sees it).
	for _, p := range pins {
		if p.Alias == "" {
			t.Errorf("%s %s %s: no alias to resolve", p.Type, p.Version, p.Arch)
		}
	}
}
