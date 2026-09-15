package browser

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// evalStr runs an expression and returns its JS string form.
func evalStr(t *testing.T, p *Page, expr string) string {
	t.Helper()
	v, err := p.Eval(expr)
	if err != nil {
		t.Fatalf("%s: %v", expr, err)
	}
	if v == nil {
		return "<nil>"
	}
	return v.String()
}

// evalThen runs script and returns expr afterwards. Any promise the script
// resolved has already run its callbacks: goja drains its job queue before
// Eval returns, so a `.then(v => window.__r = v)` is visible here.
func evalThen(t *testing.T, p *Page, script, expr string) string {
	t.Helper()
	if _, err := p.Eval(script); err != nil {
		t.Fatalf("script: %v", err)
	}
	return evalStr(t, p, expr)
}

// --- Blob / File ------------------------------------------------------------

func TestBlobBody(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	for _, tc := range []struct{ expr, want string }{
		{`new Blob(['hi']).size`, "2"},
		{`new Blob(['hi']).type`, ""},
		{`new Blob(['hi'],{type:'Text/Plain'}).type`, "text/plain"},
		{`new Blob(['hello']).slice(1,3).size`, "2"},
		{`new Blob(['a','b','c']).size`, "3"},
		// A nested Blob contributes its bytes, not "[object Blob]".
		{`new Blob([new Blob(['xy']),'z']).size`, "3"},
		{`Object.prototype.toString.call(new Blob([]))`, "[object Blob]"},
	} {
		if got := evalStr(t, p, tc.expr); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
	if got := evalStr(t, p, `typeof new Blob(['hi']).text().then`); got != "function" {
		t.Errorf("Blob.text() did not return a promise: %s", got)
	}
}

func TestBlobSliceContents(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	got := evalThen(t, p,
		`new Blob(['hello']).slice(1,3).text().then(function(v){ window.__r = v });`,
		`window.__r`)
	if got != "el" {
		t.Fatalf("slice(1,3).text() = %q, want %q", got, "el")
	}
}

func TestFileBody(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	for _, tc := range []struct{ expr, want string }{
		{`new File(['xy'],'a.txt').name`, "a.txt"},
		{`new File(['xy'],'a.txt').size`, "2"},
		{`new File(['xy'],'a.txt').type`, ""},
		{`new File(['xy'],'a.txt',{type:'text/csv'}).type`, "text/csv"},
		{`new File(['xy'],'a.txt').lastModified > 0`, "true"},
		{`Object.prototype.toString.call(new File([],'a'))`, "[object File]"},
	} {
		if got := evalStr(t, p, tc.expr); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

func TestFileReaderReads(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	got := evalThen(t, p, `
		var fr = new FileReader();
		fr.onload = function(){ window.__r = fr.result };
		fr.readAsText(new File(['body text'], 'a.txt'));
	`, `window.__r`)
	if got != "body text" {
		t.Fatalf("FileReader.result = %q, want %q", got, "body text")
	}
	if got := evalStr(t, p, `(function(){var f=new FileReader();f.readAsDataURL(new Blob(['hi'],{type:'text/plain'}));return f.result})()`); got != "data:text/plain;base64,aGk=" {
		t.Errorf("readAsDataURL = %q", got)
	}
}

// --- FormData ---------------------------------------------------------------

func TestFormDataStoresEntries(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	for _, tc := range []struct{ expr, want string }{
		{`(function(){var f=new FormData();f.append('a','1');f.append('a','2');return f.getAll('a').join(',')})()`, "1,2"},
		{`(function(){var f=new FormData();f.append('a','1');f.append('a','2');return f.get('a')})()`, "1"},
		{`(function(){var f=new FormData();f.append('a','1');f.set('a','9');return f.getAll('a').length})()`, "1"},
		{`(function(){var f=new FormData();f.append('a','1');f.delete('a');return f.has('a')})()`, "false"},
		{`(function(){var f=new FormData();f.append('a','1');f.append('b','2');return JSON.stringify(Array.from(f.entries()))})()`, `[["a","1"],["b","2"]]`},
		{`(function(){var f=new FormData();f.append('a','1');return Array.from(f.keys()).join(',')})()`, "a"},
		// for..of must work, which is what most page code uses.
		{`(function(){var f=new FormData();f.append('a','1');f.append('b','2');var s='';for(var p of f){s+=p[0]+'='+p[1]+';';}return s})()`, "a=1;b=2;"},
		{`Object.prototype.toString.call(new FormData())`, "[object FormData]"},
	} {
		if got := evalStr(t, p, tc.expr); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

func TestFormDataFromForm(t *testing.T) {
	p := flexPage(t, `<html><body><form id="f">
		<input name="user" value="ada">
		<input name="skip" value="no" type="submit">
		<input name="cb" value="on" type="checkbox" checked>
		<input name="off" value="on" type="checkbox">
		<textarea name="msg">hello</textarea>
		<select name="pick"><option value="a">A</option><option value="b" selected>B</option></select>
	</form></body></html>`)
	got := evalStr(t, p, `JSON.stringify(Array.from(new FormData(document.getElementById('f')).entries()))`)
	want := `[["user","ada"],["cb","on"],["msg","hello"],["pick","b"]]`
	if got != want {
		t.Fatalf("FormData(form) = %s, want %s", got, want)
	}
}

// --- Headers ----------------------------------------------------------------

func TestHeadersObject(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	for _, tc := range []struct{ expr, want string }{
		{`new Headers({a:'b'}).get('a')`, "b"},
		{`new Headers({A:'b'}).get('a')`, "b"},
		{`new Headers({a:'b'}).get('missing') === null`, "true"},
		{`(function(){var h=new Headers();h.append('a','1');h.append('a','2');return h.get('a')})()`, "1, 2"},
		{`(function(){var h=new Headers();h.append('a','1');h.append('a','2');return h.getAll('a').length})()`, "2"},
		{`(function(){var h=new Headers({a:'1'});h.set('a','2');return h.get('a')})()`, "2"},
		{`(function(){var h=new Headers({a:'1'});h.delete('a');return h.has('a')})()`, "false"},
		{`(function(){var h=new Headers();h.set('X-Test','v');return JSON.stringify(Array.from(h))})()`, `[["x-test","v"]]`},
		{`Object.prototype.toString.call(new Headers())`, "[object Headers]"},
	} {
		if got := evalStr(t, p, tc.expr); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

// --- Request / Response -----------------------------------------------------

func TestRequestAndResponse(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	for _, tc := range []struct{ expr, want string }{
		{`new Request('https://example.test/a').url`, "https://example.test/a"},
		{`new Request('/a').url`, "https://example.test/a"},
		{`new Request('https://example.test/a',{method:'post'}).method`, "POST"},
		{`new Request('https://example.test/a',{headers:{a:'b'}}).headers.get('a')`, "b"},
		{`new Response('hi').status`, "200"},
		{`new Response('hi').ok`, "true"},
		{`new Response('hi',{status:404}).ok`, "false"},
		{`new Response('hi',{status:201,statusText:'Created'}).statusText`, "Created"},
		{`typeof new Response('hi').body.getReader`, "function"},
		{`new Response('hi').bodyUsed`, "false"},
		{`Object.prototype.toString.call(new Response())`, "[object Response]"},
	} {
		if got := evalStr(t, p, tc.expr); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

func TestResponseBodyReads(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	got := evalThen(t, p,
		`new Response('hello').text().then(function(v){ window.__r = v });`,
		`window.__r`)
	if got != "hello" {
		t.Errorf("Response.text() = %q", got)
	}
	got = evalThen(t, p,
		`Response.json({a:1}).then(function(r){ return r.json() }).then(function(v){ window.__r = v.a });`,
		`window.__r`)
	if got != "1" {
		t.Errorf("Response.json() = %q", got)
	}
	// bodyUsed must flip after a read.
	if got := evalStr(t, p, `(function(){var r=new Response('x');r.text();return r.bodyUsed})()`); got != "true" {
		t.Errorf("bodyUsed after text() = %q, want true", got)
	}
}

// --- Streams ----------------------------------------------------------------

func TestReadableStreamReader(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	for _, tc := range []struct{ expr, want string }{
		{`typeof ReadableStream`, "function"},
		{`typeof WritableStream`, "function"},
		{`typeof TransformStream`, "function"},
		{`(function(){var r=new ReadableStream({start:function(c){c.enqueue(new Uint8Array([1,2]));c.close()}});return r.locked})()`, "false"},
		{`(function(){var r=new ReadableStream({start:function(c){c.enqueue(new Uint8Array([1,2]));c.close()}});var rd=r.getReader();return r.locked})()`, "true"},
	} {
		if got := evalStr(t, p, tc.expr); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

func TestReadableStreamDrains(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	got := evalThen(t, p, `
		(async function(){
			var r = new ReadableStream({start:function(c){ c.enqueue(new Uint8Array([104,105])); c.close() }});
			var reader = r.getReader();
			var out = '';
			for(;;){
				var step = await reader.read();
				if (step.done) break;
				out += String.fromCharCode.apply(null, step.value);
			}
			window.__r = out;
		})()
	`, `window.__r`)
	if got != "hi" {
		t.Fatalf("stream drained to %q, want %q", got, "hi")
	}
}

func TestTransformStreamPipes(t *testing.T) {
	p := flexPage(t, `<html><body></body></html>`)
	got := evalThen(t, p, `
		(async function(){
			var ts = new TransformStream({ transform: function(chunk, ctrl){ ctrl.enqueue(chunk) } });
			var writer = ts.writable.getWriter();
			writer.write(new TextEncoder().encode('abc'));
			writer.close();
			var reader = ts.readable.getReader();
			var out = '';
			for(;;){
				var step = await reader.read();
				if (step.done) break;
				out += new TextDecoder().decode(step.value);
			}
			window.__r = out;
		})()
	`, `window.__r`)
	if got != "abc" {
		t.Fatalf("transform produced %q, want %q", got, "abc")
	}
}

// --- fetch ------------------------------------------------------------------

func TestFetchReturnsRealResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test", "yes")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	p := flexPage(t, `<html><body></body></html>`)
	got := evalThen(t, p, fmt.Sprintf(`
		(async function(){
			var r = await fetch(%q);
			var j = await r.json();
			window.__r = r.status + ':' + r.headers.get('x-test') + ':' + j.ok;
		})()
	`, srv.URL), `window.__r`)
	if got != "200:yes:true" {
		t.Fatalf("fetch gave %q, want %q", got, "200:yes:true")
	}
}

func TestFetchPostsFormData(t *testing.T) {
	var body, ctype string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		ctype = r.Header.Get("Content-Type")
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	p := flexPage(t, `<html><body></body></html>`)
	_, err := p.Eval(fmt.Sprintf(`
		(function(){
			var f = new FormData();
			f.append('a', '1');
			f.append('b', 'two words');
			fetch(%q, {method:'POST', body:f});
		})()
	`, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ctype, "application/x-www-form-urlencoded") {
		t.Errorf("Content-Type = %q", ctype)
	}
	if body != "a=1&b=two+words" {
		t.Errorf("body = %q, want %q", body, "a=1&b=two+words")
	}
}

func TestFetchBodyStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "streamed")
	}))
	defer srv.Close()

	p := flexPage(t, `<html><body></body></html>`)
	got := evalThen(t, p, fmt.Sprintf(`
		(async function(){
			var r = await fetch(%q);
			var reader = r.body.getReader();
			var out = '';
			for(;;){
				var step = await reader.read();
				if (step.done) break;
				out += new TextDecoder().decode(step.value);
			}
			window.__r = out;
		})()
	`, srv.URL), `window.__r`)
	if got != "streamed" {
		t.Fatalf("resp.body drained to %q, want %q", got, "streamed")
	}
}
