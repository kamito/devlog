// Command devlog starts a small web server that indexes the Markdown (.md)
// and HTML (.html/.htm) files located in the current working directory and
// any of its subdirectories. The index page lists the first title found in
// each file; selecting a Markdown file renders it as HTML on a detail page,
// while an HTML file opens as-is in a new tab. Other files in the directory
// (stylesheets, images, …) are served verbatim under /files/.
package main

import (
	"bufio"
	"bytes"
	"embed"
	"flag"
	stdhtml "html"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	gmhtml "github.com/yuin/goldmark/renderer/html"
)

// staticFS holds the GitHub Markdown stylesheet bundled into the binary.
//
//go:embed static/github-markdown.css
var staticFS embed.FS

// doc describes a single Markdown or HTML file discovered in the content
// directory.
type doc struct {
	Name  string // path relative to the content dir, slash-separated — the URL key
	Title string // first heading/<title> in the file, or the file name if none
	Date  string // date parsed from the file name (YYYY-MM-DD), or "" if none
	Dir   string // subdirectory holding the file, slash-separated ("" at top level)
	HTML  bool   // true for .html/.htm files, served verbatim in a new tab
}

// app holds the server configuration and shared dependencies.
type app struct {
	dir       string   // directory that is scanned for Markdown files
	maxDepth  int      // deepest subdirectory level to descend into (0 = unlimited)
	exclude   []string // directory name/path patterns to skip while scanning
	md        goldmark.Markdown
	tpl       *template.Template
	highlight []byte // generated CSS for code-block syntax highlighting
}

func main() {
	defDir, err := defaultDir()
	if err != nil {
		log.Fatalf("could not determine working directory: %v", err)
	}

	addr := flag.String("addr", "0.0.0.0:8080", "address to listen on (host:port)")
	host := flag.String("host", "", "host to listen on (overrides the host part of -addr)")
	port := flag.Int("port", 0, "port to listen on (overrides the port part of -addr)")
	dir := flag.String("dir", defDir, "directory to scan for Markdown files")
	maxDepth := flag.Int("max-depth", 0, "how many subdirectory levels to scan (0 = unlimited, 1 = top level only)")
	exclude := flag.String("exclude", "", "comma-separated directory names or glob patterns to skip (e.g. \"node_modules,vendor,tmp/*\")")
	flag.Parse()

	listenAddr, err := resolveAddr(*addr, *host, *port)
	if err != nil {
		log.Fatalf("invalid listen address: %v", err)
	}

	absDir, err := filepath.Abs(*dir)
	if err != nil {
		log.Fatalf("invalid directory %q: %v", *dir, err)
	}

	a := &app{
		dir:      absDir,
		maxDepth: *maxDepth,
		exclude:  splitPatterns(*exclude),
		md: goldmark.New(
			goldmark.WithExtensions(
				extension.GFM,
				highlighting.NewHighlighting(
					highlighting.WithStyle("github"),
					highlighting.WithFormatOptions(chromahtml.WithClasses(true)),
				),
			),
			goldmark.WithParserOptions(parser.WithAutoHeadingID()),
			goldmark.WithRendererOptions(gmhtml.WithUnsafe()),
		),
		tpl:       template.Must(template.New("").Parse(templates)),
		highlight: highlightCSS(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", a.handleIndex)
	mux.HandleFunc("GET /view/{name...}", a.handleView)
	mux.HandleFunc("GET /files/{name...}", a.handleFile)
	mux.Handle("GET /static/github-markdown.css", http.FileServerFS(staticFS))
	mux.HandleFunc("GET /static/highlight.css", a.handleHighlightCSS)

	log.Printf("serving Markdown from %s on http://%s", absDir, listenAddr)
	if err := http.ListenAndServe(listenAddr, mux); err != nil {
		log.Fatal(err)
	}
}

// resolveAddr composes the listen address from -addr, overriding its host
// and/or port parts when -host or -port are explicitly set (a non-empty host
// or a non-zero port). The result is always a valid "host:port" string.
func resolveAddr(addr, host string, port int) (string, error) {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	if host != "" {
		h = host
	}
	if port != 0 {
		p = strconv.Itoa(port)
	}
	return net.JoinHostPort(h, p), nil
}

// highlightCSS builds the chroma stylesheet for fenced code blocks, using
// GitHub's light palette by default and the dark palette under a
// prefers-color-scheme media query so it tracks the page theme.
//
// The github/github-dark styles tag their selectors with a ".light"/".dark"
// variant class (e.g. ".chroma.light .kn"). Rendering always emits the light
// variant on the <pre>, so we strip those suffixes and let the media queries
// alone decide which palette applies — both then target plain ".chroma".
func highlightCSS() []byte {
	f := chromahtml.New(chromahtml.WithClasses(true))

	var light, dark bytes.Buffer
	_ = f.WriteCSS(&light, styles.Get("github"))
	_ = f.WriteCSS(&dark, styles.Get("github-dark"))

	var b bytes.Buffer
	b.WriteString("@media (prefers-color-scheme: light) {\n")
	b.WriteString(strings.ReplaceAll(light.String(), ".light", ""))
	b.WriteString("}\n@media (prefers-color-scheme: dark) {\n")
	b.WriteString(strings.ReplaceAll(dark.String(), ".dark", ""))
	b.WriteString("}\n")
	return b.Bytes()
}

// handleHighlightCSS serves the generated syntax-highlighting stylesheet.
func (a *app) handleHighlightCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Write(a.highlight)
}

// defaultDir returns the current working directory — the place the command was
// invoked from — so Markdown files are scanned relative to where you run
// devlog, not where the binary happens to live.
func defaultDir() (string, error) {
	return os.Getwd()
}

// handleIndex renders the list of Markdown files and their titles.
func (a *app) handleIndex(w http.ResponseWriter, r *http.Request) {
	docs, err := a.scan()
	if err != nil {
		http.Error(w, "could not read directory", http.StatusInternalServerError)
		log.Printf("scan %s: %v", a.dir, err)
		return
	}
	a.render(w, "index", map[string]any{"Dir": a.dir, "Docs": docs})
}

// contentPath resolves a slash-separated request name to an absolute path
// inside the content directory. It rejects path traversal (the resolved path
// must stay within a.dir), hidden path components, and paths whose parent
// directories match -exclude, returning ok=false in those cases.
func (a *app) contentPath(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	full := filepath.Join(a.dir, filepath.FromSlash(name))
	rel, err := filepath.Rel(a.dir, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for i, part := range parts {
		if strings.HasPrefix(part, ".") {
			return "", false
		}
		if i < len(parts)-1 && a.excluded(strings.Join(parts[:i+1], "/")) {
			return "", false
		}
	}
	return full, true
}

// handleView renders a single Markdown file as HTML.
func (a *app) handleView(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	if !strings.HasSuffix(strings.ToLower(name), ".md") {
		http.NotFound(w, r)
		return
	}

	full, ok := a.contentPath(name)
	if !ok {
		http.NotFound(w, r)
		return
	}

	src, err := os.ReadFile(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var buf bytes.Buffer
	if err := a.md.Convert(src, &buf); err != nil {
		http.Error(w, "could not render Markdown", http.StatusInternalServerError)
		log.Printf("render %s: %v", name, err)
		return
	}

	a.render(w, "view", map[string]any{
		"Title": titleFromBytes(src, name),
		"Body":  template.HTML(buf.String()),
	})
}

// handleFile serves any file inside the content directory verbatim — HTML
// pages opened from the index as well as the assets they reference
// (stylesheets, scripts, images, …). Relative links inside a served HTML
// page keep working because they resolve to sibling /files/ URLs.
func (a *app) handleFile(w http.ResponseWriter, r *http.Request) {
	full, ok := a.contentPath(r.PathValue("name"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, full)
}

// splitPatterns turns a comma-separated flag value into a pattern list,
// dropping empty entries and surrounding whitespace.
func splitPatterns(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, strings.Trim(filepath.ToSlash(p), "/"))
		}
	}
	return out
}

// excluded reports whether the directory at the slash-separated path rel
// (relative to the content dir) matches one of the -exclude patterns. A
// pattern matches either the directory's own name or its whole relative path,
// using filepath.Match glob syntax.
func (a *app) excluded(rel string) bool {
	name := path.Base(rel)
	for _, pat := range a.exclude {
		if pat == rel || pat == name {
			return true
		}
		if ok, err := path.Match(pat, rel); err == nil && ok {
			return true
		}
		if ok, err := path.Match(pat, name); err == nil && ok {
			return true
		}
	}
	return false
}

// scan walks the content directory and its subdirectories, returning every
// discovered Markdown and HTML file sorted by its relative path. Hidden directories
// (those whose name starts with "."), directories matching -exclude, and
// anything deeper than -max-depth are skipped.
func (a *app) scan() ([]doc, error) {
	var docs []doc
	err := filepath.WalkDir(a.dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(a.dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		if e.IsDir() {
			if p == a.dir {
				return nil
			}
			if strings.HasPrefix(e.Name(), ".") || a.excluded(rel) {
				return filepath.SkipDir
			}
			// depth counts path elements: a top-level subdirectory is 1, so
			// with -max-depth 1 (top level only) we never descend into it.
			if a.maxDepth > 0 && strings.Count(rel, "/")+1 >= a.maxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		var isHTML bool
		switch strings.ToLower(path.Ext(e.Name())) {
		case ".md":
		case ".html", ".htm":
			isHTML = true
		default:
			return nil
		}

		var title string
		if isHTML {
			title, err = titleFromHTMLFile(p)
		} else {
			title, err = titleFromFile(p)
		}
		if err != nil {
			log.Printf("read %s: %v", rel, err)
			title = e.Name()
		}

		dir := path.Dir(rel)
		if dir == "." {
			dir = ""
		}
		docs = append(docs, doc{Name: rel, Title: title, Date: dateFromName(e.Name()), Dir: dir, HTML: isHTML})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(docs, func(i, j int) bool {
		return docs[i].Name < docs[j].Name
	})
	return docs, nil
}

// dateRE matches a date — and an optional time — embedded in a file name, with
// optional separators. Accepted forms include:
//
//	"2026-06-10", "20260610", "2026_06_10"        (date only)
//	"20260610-1530", "2026-06-10 15:30"           (date + HH:MM)
//	"20260610T153045", "2026-06-10_15-30-45"      (date + HH:MM:SS)
//
// Groups: 1=year 2=month 3=day 4=hour 5=minute 6=second (4–6 optional).
var dateRE = regexp.MustCompile(`(\d{4})[-_]?(\d{2})[-_]?(\d{2})(?:[-_ tT]?(\d{2})[-_:]?(\d{2})(?:[-_:]?(\d{2}))?)?`)

// dateFromName extracts a date (and time, when the name carries one) and returns
// it as a digits-only stamp: "yyyymmdd", "yyyymmddHHMM" or "yyyymmddHHMMSS", or
// "" if the name contains no recognizable date.
func dateFromName(name string) string {
	m := dateRE.FindStringSubmatch(name)
	if m == nil {
		return ""
	}
	stamp := m[1] + "/" + m[2] + "/" + m[3]
	if m[4] == "" || m[5] == "" {
		return stamp
	}
	return stamp + " " + m[4] + ":" + m[5] + ":" + m[6]
}

// titleFromFile returns the first Markdown heading in the file, or the file
// name if no heading is present.
func titleFromFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if t, ok := headingText(sc.Text()); ok {
			return t, nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return filepath.Base(path), nil
}

// htmlTitleRE and htmlH1RE locate a display title inside an HTML document;
// htmlTagRE strips any markup nested in the matched text.
var (
	htmlTitleRE = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	htmlH1RE    = regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)
	htmlTagRE   = regexp.MustCompile(`(?s)<[^>]*>`)
)

// titleFromHTMLFile returns the document's <title> (or, failing that, its
// first <h1>) from the leading 64 KiB of the file, or the file name if
// neither is present.
func titleFromHTMLFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	head, err := io.ReadAll(io.LimitReader(f, 64*1024))
	if err != nil {
		return "", err
	}
	for _, re := range []*regexp.Regexp{htmlTitleRE, htmlH1RE} {
		if m := re.FindSubmatch(head); m != nil {
			t := htmlTagRE.ReplaceAll(m[1], nil)
			if s := strings.TrimSpace(stdhtml.UnescapeString(string(t))); s != "" {
				return s, nil
			}
		}
	}
	return filepath.Base(path), nil
}

// titleFromBytes is the in-memory counterpart of titleFromFile.
func titleFromBytes(src []byte, fallback string) string {
	sc := bufio.NewScanner(bytes.NewReader(src))
	for sc.Scan() {
		if t, ok := headingText(sc.Text()); ok {
			return t
		}
	}
	return fallback
}

// headingText extracts the text of an ATX heading line (e.g. "## Foo").
func headingText(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	text := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
	if text == "" {
		return "", false
	}
	return text, true
}

// render executes the named template block and writes the result.
func (a *app) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.tpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("render template %s: %v", name, err)
	}
}

const templates = `
{{define "head"}}<!DOCTYPE html>
<html lang="ja">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.}}</title>
<link rel="stylesheet" href="/static/github-markdown.css">
<link rel="stylesheet" href="/static/highlight.css">
<style>
:root { color-scheme: light dark; }
body { margin: 0; background-color: #ffffff; }
@media (prefers-color-scheme: dark) { body { background-color: #0d1117; } }
.articles > div { padding: 4px 8px; }
.markdown-body { box-sizing: border-box; max-width: 980px;
  margin: 0 auto; padding: 32px 45px; }
@media (max-width: 767px) { .markdown-body { padding: 20px; } }
.dir { color: #59636e; font-size: .85em; margin-left: 8px; }
@media (prefers-color-scheme: dark) { .dir { color: #8b949e; } }
.date { color: #59636e; font-size: .85em; font-variant-numeric: tabular-nums; margin-left:8px; }
@media (prefers-color-scheme: dark) { .date { color: #8b949e; } }
.badge { color: #59636e; border: 1px solid #d1d9e0; border-radius: 6px;
  font-size: .7em; padding: 1px 5px; margin-left: 8px; vertical-align: middle; }
@media (prefers-color-scheme: dark) { .badge { color: #8b949e; border-color: #3d444d; } }
</style>
</head>
<body>
<article class="markdown-body">
{{end}}

{{define "foot"}}</article>
</body>
</html>{{end}}

{{define "index"}}{{template "head" "INDEX"}}
<h1>INDEX</h1>
<p class="dir">{{.Dir}}</p>
{{if .Docs}}
<div class="articles">
{{range .Docs}}<div>{{if .HTML}}<a href="/files/{{.Name}}" target="_blank" rel="noopener">{{.Title}}</a><span class="badge">HTML</span>{{else}}<a href="/view/{{.Name}}">{{.Title}}</a>{{end}}{{if .Dir}}<span class="dir">{{.Dir}}/</span>{{end}}{{if .Date}}<span class="date">{{.Date}}</span>{{end}}</div>
{{end}}</div>
{{else}}
<p>このディレクトリに .md / .html ファイルはありません。</p>
{{end}}
{{template "foot"}}{{end}}

{{define "view"}}{{template "head" .Title}}
<p><a href="/">&larr; INDEX</a></p>
{{.Body}}
{{template "foot"}}{{end}}
`
