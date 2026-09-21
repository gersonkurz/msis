package main

import (
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/gersonkurz/msis/internal/sbom"
	"github.com/gersonkurz/msis/internal/sbom/conformance"
)

//go:embed schema.sql
var schemaSQL string

// Report is what a build produced, so the caller can print it and the tests can assert on it
// without re-querying the database.
type Report struct {
	Documents  int
	Components int
	Products   int
	Failures   []Failure
}

// Failure is a document in the corpus that did not make it into the index.
type Failure struct {
	Source  string
	Problem string
}

// Build reads every CycloneDX document under corpusDir and writes a fresh index to dbPath.
//
// Always fresh: the corpus is authoritative (#29 D12) and this is a projection of it, so an
// existing database is replaced rather than updated. Incremental update would mean deciding
// what to do about a document that has since been deleted or rewritten, and the cheap, always
// correct answer is to have no such state.
func Build(corpusDir, dbPath string) (*Report, error) {
	docs, failures, err := readCorpus(corpusDir)
	if err != nil {
		return nil, err
	}

	if err := os.Remove(dbPath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("replacing the existing index: %w", err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	if _, err := db.Exec(schemaSQL); err != nil {
		return nil, fmt.Errorf("creating the schema: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	report, err := insertAll(tx, docs, failures, corpusDir)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return report, nil
}

// --- reading the corpus ----------------------------------------------------------------

// ingested is one document, parsed and keyed, before anything is written. The whole corpus is
// read before any row is inserted because a BOM-Link can only be resolved once every serial in
// the corpus is known, and a link that points forward is as valid as one that points back.
type ingested struct {
	source string // path within the corpus, slash-separated
	id     string
	sha256 string
	doc    cdxDocument
}

func readCorpus(dir string) ([]ingested, []Failure, error) {
	var paths []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".cdx.json") {
			return nil
		}
		paths = append(paths, p)
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("reading the corpus: %w", err)
	}
	// No sort here: filepath.WalkDir is documented to walk in lexical order, so the corpus
	// is already read in a defined sequence. Nothing downstream depends on it in any case -
	// every key is natural and Dump orders its own rows - but two builds reading the corpus
	// in different orders would make "rebuilt, identical contents" a coincidence, so it is
	// worth knowing where the order comes from.

	var docs []ingested
	var failures []Failure
	for _, p := range paths {
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			rel = p
		}
		source := filepath.ToSlash(rel)

		data, err := os.ReadFile(p)
		if err != nil {
			failures = append(failures, Failure{source, err.Error()})
			continue
		}
		// A2's validation, reused rather than restated: D is a consumer, so it checks that
		// a document IS CycloneDX and leaves the emitter profile to the emitters.
		if err := conformance.ValidateSchema(data); err != nil {
			failures = append(failures, Failure{source,
				"not a valid CycloneDX document: " + firstLine(err.Error())})
			continue
		}
		var doc cdxDocument
		if err := json.Unmarshal(data, &doc); err != nil {
			failures = append(failures, Failure{source, err.Error()})
			continue
		}

		sum := sha256.Sum256(data)
		digest := hex.EncodeToString(sum[:])
		docs = append(docs, ingested{
			source: source,
			id:     documentID(doc.SerialNumber, documentVersion(doc.Version), digest),
			sha256: digest,
			doc:    doc,
		})
	}

	// Two files carrying one serial is a corpus problem, not an index problem: the second
	// would silently replace the first. Both are reported and neither is indexed, because
	// there is no way to tell which one a link meant.
	return rejectDuplicates(docs, failures)
}

// documentID keys a document REVISION.
//
// A serial identifies a document; a serial and a version identify a revision of it, and that
// pair is what a BOM-Link addresses. Keying on the serial alone had two consequences, both
// wrong: two legitimate revisions of one document collided as duplicates and neither was
// indexed, and a link to version 2 resolved happily to version 1.
//
// The key is spelled as the link itself - urn:cdx:<uuid>/<version> - so resolving a link is a
// lookup of the value rather than a reconstruction of it. A document with no serial is keyed
// by the digest of its own bytes, which is stable for the same input and cannot collide with
// a urn:cdx.
func documentID(serial string, version int, digest string) string {
	if serial != "" {
		return bomLinkFor(serial, version)
	}
	return "sha256:" + digest
}

// bomLinkFor builds the canonical BOM-Link for a serial and version.
func bomLinkFor(serial string, version int) string {
	return "urn:cdx:" + strings.TrimPrefix(serial, "urn:uuid:") + "/" + strconv.Itoa(version)
}

// documentVersion is the CycloneDX document version. Absent is 1 - the schema's default - and
// is not the same as 0, which is why it is normalised once, here, rather than at each use.
func documentVersion(v int) int {
	if v <= 0 {
		return 1
	}
	return v
}

func rejectDuplicates(docs []ingested, failures []Failure) ([]ingested, []Failure, error) {
	seen := map[string][]string{}
	for _, d := range docs {
		seen[d.id] = append(seen[d.id], d.source)
	}
	var kept []ingested
	for _, d := range docs {
		if len(seen[d.id]) == 1 {
			kept = append(kept, d)
			continue
		}
		others := make([]string, 0, len(seen[d.id])-1)
		for _, s := range seen[d.id] {
			if s != d.source {
				others = append(others, s)
			}
		}
		failures = append(failures, Failure{d.source, fmt.Sprintf(
			"this document shares its identity (%s) with %s; neither is indexed, because a "+
				"link naming that identity cannot be resolved to one of them. Two REVISIONS "+
				"of one document are fine - they differ in `version` - but two files claiming "+
				"the same serial AND version are not",
			d.id, strings.Join(others, ", "))})
	}
	sort.Slice(failures, func(i, j int) bool { return failures[i].Source < failures[j].Source })
	return kept, failures, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// --- writing the index ------------------------------------------------------------------

func insertAll(tx *sql.Tx, docs []ingested, failures []Failure, corpusDir string) (*Report, error) {
	report := &Report{Documents: len(docs), Failures: failures}

	// Suppliers first: products, documents and components all reference the table.
	suppliers := map[string]bool{}
	note := func(name string) {
		if name != "" {
			suppliers[name] = true
		}
	}
	for _, d := range docs {
		note(d.doc.Metadata.Component.supplierName())
		if sup := d.doc.Metadata.Supplier; sup != nil {
			note(sup.Name)
		}
		// Recursive, because insertion is. A supplier named only on a NESTED component
		// would otherwise never reach the supplier table, and the component row
		// referencing it would fail its foreign key and abort the whole build - so the
		// half of the nesting fix that reads documents would have been undone by the half
		// that writes them.
		noteSuppliers(note, d.doc.Components)
	}
	for _, name := range sortedKeys(suppliers) {
		if _, err := tx.Exec(`INSERT INTO supplier(name) VALUES (?)`, name); err != nil {
			return nil, err
		}
	}

	// Products, then documents: a document references its product.
	products := map[string]*productRow{}
	for _, d := range docs {
		key, upgrade := productIdentity(&d.doc)
		p, ok := products[key]
		if !ok {
			p = &productRow{id: key, upgradeCode: upgrade}
			products[key] = p
		}
		// The product's name is the one the most recent document gives it, because a
		// product can be renamed between releases and the current name is the useful
		// answer. "Most recent" is by the documents' own timestamps, which are fixed input,
		// so this is deterministic for a fixed corpus - the id breaks ties.
		if name := d.doc.Metadata.Component.Name; name != "" {
			if p.nameFrom == "" || d.doc.Metadata.Timestamp+"\x00"+d.id > p.nameFrom {
				p.name = name
				p.nameFrom = d.doc.Metadata.Timestamp + "\x00" + d.id
				p.supplier = d.doc.Metadata.Component.supplierName()
			}
		}
	}
	report.Products = len(products)
	for _, key := range sortedProductKeys(products) {
		p := products[key]
		if p.name == "" {
			p.name = key
		}
		if _, err := tx.Exec(
			`INSERT INTO product(id, upgrade_code, name, supplier) VALUES (?, ?, ?, ?)`,
			p.id, nullable(p.upgradeCode), p.name, nullable(p.supplier)); err != nil {
			return nil, err
		}
	}

	// Every revision the corpus holds, keyed the way a link spells it, so resolution is a
	// lookup rather than a guess. Keyed by serial alone, a link to version 2 resolved to
	// version 1 - a link that silently points at different content than it asked for.
	byLink := map[string]string{}
	for _, d := range docs {
		if d.doc.SerialNumber != "" {
			byLink[d.id] = d.id
		}
	}

	// Every document row first, then their contents. A BOM-Link resolves to a document that
	// may sort after the one linking to it - the corpus really is like that, a bundle's
	// document naming the MSI document beside it - so inserting a document's relationships
	// while later documents are still missing fails the foreign key on a perfectly good
	// corpus. Two passes, rather than deferring the constraint: a deferred check reports at
	// COMMIT, by which point it can no longer say which file was at fault.
	for _, d := range docs {
		if err := insertDocumentRow(tx, d); err != nil {
			return nil, fmt.Errorf("%s: %w", d.source, err)
		}
	}
	for _, d := range docs {
		if err := insertDocumentContents(tx, d, byLink); err != nil {
			return nil, fmt.Errorf("%s: %w", d.source, err)
		}
		report.Components += countComponents(d.doc.Components)
	}

	for _, f := range failures {
		if _, err := tx.Exec(
			`INSERT OR REPLACE INTO ingest_error(source, problem) VALUES (?, ?)`,
			f.Source, f.Problem); err != nil {
			return nil, err
		}
	}

	for _, kv := range [][2]string{
		{"schema_version", "1"},
		{"corpus", filepath.ToSlash(corpusDir)},
		{"documents", strconv.Itoa(report.Documents)},
		{"failures", strconv.Itoa(len(failures))},
	} {
		if _, err := tx.Exec(`INSERT INTO build_info(key, value) VALUES (?, ?)`,
			kv[0], kv[1]); err != nil {
			return nil, err
		}
	}
	return report, nil
}

type productRow struct {
	id, upgradeCode, name, supplier string
	nameFrom                        string // the (timestamp, id) the name was taken from
}

// productIdentity decides which product a document describes.
//
// The UpgradeCode where the document states one, because Windows Installer defines it as the
// identifier constant across a product's releases. Otherwise the subject's name, which is not
// guaranteed unique - so the key says which rule produced it rather than leaving a consumer to
// assume the strong one.
func productIdentity(d *cdxDocument) (id, upgradeCode string) {
	for _, p := range d.Metadata.Properties {
		if p.Name == "msis:msi.upgradeCode" && p.Value != "" {
			code := normaliseGUID(p.Value)
			return "upgrade:" + code, code
		}
	}
	if name := d.Metadata.Component.Name; name != "" {
		return "name:" + name, ""
	}
	return "name:", ""
}

func normaliseGUID(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "{")
	s = strings.TrimSuffix(s, "}")
	return strings.ToLower(s)
}

func insertDocumentRow(tx *sql.Tx, d ingested) error {
	productID, _ := productIdentity(&d.doc)
	subject := d.doc.Metadata.Component

	version := documentVersion(d.doc.Version)

	if _, err := tx.Exec(`
		INSERT INTO document(id, serial, version, spec_version, timestamp, source, sha256,
		                     product_id, subject_ref, subject_name, subject_version,
		                     subject_sha256, subject_artifact, supplier)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.id, nullable(d.doc.SerialNumber), version, d.doc.SpecVersion,
		nullable(d.doc.Metadata.Timestamp), d.source, d.sha256, productID,
		nullable(subject.BOMRef), nullable(subject.Name), nullable(subject.Version),
		nullable(subject.hash("SHA-256")), nullable(propertyOf(d.doc.Metadata.Properties,
			"msis:subject.artifact")), nullable(subject.supplierName()),
	); err != nil {
		return err
	}
	return nil
}

func insertDocumentContents(tx *sql.Tx, d ingested, byLink map[string]string) error {
	for _, p := range d.doc.Metadata.Properties {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO document_property(document_id, name, value) VALUES (?, ?, ?)`,
			d.id, p.Name, p.Value); err != nil {
			return err
		}
	}

	// Flattened depth-first, so a component that CONTAINS components contributes all of them.
	// CycloneDX allows that nesting - an assembly holding the files it is made of - and
	// walking only the top-level array made those children vanish from a document that
	// otherwise reported clean: a digest lookup returned a false negative while coverage said
	// nothing was missing.
	ordinal := 0
	var walk func(parent string, depth int, list []cdxComponent) error
	walk = func(parent string, depth int, list []cdxComponent) error {
		for _, c := range list {
			i := ordinal
			ordinal++
			if err := insertComponent(tx, d, i, depth, parent, c, byLink); err != nil {
				return fmt.Errorf("component %d (%s): %w", i, c.Name, err)
			}
			if len(c.Components) > 0 {
				// The child's parent is this component's ref where it has one. Where it
				// does not, the chain is recorded as far as it is knowable rather than
				// invented.
				if err := walk(c.BOMRef, depth+1, c.Components); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk("", 0, d.doc.Components); err != nil {
		return err
	}

	for _, dep := range d.doc.Dependencies {
		for _, on := range dep.DependsOn {
			if _, err := tx.Exec(`
				INSERT OR IGNORE INTO relationship(document_id, from_ref, to_ref, kind)
				VALUES (?, ?, ?, 'dependsOn')`, d.id, dep.Ref, on); err != nil {
				return err
			}
		}
	}
	return nil
}

// promoted are the properties that became component columns. They are not repeated in
// component_property, so a fact lives in exactly one place and a query cannot find two answers.
var promoted = map[string]bool{
	"msis:role":            true,
	"msis:installTarget":   true,
	"msis:payload.carried": true,
}

func insertComponent(tx *sql.Tx, d ingested, ordinal, depth int, parent string, c cdxComponent,
	byLink map[string]string) error {

	if _, err := tx.Exec(`
		INSERT INTO component(document_id, ordinal, bom_ref, type, name, version, purl,
		                      role, install_target, carried, supplier, parent_ref, depth)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.id, ordinal, nullable(c.BOMRef), c.Type, c.Name, nullable(c.Version),
		nullable(c.PURL),
		nullable(propertyOf(c.Properties, "msis:role")),
		nullable(propertyOf(c.Properties, "msis:installTarget")),
		carriedValue(propertyOf(c.Properties, "msis:payload.carried")),
		nullable(c.supplierName()), nullable(parent), depth,
	); err != nil {
		return err
	}

	for _, h := range c.Hashes {
		if _, err := tx.Exec(`
			INSERT OR IGNORE INTO hash(document_id, ordinal, alg, content) VALUES (?, ?, ?, ?)`,
			d.id, ordinal, h.Alg, strings.ToLower(h.Content)); err != nil {
			return err
		}
	}

	for _, p := range c.Properties {
		if promoted[p.Name] {
			continue
		}
		if _, err := tx.Exec(`
			INSERT OR IGNORE INTO component_property(document_id, ordinal, name, value)
			VALUES (?, ?, ?, ?)`, d.id, ordinal, p.Name, p.Value); err != nil {
			return err
		}
	}

	// A BOM-Link points at a document by serial. Whether that document is in this corpus is
	// the interesting part - a link that resolves nowhere is exactly what an auditor needs
	// to be told about - so the target is recorded either way and resolution is a column.
	for _, ref := range c.ExternalReferences {
		if ref.Type != "bom" || c.BOMRef == "" {
			continue
		}
		var resolved any
		if serial, version, err := sbom.ParseBOMLink(ref.URL); err == nil {
			// Resolved on serial AND version: a link asks for a particular revision, and
			// answering with a different one is worse than answering "not here".
			if id, ok := byLink[bomLinkFor(serial, version)]; ok {
				resolved = id
			}
		}
		if _, err := tx.Exec(`
			INSERT OR IGNORE INTO relationship(document_id, from_ref, to_ref, kind,
			                                   resolved_document_id)
			VALUES (?, ?, ?, 'bom-link', ?)`, d.id, c.BOMRef, ref.URL, resolved); err != nil {
			return err
		}
	}
	return nil
}

// carriedValue maps the msis:payload.carried property to a nullable boolean. Absent is NULL -
// "the document does not say" - and is a different answer from false.
func carriedValue(v string) any {
	switch v {
	case "true":
		return 1
	case "false":
		return 0
	default:
		return nil
	}
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func propertyOf(props []cdxProperty, name string) string {
	for _, p := range props {
		if p.Name == name {
			return p.Value
		}
	}
	return ""
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedProductKeys(m map[string]*productRow) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// --- the shapes read out of a document ----------------------------------------------------

// Its own minimal view of CycloneDX rather than the emitter's types: the index consumes
// documents msis did not necessarily write, and sharing structs with one emitter would quietly
// make that emitter's choices look like the format.
type cdxDocument struct {
	BOMFormat    string `json:"bomFormat"`
	SpecVersion  string `json:"specVersion"`
	SerialNumber string `json:"serialNumber"`
	Version      int    `json:"version"`

	Metadata struct {
		Timestamp  string        `json:"timestamp"`
		Component  cdxComponent  `json:"component"`
		Supplier   *cdxSupplier  `json:"supplier"`
		Properties []cdxProperty `json:"properties"`
	} `json:"metadata"`

	Components   []cdxComponent `json:"components"`
	Dependencies []struct {
		Ref       string   `json:"ref"`
		DependsOn []string `json:"dependsOn"`
	} `json:"dependencies"`
}

// noteSuppliers walks a component tree, so every supplier the index will write a reference to
// is registered first. It mirrors the insertion walk exactly; the two must not diverge.
func noteSuppliers(note func(string), list []cdxComponent) {
	for _, c := range list {
		note(c.supplierName())
		noteSuppliers(note, c.Components)
	}
}

func countComponents(list []cdxComponent) int {
	n := 0
	for _, c := range list {
		n += 1 + countComponents(c.Components)
	}
	return n
}

type cdxComponent struct {
	Type       string        `json:"type"`
	BOMRef     string        `json:"bom-ref"`
	Name       string        `json:"name"`
	Version    string        `json:"version"`
	PURL       string        `json:"purl"`
	Supplier   *cdxSupplier  `json:"supplier"`
	Hashes     []cdxHash     `json:"hashes"`
	Properties []cdxProperty `json:"properties"`

	ExternalReferences []struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"externalReferences"`

	// A component may contain components. msis emits none, but the index consumes documents
	// msis did not write.
	Components []cdxComponent `json:"components"`
}

func (c cdxComponent) supplierName() string {
	if c.Supplier == nil {
		return ""
	}
	return c.Supplier.Name
}

func (c cdxComponent) hash(alg string) string {
	for _, h := range c.Hashes {
		if strings.EqualFold(h.Alg, alg) {
			return strings.ToLower(h.Content)
		}
	}
	return ""
}

type cdxSupplier struct {
	Name string `json:"name"`
}

type cdxHash struct {
	Alg     string `json:"alg"`
	Content string `json:"content"`
}

type cdxProperty struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
