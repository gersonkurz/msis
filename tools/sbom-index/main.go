// Command sbom-index builds and queries a SQLite index over a directory of CycloneDX
// documents.
//
// It lives in its own module, outside cmd/msis, so that the SQLite driver stays out of the
// root module's requirement graph entirely - not merely out of the msis binary (#29 D11).
// TestTheRootModuleDoesNotRequireSQLite in the root module checks that directly.
//
// The corpus is authoritative and this is a projection of it (#29 D12). If the two disagree,
// the documents win: delete the database and build it again.
//
// This is a different module from the repository root, so `go run ./tools/sbom-index` from
// there does not resolve. Use `go -C tools/sbom-index run .`, or the just recipes, which also
// make the corpus path absolute - the tool runs from inside this directory.
//
//	just sbom-index                       # index bootstrap/dist
//	just sbom-query coverage
//	go -C tools/sbom-index run . -corpus /abs/path -query coverage
//	go -C tools/sbom-index run . -queries
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	_ "modernc.org/sqlite"
)

// defaultDB is where the index lands when the caller names only a corpus: beside the documents
// it describes, because a database whose corpus you cannot find is not much use.
const defaultDB = "sbom-index.db"

type argList map[string]string

func (a argList) String() string { return "" }

func (a argList) Set(v string) error {
	name, value, ok := strings.Cut(v, "=")
	if !ok {
		return fmt.Errorf("expected name=value, got %q", v)
	}
	a[name] = value
	return nil
}

func main() {
	var (
		corpus  = flag.String("corpus", "", "directory of *.cdx.json documents to index")
		dbPath  = flag.String("db", "", "the index file (default: <corpus>/"+defaultDB+")")
		query   = flag.String("query", "", "run a documented query by name")
		list    = flag.Bool("queries", false, "list the documented queries and exit")
		explain = flag.Bool("explain", false, "with -query: print what the query does and its SQL")
		args    = argList{}
	)
	flag.Var(args, "arg", "a query parameter, name=value; repeatable")
	flag.Parse()

	if err := rejectPositional(flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "sbom-index:", err)
		os.Exit(2)
	}

	if err := run(*corpus, *dbPath, *query, *list, *explain, args); err != nil {
		fmt.Fprintln(os.Stderr, "sbom-index:", err)
		os.Exit(1)
	}
}

// rejectPositional refuses any non-flag argument, and saying so matters more than it looks.
//
// Go's flag package stops parsing at the first positional, so a path that arrived split at a
// space - an unquoted "C:\My Projects\dist" - turned everything after it, INCLUDING -query,
// into arguments nobody read, and the command exited 0 having done nothing. Quoting the paths
// is the actual fix; this makes the next quoting mistake loud instead of silent.
func rejectPositional(rest []string) error {
	if len(rest) == 0 {
		return nil
	}
	return fmt.Errorf(
		"unexpected argument %q; this command takes only flags"+
			"\n  a path containing spaces must be quoted, or it arrives split and every flag"+
			"\n  after it is silently ignored", rest[0])
}

func run(corpus, dbPath, query string, list, explain bool, args argList) error {
	queries, err := Queries()
	if err != nil {
		return err
	}

	if list {
		return printQueryList(os.Stdout, queries)
	}

	if dbPath == "" {
		if corpus == "" {
			flag.Usage()
			return fmt.Errorf("give -corpus (to build) or -db (to query)")
		}
		dbPath = filepath.Join(corpus, defaultDB)
	}

	// A document that could not be indexed is reported, never skipped: in the terminal, in
	// the ingest_error table, and in the exit status, because a build script that ignores
	// stdout still has to notice. Held until the end rather than returned here: asking for a
	// query in the same command must not turn an incomplete build into a successful one,
	// which is exactly what returning only when `query == ""` did.
	var incomplete error
	if corpus != "" {
		report, err := Build(corpus, dbPath)
		if err != nil {
			return err
		}
		printReport(os.Stdout, dbPath, report)
		if len(report.Failures) > 0 {
			incomplete = fmt.Errorf("%d document(s) in the corpus were not indexed (see "+
				"above, and `-query rejected`)", len(report.Failures))
		}
	}

	if query == "" {
		return incomplete
	}

	var q *Query
	for i := range queries {
		if queries[i].Name == query {
			q = &queries[i]
		}
	}
	if q == nil {
		names := make([]string, len(queries))
		for i, c := range queries {
			names[i] = c.Name
		}
		sort.Strings(names)
		return fmt.Errorf("no query named %q; there is %s", query, strings.Join(names, ", "))
	}

	if explain {
		fmt.Printf("%s\n\n%s\n\n%s\n", q.Name, q.Comment, q.SQL)
		return incomplete
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	cols, rows, err := Run(db, *q, args)
	if err != nil {
		return err
	}
	printTable(os.Stdout, cols, rows)
	return incomplete
}

func printQueryList(w *os.File, queries []Query) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tPARAMETERS\tQUESTION")
	for _, q := range queries {
		params := strings.Join(q.Params, ", ")
		if params == "" {
			params = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", q.Name, params, firstLine(q.Comment))
	}
	return tw.Flush()
}

func printReport(w *os.File, dbPath string, r *Report) {
	fmt.Fprintf(w, "Wrote: %s\n", dbPath)
	fmt.Fprintf(w, "  %d document(s), %d component(s), %d product(s)\n",
		r.Documents, r.Components, r.Products)
	for _, f := range r.Failures {
		fmt.Fprintf(w, "  NOT INDEXED  %s: %s\n", f.Source, f.Problem)
	}
}

func printTable(w *os.File, cols []string, rows [][]string) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(upper(cols), "\t"))
	for _, r := range rows {
		fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	tw.Flush()
	if len(rows) == 0 {
		fmt.Fprintln(w, "(no rows)")
	}
}

func upper(s []string) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[i] = strings.ToUpper(v)
	}
	return out
}
