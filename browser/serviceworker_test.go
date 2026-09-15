package browser

import "testing"

// A page that feature-detects service workers should not break: register
// resolves, and there is never a controller.
func TestServiceWorkerSurface(t *testing.T) {
	p := flexPage(t, `<script>
	window.hasSW = ('serviceWorker' in navigator);
	window.state = 'pending';
	navigator.serviceWorker.register('/sw.js').then(function(reg){
		window.state = reg && typeof reg.unregister === 'function' ? 'registered' : 'bad';
	}).catch(function(e){ window.state = 'error:' + e; });
	</script>`)
	v, err := p.Eval(`String(window.hasSW)`)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if v.String() != "true" {
		t.Fatalf("serviceWorker in navigator = %q, want true", v.String())
	}
	s, _ := p.Eval(`window.state`)
	if s.String() != "registered" {
		t.Fatalf("register state = %q, want registered", s.String())
	}
}
