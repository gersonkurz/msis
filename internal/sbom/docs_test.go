package sbom

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// docs/sbom.md documents the msis:* vocabulary (#44). A table of property names in prose is
// exactly the kind of documentation that rots: someone adds a property, the emitter and the
// index learn about it, and the document quietly describes a smaller feature than the one that
// ships - which is worse than no table, because a reader has no way to tell.
//
// So the vocabulary is read out of the SOURCE, not listed here, and every name in it has to
// appear in the document. The same coupling docs/msis.xsd has with the parser.
func TestTheDocumentedVocabularyIsTheEmittedVocabulary(t *testing.T) {
	emitted := vocabularyOf(t, "document.go")
	if len(emitted) < 20 {
		t.Fatalf("%d msis: properties found in document.go; the extraction is broken, not the "+
			"documentation", len(emitted))
	}

	doc, err := os.ReadFile("../../docs/sbom.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(doc)

	var missing []string
	for _, name := range emitted {
		if !strings.Contains(text, name) {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("docs/sbom.md does not mention %d emitted propert(ies): %s\n"+
			"A reader cannot tell a property the document forgot from one that does not exist.",
			len(missing), strings.Join(missing, ", "))
	}
}

// vocabularyOf collects every msis: property name declared as a string constant in one file.
func vocabularyOf(t *testing.T, file string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil || !strings.HasPrefix(value, "msis:") {
			return true
		}
		seen[value] = true
		return true
	})

	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// The same argument for the "no link" reasons (#44 asks for them explicitly). A bundle records
// why it could not link to a chained installer's document, and the reasons are the actionable
// part: "no link" for thirteen different causes is not. The document tabulates them, and the
// table was incomplete the first time it was written - so the count is pinned to the source.
//
// Counted rather than matched, because most of the reasons are format strings beginning with a
// verb the caller supplies; there is no literal to compare. What this catches is the failure
// that actually happens: a reason added to the emitter and not to the document.
func TestEveryNoLinkReasonIsDocumented(t *testing.T) {
	code, err := os.ReadFile("bundle.go")
	if err != nil {
		t.Fatal(err)
	}
	// Each distinct `no link: …` the emitter can produce. The bare literal `"no link: "` is
	// not one of them: it appears twice, once as the prefix wrapping a resolver error and once
	// where BOMLink strips it off again.
	text := string(code)
	emitted := strings.Count(text, `"no link: `) - strings.Count(text, `"no link: "`)

	// Plus the ones childArtifactPath returns, which reach the document through that wrapper.
	// Scoped to that function: `return "", …` appears elsewhere in the file for other reasons.
	body := text[strings.Index(text, "func childArtifactPath"):]
	body = body[:strings.Index(body, "\n}")]
	emitted += strings.Count(body, `return "", `)

	doc, err := os.ReadFile("../../docs/sbom.md")
	if err != nil {
		t.Fatal(err)
	}
	documented := noLinkRows(string(doc))

	if documented != emitted {
		t.Errorf("the emitter can record %d reasons for not linking; docs/sbom.md tabulates %d.\n"+
			"A reason that is emitted and undocumented is the one a reader will meet and be "+
			"unable to act on.", emitted, documented)
	}
}

// noLinkRows counts the body rows of the reason table, found by its header rather than by
// position so that adding a section above it does not silently change the answer.
func noLinkRows(doc string) int {
	const header = "| recorded reason | what it means |"
	i := strings.Index(doc, header)
	if i < 0 {
		return -1
	}
	rows := 0
	for _, line := range strings.Split(doc[i:], "\n") {
		switch {
		case strings.HasPrefix(line, "|---"), line == header:
		case strings.HasPrefix(line, "|"):
			rows++
		default:
			return rows
		}
	}
	return rows
}
