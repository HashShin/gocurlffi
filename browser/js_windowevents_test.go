package browser

import "testing"

// Event names are case-sensitive in the source but the dispatcher lowercases
// them, so registration must lower them too. 'load' hid this because it is
// already lowercase; 'DOMContentLoaded' never matched and the listener silently
// never ran.
func TestWindowListenerEventNameCasing(t *testing.T) {
	p := flexPage(t, `<html><body><script>
		window.__hits = [];
		addEventListener('DOMContentLoaded', function(){ window.__hits.push('dcl') });
		window.addEventListener('DOMContentLoaded', function(){ window.__hits.push('dcl2') });
		window.addEventListener('load', function(){ window.__hits.push('load') });
	</script></body></html>`)
	got := evalStr(t, p, "window.__hits.join(',')")
	if got != "dcl,dcl2,load" {
		t.Fatalf("window listeners fired = %q, want %q", got, "dcl,dcl2,load")
	}
}

func TestWindowListenerRemoveIsCaseInsensitive(t *testing.T) {
	p := flexPage(t, `<html><body><script>
		window.__hits = 0;
		function h(){ window.__hits++ }
		window.addEventListener('DOMContentLoaded', h);
		window.removeEventListener('DOMContentLoaded', h);
	</script></body></html>`)
	if got := evalStr(t, p, "window.__hits"); got != "0" {
		t.Fatalf("removed listener fired %q times, want 0", got)
	}
}
