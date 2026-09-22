package variables

import (
	"sort"

	"github.com/aymerick/raymond"
	"github.com/aymerick/raymond/ast"
	"github.com/aymerick/raymond/parser"
)

// ResolveChecked is Resolve with one addition: it reports the variables the template referred
// to that this dictionary does not define, instead of letting the engine render them as "".
//
// When undefined is non-empty, out is "" and the caller decides what to do - the registry
// writes the value as authored and warns (decisions D8). err is a parse or render error as
// from Resolve.
//
// Which references count is decided the way the dependency resolver decides it (see
// resolver.resolve): by what the engine ACTUALLY EVALUATES, not by reading the text. The
// parsed template supplies the candidates - every path that could be a variable, which
// excludes helper names such as `if` and syntax such as `else` - and each candidate the
// dictionary lacks is given a sentinel function in the render context. Whatever the render
// reaches is undefined-and-used; a reference inside a branch the render does not take is not
// reported. That covers every reference form the engine accepts (whitespace control `{{~X~}}`,
// hyphenated names) without a second grammar for them, which is what a regexp would have been
// and where it went wrong.
//
// A name used in a condition counts: `{{#if FOO}}` with FOO undefined is reported rather than
// treated as false. Handlebars would take the else branch silently, and silence is the thing
// this function exists to remove; a variable that is meant to be optional is defined as empty.
func (d Dictionary) ResolveChecked(s string) (out string, undefined []string, err error) {
	src := keepBackslashBeforeReference(s)
	program, err := parser.Parse(src)
	if err != nil {
		return "", nil, err
	}

	ctx := d.verbatimContext()
	reached := map[string]bool{}
	for _, name := range candidateNames(program) {
		if _, ok := d[name]; ok {
			continue
		}
		name := name
		ctx[name] = func() any {
			reached[name] = true
			return raymond.SafeString("")
		}
	}

	tpl, err := raymond.Parse(src)
	if err != nil {
		return "", nil, err
	}
	out, err = tpl.Exec(ctx)
	if len(reached) > 0 {
		// The output was rendered against sentinels and is discarded, as is any error the
		// sentinels provoked: the only thing wanted from this render is which names it reached.
		for name := range reached {
			undefined = append(undefined, name)
		}
		sort.Strings(undefined)
		return "", undefined, nil
	}
	return out, nil, err
}

// candidateNames lists every root name a template could look up as a variable, in a defined
// order. A path standing alone in a mustache or block is a variable; one followed by params or
// a hash names a helper and is skipped, while its params are variables. Data references
// (`@index`) and `this` are not variables of ours.
func candidateNames(program *ast.Program) []string {
	c := &nameCollector{names: map[string]bool{}}
	c.program(program)
	names := make([]string, 0, len(c.names))
	for n := range c.names {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

type nameCollector struct {
	names map[string]bool
}

func (c *nameCollector) program(p *ast.Program) {
	if p == nil {
		return
	}
	for _, n := range p.Body {
		c.node(n)
	}
}

func (c *nameCollector) node(n ast.Node) {
	switch v := n.(type) {
	case *ast.MustacheStatement:
		c.expression(v.Expression)
	case *ast.BlockStatement:
		c.expression(v.Expression)
		c.program(v.Program)
		c.program(v.Inverse)
	case *ast.SubExpression:
		c.expression(v.Expression)
	}
	// Content, comments and partials reference no variables.
}

func (c *nameCollector) expression(e *ast.Expression) {
	if e == nil {
		return
	}
	if len(e.Params) == 0 && e.Hash == nil {
		c.path(e.Path) // alone: a variable reference
	}
	for _, p := range e.Params {
		c.param(p)
	}
	if e.Hash != nil {
		for _, pair := range e.Hash.Pairs {
			c.param(pair.Val)
		}
	}
}

func (c *nameCollector) param(n ast.Node) {
	switch v := n.(type) {
	case *ast.PathExpression:
		c.path(v)
	case *ast.SubExpression:
		c.expression(v.Expression)
	}
}

func (c *nameCollector) path(n ast.Node) {
	p, ok := n.(*ast.PathExpression)
	if !ok || p.Data || len(p.Parts) == 0 {
		return
	}
	root := lookupKey(p.Parts[0])
	if root == "this" || root == "." {
		return
	}
	c.names[root] = true
}

// lookupKey is the key the evaluator looks a path segment up under. The parser keeps a
// literal segment's brackets - `{{[PRODUCT VERSION]}}` parses as the part "[PRODUCT VERSION]" -
// and raymond's evalPath strips exactly one enclosing pair before the lookup. A candidate has
// to be normalised the same way, or its sentinel sits under a key the render never asks for
// and an undefined bracketed reference renders as "" unreported (found in review of #46).
func lookupKey(part string) string {
	if len(part) >= 2 && part[0] == '[' && part[len(part)-1] == ']' {
		return part[1 : len(part)-1]
	}
	return part
}
