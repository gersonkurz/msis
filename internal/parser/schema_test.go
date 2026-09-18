package parser

import (
	"encoding/xml"
	"go/ast"
	goparser "go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
	"testing"
)

// docs/msis.xsd is what CLAUDE.md calls "the authoritative .msis schema", so it is a claim
// about this parser. It drifted: <create-folder> and <remove-on-uninstall> were accepted
// everywhere the other item elements are, and appeared in neither the schema nor the tutorial
// (issue #22). A user reading the schema to learn what msis can do got an incomplete answer,
// and one validating a working .msis against it got a false error.
//
// This relates the two mechanically instead of by memory. The parser side is read from the
// dispatch switches themselves via go/ast, not from a list kept next to them, because a list
// next to the code is exactly what fails to get updated - the defect this closes, and the same
// shape as #18, #19 and #20.

// elementSwitchContexts maps the receiver of an UnmarshalXML method to the element whose
// children that method dispatches on.
var elementSwitchContexts = map[string]string{
	"xmlSetup":   "setup",
	"xmlFeature": "feature",
	"xmlBundle":  "bundle",
}

func TestSchemaDocumentsEveryElementTheParserAccepts(t *testing.T) {
	accepted := parserElements(t)
	declared := schemaElements(t)

	for _, context := range sortedKeys(accepted) {
		for _, name := range accepted[context] {
			if !declared[context][name] {
				t.Errorf("the parser accepts <%s> inside <%s>, but docs/msis.xsd does not declare it there - "+
					"a valid .msis fails schema validation, and a user reading the schema cannot find the element",
					name, context)
			}
		}
	}
}

// TestSchemaDeclaresNothingTheParserRejects is the other direction: an element in the schema
// that the parser refuses is a documented feature that does not exist.
func TestSchemaDeclaresNothingTheParserRejects(t *testing.T) {
	accepted := parserElements(t)
	declared := schemaElements(t)

	for _, context := range sortedKeys(declared) {
		acceptedHere := make(map[string]bool)
		for _, name := range accepted[context] {
			acceptedHere[name] = true
		}
		for name := range declared[context] {
			if !acceptedHere[name] {
				t.Errorf("docs/msis.xsd declares <%s> inside <%s>, but the parser rejects it there", name, context)
			}
		}
	}
}

// parserElements reads the case labels of every `switch t.Name.Local` in parser.go, keyed by
// the element being parsed. Reading the switches means a newly handled element is picked up
// with no second place to update.
func parserElements(t *testing.T) map[string][]string {
	t.Helper()

	fset := token.NewFileSet()
	file, err := goparser.ParseFile(fset, "parser.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing parser.go: %v", err)
	}

	found := make(map[string][]string)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 {
			continue
		}
		star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		ident, ok := star.X.(*ast.Ident)
		if !ok {
			continue
		}
		context, ok := elementSwitchContexts[ident.Name]
		if !ok {
			continue
		}

		ast.Inspect(fn, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok || !isNameLocal(sw.Tag) {
				return true
			}
			for _, stmt := range sw.Body.List {
				clause, ok := stmt.(*ast.CaseClause)
				if !ok {
					continue
				}
				for _, expr := range clause.List {
					lit, ok := expr.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					name, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatalf("unquoting case label %s: %v", lit.Value, err)
					}
					found[context] = append(found[context], name)
				}
			}
			return true
		})
	}

	if len(found) != len(elementSwitchContexts) {
		t.Fatalf("expected an element switch for each of %v, found %v - "+
			"the parser was restructured and this test no longer sees its dispatch",
			sortedKeys(elementSwitchContexts), sortedKeys(found))
	}
	return found
}

// isNameLocal reports whether an expression is `t.Name.Local`, the tag of the element dispatch
// switches. The receiver name matters: the same methods also switch on `attr.Name.Local` to
// read attributes, and those case labels are attribute names, not elements. If the parser is
// restructured so the element switch is over something else, parserElements fails on the count
// check rather than silently comparing an empty set.
func isNameLocal(expr ast.Expr) bool {
	outer, ok := expr.(*ast.SelectorExpr)
	if !ok || outer.Sel.Name != "Local" {
		return false
	}
	inner, ok := outer.X.(*ast.SelectorExpr)
	if !ok || inner.Sel.Name != "Name" {
		return false
	}
	base, ok := inner.X.(*ast.Ident)
	return ok && base.Name == "t"
}

// schemaElements returns the child elements docs/msis.xsd permits inside each of the
// containers the parser dispatches for.
func schemaElements(t *testing.T) map[string]map[string]bool {
	t.Helper()

	data, err := os.ReadFile("../../docs/msis.xsd")
	if err != nil {
		t.Fatalf("reading schema: %v", err)
	}
	var schema xsdSchema
	if err := xml.Unmarshal(data, &schema); err != nil {
		t.Fatalf("parsing schema: %v", err)
	}

	// The container for "feature" and "bundle" is a named complexType; "setup" is a top-level
	// element with its complexType inline.
	byType := map[string]string{"feature": "FeatureType", "bundle": "BundleType"}
	out := make(map[string]map[string]bool)

	for context, typeName := range byType {
		for _, ct := range schema.ComplexTypes {
			if ct.Name == typeName {
				out[context] = namesOf(ct.Sequence.Choice.Elements)
			}
		}
		if out[context] == nil {
			t.Fatalf("docs/msis.xsd has no complexType %q for <%s>", typeName, context)
		}
	}

	for _, el := range schema.Elements {
		if el.Name == "setup" && el.ComplexType != nil {
			out["setup"] = namesOf(el.ComplexType.Sequence.Choice.Elements)
		}
	}
	if out["setup"] == nil {
		t.Fatal("docs/msis.xsd has no <setup> element declaration with an inline complexType")
	}
	return out
}

type xsdSchema struct {
	ComplexTypes []xsdComplexType `xml:"complexType"`
	Elements     []xsdElement     `xml:"element"`
}

type xsdComplexType struct {
	Name     string      `xml:"name,attr"`
	Sequence xsdSequence `xml:"sequence"`
}

type xsdSequence struct {
	Choice xsdChoice `xml:"choice"`
}

type xsdChoice struct {
	Elements []xsdElement `xml:"element"`
}

type xsdElement struct {
	Name        string          `xml:"name,attr"`
	ComplexType *xsdComplexType `xml:"complexType"`
}

func namesOf(elements []xsdElement) map[string]bool {
	names := make(map[string]bool, len(elements))
	for _, el := range elements {
		names[el.Name] = true
	}
	return names
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
