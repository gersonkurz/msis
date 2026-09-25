package main

import (
	"database/sql"
	_ "embed"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strings"
)

//go:embed queries.sql
var querySQL string

// Query is one entry of queries.sql: the documented SQL, its comment, and the parameters it
// needs. The file is the single source - the documentation is what runs, so a query cannot sit
// there looking correct while being broken.
type Query struct {
	Name    string
	Comment string
	SQL     string
	Params  []string
}

var queryHeader = regexp.MustCompile(`(?m)^-- name: ([a-z0-9-]+)\s*$`)

// paramPattern matches SQLite named parameters, and deliberately not a ':' inside a string
// literal - which is why it requires a word boundary before the colon.
var paramPattern = regexp.MustCompile(`(^|[\s(,])\:([a-zA-Z_][a-zA-Z0-9_]*)`)

// Queries parses queries.sql. Parsing rather than hand-listing: a query added to the file is
// available immediately, and - more to the point - the test that runs every query finds a new
// one without anyone remembering to register it.
func Queries() ([]Query, error) {
	locs := queryHeader.FindAllStringSubmatchIndex(querySQL, -1)
	if len(locs) == 0 {
		return nil, fmt.Errorf("queries.sql declares no queries")
	}

	var out []Query
	for i, loc := range locs {
		name := querySQL[loc[2]:loc[3]]
		end := len(querySQL)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		body := querySQL[loc[1]:end]

		// Split the leading comment block from the statement: the comment is the
		// documentation, and -query prints it on request.
		var comment, stmt []string
		inComment := true
		for _, line := range strings.Split(body, "\n") {
			trimmed := strings.TrimSpace(line)
			if inComment && (trimmed == "" || strings.HasPrefix(trimmed, "--")) {
				comment = append(comment, strings.TrimPrefix(strings.TrimPrefix(trimmed, "--"), " "))
				continue
			}
			inComment = false
			stmt = append(stmt, line)
		}

		sqlText := strings.TrimSpace(strings.Join(stmt, "\n"))
		if sqlText == "" {
			return nil, fmt.Errorf("query %q has no statement", name)
		}
		out = append(out, Query{
			Name:    name,
			Comment: strings.TrimSpace(strings.Join(comment, "\n")),
			SQL:     sqlText,
			Params:  paramsOf(sqlText),
		})
	}
	return out, nil
}

func paramsOf(sqlText string) []string {
	seen := map[string]bool{}
	for _, m := range paramPattern.FindAllStringSubmatch(sqlText, -1) {
		seen[m[2]] = true
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Run executes a query with the given arguments and returns the column names and rows.
//
// Every declared parameter must be supplied. A missing one is an error rather than an implicit
// empty string: `-query products-shipping` with no -arg would otherwise return nothing and look
// like the answer.
func Run(db *sql.DB, q Query, args map[string]string) ([]string, [][]string, error) {
	var missing []string
	for _, p := range q.Params {
		if _, ok := args[p]; !ok {
			missing = append(missing, ":"+p)
		}
	}
	if len(missing) > 0 {
		return nil, nil, fmt.Errorf("query %q needs %s", q.Name, strings.Join(missing, ", "))
	}
	for _, name := range slices.Sorted(maps.Keys(args)) { // the first unknown one is named: in a defined order (#73)
		if !contains(q.Params, name) {
			return nil, nil, fmt.Errorf("query %q takes no parameter %q; it takes %s",
				q.Name, name, strings.Join(q.Params, ", "))
		}
	}

	named := make([]any, 0, len(q.Params))
	for _, p := range q.Params {
		named = append(named, sql.Named(p, args[p]))
	}

	rows, err := db.Query(q.SQL, named...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}

	var out [][]string
	for rows.Next() {
		cells := make([]any, len(cols))
		for i := range cells {
			cells[i] = new(sql.NullString)
		}
		if err := rows.Scan(cells...); err != nil {
			return nil, nil, err
		}
		record := make([]string, len(cols))
		for i, c := range cells {
			if ns := c.(*sql.NullString); ns.Valid {
				record[i] = ns.String
			}
		}
		out = append(out, record)
	}
	return cols, out, rows.Err()
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
