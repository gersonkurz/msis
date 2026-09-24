package parser

import (
	"fmt"
	"regexp"
	"strings"
)

// SPDX licence expressions (SPDX 2.3 Annex D), checked for their SYNTAX: balanced grouping,
// AND/OR/WITH in the right places, "+" only as the one suffix of a licence id. Deliberately not
// checked: whether an id is on the SPDX licence list - that list grows, and a copy here would
// refuse a valid id as soon as it went stale (#64).
//
//	expression = or-expr
//	or-expr    = and-expr *( "OR" and-expr )
//	and-expr   = with-expr *( "AND" with-expr )
//	with-expr  = primary [ "WITH" exception-id ]
//	primary    = "(" expression ")" / license-ref / license-id [ "+" ]
//
// Parentheses delimit tokens as well as whitespace does, so "(MIT)OR(Apache-2.0)" is valid. The
// operators are upper case: "and" is not an operator, and a lower-case one is refused.

var (
	spdxID      = regexp.MustCompile(`^[A-Za-z0-9.-]+\+?$`)
	spdxRef     = regexp.MustCompile(`^(DocumentRef-[A-Za-z0-9.-]+:)?LicenseRef-[A-Za-z0-9.-]+$`)
	spdxExcept  = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)
	spdxWordRun = regexp.MustCompile(`^[A-Za-z0-9.:+-]+`)
)

// validSPDX reports whether expr is a syntactically valid SPDX licence expression.
func validSPDX(expr string) error {
	toks, err := spdxTokens(expr)
	if err != nil {
		return err
	}
	p := &spdxParser{toks: toks}
	if err := p.or(); err != nil {
		return err
	}
	if p.i != len(p.toks) {
		return fmt.Errorf("unexpected %q", p.toks[p.i])
	}
	return nil
}

func spdxTokens(s string) ([]string, error) {
	var out []string
	for i := 0; i < len(s); {
		switch c := s[i]; {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(' || c == ')':
			out = append(out, string(c))
			i++
		default:
			w := spdxWordRun.FindString(s[i:])
			if w == "" {
				return nil, fmt.Errorf("unexpected character %q", string(c))
			}
			out = append(out, w)
			i += len(w)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty expression")
	}
	return out, nil
}

type spdxParser struct {
	toks []string
	i    int
}

func (p *spdxParser) peek() string {
	if p.i < len(p.toks) {
		return p.toks[p.i]
	}
	return ""
}

func (p *spdxParser) or() error {
	if err := p.and(); err != nil {
		return err
	}
	for p.peek() == "OR" {
		p.i++
		if err := p.and(); err != nil {
			return err
		}
	}
	return nil
}

func (p *spdxParser) and() error {
	if err := p.with(); err != nil {
		return err
	}
	for p.peek() == "AND" {
		p.i++
		if err := p.with(); err != nil {
			return err
		}
	}
	return nil
}

// with parses a with-expr. WITH attaches an exception to ONE licence - Annex D's simple
// expression - so a parenthesised compound on its left is refused: "(MIT OR Apache-2.0) WITH X"
// and "(A WITH X) WITH Y" are not expressions (#64's review). A group may still be combined with
// AND and OR, which is where it belongs.
func (p *spdxParser) with() error {
	grouped := p.peek() == "("
	if err := p.primary(); err != nil {
		return err
	}
	if p.peek() == "WITH" && grouped {
		return fmt.Errorf("WITH applies to a single licence id, not to a parenthesised expression")
	}
	if p.peek() == "WITH" {
		p.i++
		ex := p.peek()
		if !spdxExcept.MatchString(ex) || isOperator(ex) {
			return fmt.Errorf("WITH needs an exception id, got %q", ex)
		}
		p.i++
	}
	return nil
}

func (p *spdxParser) primary() error {
	t := p.peek()
	switch {
	case t == "(":
		p.i++
		if err := p.or(); err != nil {
			return err
		}
		if p.peek() != ")" {
			return fmt.Errorf("unbalanced parenthesis")
		}
		p.i++
		return nil
	case t == "" || t == ")" || isOperator(t):
		return fmt.Errorf("expected a licence id, got %q", t)
	case strings.HasPrefix(t, "LicenseRef-") || strings.HasPrefix(t, "DocumentRef-"):
		// A reserved prefix makes it a reference, which has its own form.
		if spdxRef.MatchString(t) {
			p.i++
			return nil
		}
		return fmt.Errorf("%q is not a LicenseRef-/DocumentRef- reference", t)
	case spdxID.MatchString(t):
		p.i++
		return nil
	}
	return fmt.Errorf("%q is not a licence id", t)
}

func isOperator(t string) bool { return t == "AND" || t == "OR" || t == "WITH" }
