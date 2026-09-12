package requests

import (
	"testing"

	"github.com/bogdanfinn/tls-client/profiles"
	tls "github.com/bogdanfinn/utls"

	"gocurlffi/impersonate"
)

func TestCurlClientHelloSpecMatchesCapturedCurl(t *testing.T) {
	spec := buildCurlClientHelloSpec()
	if len(spec.CipherSuites) != 30 {
		t.Fatalf("curl ciphers = %d, want 30", len(spec.CipherSuites))
	}
	if spec.CipherSuites[0] != 0x1302 || spec.CipherSuites[29] != 0x002f {
		t.Errorf("unexpected cipher order: first=%#x last=%#x",
			spec.CipherSuites[0], spec.CipherSuites[29])
	}
	if len(spec.Extensions) != len(curlExtensionOrder) {
		t.Fatalf("curl extensions = %d, want %d", len(spec.Extensions), len(curlExtensionOrder))
	}
	// The first extension curl sends is renegotiation_info (65281), and the
	// last is compress_certificate (27).
	if _, ok := spec.Extensions[0].(*tls.RenegotiationInfoExtension); !ok {
		t.Errorf("first extension = %T", spec.Extensions[0])
	}
	if _, ok := spec.Extensions[len(spec.Extensions)-1].(*tls.UtlsCompressCertExtension); !ok {
		t.Errorf("last extension = %T", spec.Extensions[len(spec.Extensions)-1])
	}
}

func TestCurlProfileHTTP2Settings(t *testing.T) {
	base, ok := profiles.MappedTLSClients["chrome_150"]
	if !ok {
		t.Fatal("chrome_150 profile missing")
	}
	p := curlClientProfile(base)
	if p.GetConnectionFlow() != 1048510465 {
		t.Errorf("connection flow = %d", p.GetConnectionFlow())
	}
	order := p.GetSettingsOrder()
	if len(order) != 3 || order[0] != 3 || order[1] != 4 || order[2] != 2 {
		t.Errorf("settings order = %v, want [3 4 2]", order)
	}
	pseudo := p.GetPseudoHeaderOrder()
	if len(pseudo) != 4 || pseudo[1] != ":scheme" {
		t.Errorf("pseudo order = %v, want m,s,a,p", pseudo)
	}
	if p.GetHeaderPriority() != nil {
		t.Errorf("curl sends no priority frame, got %+v", p.GetHeaderPriority())
	}
}

func TestNativeAndCurlDetection(t *testing.T) {
	for _, name := range []string{"", "native", "none", "go", "NATIVE"} {
		if !isNativeImpersonation(name) {
			t.Errorf("%q should be native", name)
		}
	}
	for _, name := range []string{"curl", "Curl", "CURL"} {
		if !isCurlImpersonation(name) {
			t.Errorf("%q should be curl", name)
		}
	}
	if isNativeImpersonation("curl") || isCurlImpersonation("chrome131") {
		t.Error("native/curl detection overlap")
	}
}

func TestCurlDefaultHeaders(t *testing.T) {
	h := NewHeaders(nil)
	applyCurlDefaults(h)
	if got := h.Get("User-Agent"); got != curlDefaultUserAgent {
		t.Errorf("user-agent = %q, want %q", got, curlDefaultUserAgent)
	}
	if got := h.Get("Accept"); got != "*/*" {
		t.Errorf("accept = %q, want */*", got)
	}

	user := NewHeaders([]HeaderPair{{"User-Agent", "custom/1"}})
	applyCurlDefaults(user)
	if user.Get("User-Agent") != "custom/1" {
		t.Errorf("user agent override lost: %q", user.Get("User-Agent"))
	}
	if user.Get("Accept") != "*/*" {
		t.Errorf("accept default missing: %q", user.Get("Accept"))
	}
}

func TestCustomTargetHeaders(t *testing.T) {
	p, err := impersonate.Get("custom")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.HTTPHeaders) != 13 {
		t.Fatalf("custom headers = %d, want 13", len(p.HTTPHeaders))
	}
	if got := p.HTTPHeaders[0].Name; got != "User-Agent" {
		t.Errorf("first header = %q", got)
	}
	if got := p.HTTPHeaders[1].Name; got != "Accept" {
		t.Errorf("second header = %q", got)
	}
	if got := p.HTTPHeaders[2].Name; got != "Accept-Encoding" {
		t.Errorf("third header = %q", got)
	}
	if !usesCurlTLS("custom") {
		t.Error("custom target must use the curl TLS transport")
	}
	if !usesCurlTLS("curl") {
		t.Error("curl target must use the curl TLS transport")
	}
	if usesCurlTLS("chrome131") {
		t.Error("browser presets must not use the curl transport")
	}
}
