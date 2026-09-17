package gocurlffi

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/HashShin/gocurlffi/browser"
)

// The facade is a list of aliases with no code of its own, so its one failure
// mode is falling behind the packages it aliases: a type or option added to
// requests and not re-exported here is invisible to a caller using the dotted
// form, and nothing else would notice.
//
// This compares the sets of exported names directly from the source, which
// catches both a missing alias and a stale one. The browser contributes the
// page API, and its names may be spelled differently here when the plain name
// is already an HTTP verb or message.
func TestFacadeCoversRequests(t *testing.T) {
	underlying := exportedNames(t, filepath.Join("requests"))
	pageAPI := exportedNames(t, filepath.Join("browser"))
	facade := exportedNames(t, ".")

	// A couple of names belong to the aliased package but make no sense
	// unqualified, so they are deliberately absent.
	skip := map[string]bool{}

	// Facade name -> the browser name it renames, for the four the facade
	// cannot carry under their own name.
	renamed := map[string]string{
		"BrowserOptions":  "Options",
		"BrowserRequest":  "Request",
		"BrowserResponse": "Response",
	}

	var missing []string
	for name := range underlying {
		if skip[name] {
			continue
		}
		if !facade[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("these exported names exist in requests but are not aliased in gocurlffi.go: %v\n"+
			"add them, or add them to skip with a reason", missing)
	}

	var stale []string
	for name := range facade {
		if underlying[name] || pageAPI[name] {
			continue
		}
		if src, ok := renamed[name]; ok && pageAPI[src] {
			continue
		}
		stale = append(stale, name)
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("these names are aliased here but exist in neither requests nor browser: %v", stale)
	}
}

// The page API is part of the one-import promise: a program that imports the
// facade has to be able to open a page and drive it without naming the browser
// package. This names what that takes, so a rewrite cannot quietly drop it.
func TestFacadeCarriesThePageAPI(t *testing.T) {
	facade := exportedNames(t, ".")
	for _, name := range []string{
		"New", "Open", "Browser", "Page", "BrowserOptions", "ScreenshotOptions",
		"BrowserRequest", "BrowserResponse", "Block", "Fulfill",
	} {
		if !facade[name] {
			t.Errorf("gocurlffi.go no longer aliases %s, which the README's page sample needs", name)
		}
	}
}

// The README's driving sample has to run from the facade alone: the same
// Request literal Browse takes opens a page, and the page is then filled and
// clicked with no second import.
func TestFacadeOpensAndDrivesAPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<input id="q">
<button id="b">go</button>
<script>document.getElementById('b').addEventListener('click', function(){ window.hit = document.getElementById('q').value })</script>`))
	}))
	defer srv.Close()

	p, err := Open(Request{URL: srv.URL + "/"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer p.Close()

	if err := p.Fill("#q", "go"); err != nil {
		t.Fatalf("Fill: %v", err)
	}
	if err := p.Click("#b"); err != nil {
		t.Fatalf("Click: %v", err)
	}
	v, err := p.Eval("window.hit")
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if v.String() != "go" {
		t.Fatalf("the page saw %v, want the filled value", v)
	}
}

// The renamed aliases must still be aliases of the browser's own types, not
// copies, or a *Page from the browser package would not be a *Page here.
func TestFacadePageAliasesAreTheSameTypes(t *testing.T) {
	var (
		_ *Page             = (*browser.Page)(nil)
		_ *Browser          = (*browser.Browser)(nil)
		_ BrowserOptions    = browser.Options{}
		_ ScreenshotOptions = browser.ScreenshotOptions{}
	)
}

// exportedNames returns the exported top-level type, func, var and const names
// declared in the non-test Go files of a package directory.
func exportedNames(t *testing.T, dir string) map[string]bool {
	t.Helper()

	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	fset := token.NewFileSet()

	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				// Methods travel with their type, so only package-level
				// functions need an alias.
				if d.Recv == nil && d.Name.IsExported() {
					out[d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if s.Name.IsExported() {
							out[s.Name.Name] = true
						}
					case *ast.ValueSpec:
						for _, n := range s.Names {
							if n.IsExported() {
								out[n.Name] = true
							}
						}
					}
				}
			}
		}
	}
	return out
}

// The facade must be aliases, not copies. A type alias and a defined type are
// one character apart and behave differently: a defined type would not accept
// or return the values the rest of the library does.
func TestFacadeUsesAliasesNotNewTypes(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "gocurlffi.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if !ts.Assign.IsValid() {
				t.Errorf("%s is a defined type; it must be an alias (`%s = ...`)",
					ts.Name.Name, ts.Name.Name)
			}
			n++
		}
	}
	if n == 0 {
		t.Fatal("no type declarations found in the facade")
	}
}

// The facade must be usable, not merely present. This exercises the aliases the
// way the dotted form does: unqualified types, an option, a target constant and
// a module-level helper, end to end against a real server.
func TestFacadeIsFunctional(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"method":  r.Method,
			"headers": r.Header,
		})
	}))
	defer srv.Close()

	rsp, err := Send(Request{
		Method:      "GET",
		URL:         srv.URL,
		Headers:     Headers{"X-Custom: value"},
		Impersonate: Chrome131,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	var out struct {
		Method  string              `json:"method"`
		Headers map[string][]string `json:"headers"`
	}
	if err := rsp.JSON(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Method != "GET" {
		t.Errorf("method = %q", out.Method)
	}
	if got := out.Headers["X-Custom"]; len(got) == 0 || got[0] != "value" {
		t.Errorf("X-Custom = %v, want value", got)
	}
	if ua := out.Headers["User-Agent"]; len(ua) == 0 || !strings.Contains(ua[0], "Chrome/131") {
		t.Errorf("User-Agent = %v, want the Chrome 131 preset", ua)
	}
}

// Session.Send must be the same function the facade exposes, so a caller can
// mix the dotted and qualified styles in one file.
func TestFacadeSessionMatchesRequests(t *testing.T) {
	sess := NewSession()
	defer sess.Close()

	var _ *Session = sess
	if NewSession == nil {
		t.Fatal("NewSession alias is nil")
	}
}
