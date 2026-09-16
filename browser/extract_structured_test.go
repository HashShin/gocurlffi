package browser

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestStructuredDataJSONLD(t *testing.T) {
	p := flexPage(t, `<html><head>
		<script type="application/ld+json">
		{"@context":"https://schema.org","@type":"Product","name":"Widget","offers":{"price":"9.99"}}
		</script>
		</head><body></body></html>`)
	data := p.StructuredData()
	if len(data) != 1 {
		t.Fatalf("got %d structured-data items, want 1", len(data))
	}
	m, ok := data[0].(map[string]any)
	if !ok {
		t.Fatalf("item = %T, want object", data[0])
	}
	if m["name"] != "Widget" {
		t.Fatalf("name = %v, want Widget", m["name"])
	}
}

func TestStructuredDataArray(t *testing.T) {
	p := flexPage(t, `<script type="application/ld+json">[{"a":1},{"b":2}]</script>`)
	if got := len(p.StructuredData()); got != 2 {
		t.Fatalf("got %d items, want 2", got)
	}
}

func TestStructuredDataSkipsInvalid(t *testing.T) {
	p := flexPage(t, `<script type="application/ld+json">{not json}</script>`)
	if got := len(p.StructuredData()); got != 0 {
		t.Fatalf("got %d items, want 0", got)
	}
}

func TestStructuredDataTrailingSemicolon(t *testing.T) {
	p := flexPage(t, `<script type="application/ld+json">{"x":1};</script>`)
	data := p.StructuredData()
	if len(data) != 1 {
		t.Fatalf("got %d items, want 1 (tolerate trailing semicolon)", len(data))
	}
}

func TestMicrodata(t *testing.T) {
	p := flexPage(t, `<div itemscope itemtype="https://schema.org/Person">
		<span itemprop="name">Ada</span>
		<a itemprop="url" href="/ada">profile</a>
	</div>`)
	items := p.Microdata()
	if len(items) != 1 {
		t.Fatalf("got %d microdata items, want 1", len(items))
	}
	if items[0].Properties["name"] != "Ada" {
		t.Fatalf("name = %q, want Ada", items[0].Properties["name"])
	}
	if items[0].Properties["url"] != "/ada" {
		t.Fatalf("url = %q, want /ada", items[0].Properties["url"])
	}
}

func TestStructuredDataJSONRoundTrip(t *testing.T) {
	p := flexPage(t, `<script type="application/ld+json">{"@type":"Thing"}</script>`)
	b, err := json.Marshal(p.StructuredData())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(b, []byte("Thing")) {
		t.Fatalf("marshalled = %s", b)
	}
}
