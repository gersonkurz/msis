package main

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

// Dump renders the whole database as ordered text.
//
// "Built, deleted, rebuilt - identical contents" (#35) cannot be checked by comparing the two
// files: SQLite's page layout, freelist and internal counters legitimately differ between two
// runs that produce the same rows. What has to be identical is the CONTENT, so the comparison
// is made on this.
//
// Tables are discovered from sqlite_master rather than listed here, so a table added to
// schema.sql is covered by the rebuild check without anyone remembering to add it - the
// failure mode being a new table that quietly is not compared. Rows are ordered by every
// column, because the check must not depend on SQLite's scan order.
func Dump(db *sql.DB) (string, error) {
	tables, err := queryStrings(db, `
		SELECT name FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%'
		ORDER BY name`)
	if err != nil {
		return "", err
	}
	if len(tables) == 0 {
		return "", fmt.Errorf("the database has no tables, so a comparison would prove nothing")
	}

	var b strings.Builder
	for _, table := range tables {
		cols, err := queryStrings(db, `SELECT name FROM pragma_table_info(?) ORDER BY cid`, table)
		if err != nil {
			return "", err
		}
		if len(cols) == 0 {
			return "", fmt.Errorf("table %s has no columns", table)
		}

		quoted := make([]string, len(cols))
		for i, c := range cols {
			quoted[i] = `"` + c + `"`
		}
		list := strings.Join(quoted, ", ")

		fmt.Fprintf(&b, "== %s (%s)\n", table, strings.Join(cols, ", "))
		rows, err := db.Query(fmt.Sprintf(
			`SELECT %s FROM "%s" ORDER BY %s`, list, table, list))
		if err != nil {
			return "", fmt.Errorf("dumping %s: %w", table, err)
		}
		n := 0
		for rows.Next() {
			cells := make([]any, len(cols))
			for i := range cells {
				cells[i] = new(sql.NullString)
			}
			if err := rows.Scan(cells...); err != nil {
				rows.Close()
				return "", err
			}
			record := make([]string, len(cols))
			for i, c := range cells {
				ns := c.(*sql.NullString)
				if !ns.Valid {
					// NULL is a bare token while every non-NULL value is quoted, so no
					// value can spell itself as NULL. "The document does not say" and
					// "the document says nothing" are different answers, and a dump that
					// conflated them would hide a regression between the two.
					record[i] = "NULL"
					continue
				}
				// Quoted, not delimited. A " | " separator with unescaped values made
				// ("a | b", "c") and ("a", "b | c") serialise identically - two databases
				// with different contents dumping the same, in the one function the
				// rebuild check is measured with. strconv.Quote also escapes newlines and
				// the quote itself, so nothing can forge a cell boundary.
				record[i] = strconv.Quote(ns.String)
			}
			fmt.Fprintf(&b, "%s\n", strings.Join(record, " "))
			n++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return "", err
		}
		rows.Close()
		fmt.Fprintf(&b, "-- %d row(s)\n\n", n)
	}
	return b.String(), nil
}

func queryStrings(db *sql.DB, q string, args ...any) ([]string, error) {
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
