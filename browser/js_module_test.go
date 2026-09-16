package browser

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func modulePage(t *testing.T, files map[string]string) *Page {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html")
		} else {
			w.Header().Set("Content-Type", "application/javascript")
		}
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	b := New(Options{})
	t.Cleanup(b.Close)
	p, err := b.Open(srv.URL + "/")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return p
}

func TestModuleNamedImport(t *testing.T) {
	p := modulePage(t, map[string]string{
		"/":       `<script type="module" src="/app.js"></script>`,
		"/app.js": `import {n} from './dep.js'; window.out = n*2;`,
		"/dep.js": `export const n = 21;`,
	})
	v, err := p.Eval(`window.out`)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got := v.ToInteger(); got != 42 {
		t.Fatalf("out = %d, want 42", got)
	}
}

func TestModuleDefaultImport(t *testing.T) {
	p := modulePage(t, map[string]string{
		"/":         `<script type="module" src="/app.js"></script>`,
		"/app.js":   `import greet from './greet.js'; window.msg = greet('world');`,
		"/greet.js": `export default function(name){ return 'hi ' + name; }`,
	})
	v, err := p.Eval(`window.msg`)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got := v.String(); got != "hi world" {
		t.Fatalf("msg = %q, want hi world", got)
	}
}

func TestModuleNamespaceImport(t *testing.T) {
	p := modulePage(t, map[string]string{
		"/":       `<script type="module" src="/app.js"></script>`,
		"/app.js": `import * as m from './m.js'; window.sum = m.a + m.b;`,
		"/m.js":   `export const a = 2; export const b = 3;`,
	})
	v, _ := p.Eval(`window.sum`)
	if v.ToInteger() != 5 {
		t.Fatalf("sum = %d, want 5", v.ToInteger())
	}
}

func TestModuleInlineAndDynamicImport(t *testing.T) {
	p := modulePage(t, map[string]string{
		"/": `<script type="module">
			import {x} from './x.js';
			window.x = x;
			window.dyn = 'pending';
			import('./y.js').then(function(m){ window.dyn = m.y });
		</script>`,
		"/x.js": `export const x = 7;`,
		"/y.js": `export const y = 9;`,
	})
	if v, _ := p.Eval(`window.x`); v.ToInteger() != 7 {
		t.Fatalf("x = %d, want 7", v.ToInteger())
	}
	if v, _ := p.Eval(`window.dyn`); v.ToInteger() != 9 {
		t.Fatalf("dynamic import = %v, want 9", v)
	}
}

func TestModuleImportMap(t *testing.T) {
	p := modulePage(t, map[string]string{
		"/": `<script type="importmap">{"imports":{"lib":"/lib/index.js"}}</script>
			<script type="module" src="/app.js"></script>`,
		"/app.js":       `import {v} from 'lib'; window.v = v;`,
		"/lib/index.js": `export const v = 123;`,
	})
	if v, _ := p.Eval(`window.v`); v.ToInteger() != 123 {
		t.Fatalf("v = %d, want 123", v.ToInteger())
	}
}

func TestModuleReExport(t *testing.T) {
	p := modulePage(t, map[string]string{
		"/":        `<script type="module" src="/app.js"></script>`,
		"/app.js":  `import {v} from './mid.js'; window.v = v;`,
		"/mid.js":  `export {v} from './leaf.js';`,
		"/leaf.js": `export const v = 55;`,
	})
	if v, _ := p.Eval(`window.v`); v.ToInteger() != 55 {
		t.Fatalf("v = %d, want 55", v.ToInteger())
	}
}

func TestModuleStringImportNotRewritten(t *testing.T) {
	p := modulePage(t, map[string]string{
		"/":       `<script type="module" src="/app.js"></script>`,
		"/app.js": `const s = "import x from 'nope'"; window.s = s;`,
	})
	v, _ := p.Eval(`window.s`)
	if v.String() != "import x from 'nope'" {
		t.Fatalf("string was mangled: %q", v.String())
	}
}
