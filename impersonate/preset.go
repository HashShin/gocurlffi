// Package impersonate exposes browser TLS/HTTP fingerprint presets ported from
// curl-impersonate (https://github.com/lexiforest/curl-impersonate), the native
// engine behind the Python curl_cffi project.
//
// The preset list, aliases and header sets are generated from curl-impersonate's
// lib/impersonate.c (see impersonate/upstream/impersonate.c and scripts/). The
// actual TLS/HTTP2/HTTP3 handshake is performed by the transport layer in the
// requests package.
package impersonate

import (
	"fmt"
	"sort"
	"strings"
)

// Header is one ordered HTTP header line from a fingerprint preset. Values are
// kept verbatim, including their original case, because header order and case
// are part of the fingerprint.
type Header struct {
	Name  string
	Value string
}

//go:generate go run ../internal/genpresets -root ..

// Preset mirrors a single struct impersonate_opts entry from curl-impersonate.
// Slices are pre-split from the colon/comma separated C strings for convenience.
type Preset struct {
	Target string
	Alias  string

	HTTPVersion int
	SSLVersion  int

	Ciphers     []string
	Curves      []string
	HTTP3Curves []string

	SigHashAlgs      []string
	HTTP3SigHashAlgs []string

	NPN                    bool
	ALPN                   bool
	ALPS                   bool
	TLSSessionTicket       bool
	WSDisableSessionTicket bool
	CertCompression        []string
	WSCertCompression      []string

	HTTPHeaders  []Header
	HTTP3Headers []Header
	WSHeaders    []Header

	HTTP2PseudoHeadersOrder string
	HTTP2Settings           string
	HTTPHeaderOrder         string
	HTTP3HTTPHeaderOrder    string
	WSHTTPHeaderOrder       string
	HTTP2WindowUpdate       int
	HTTP2Streams            string

	HTTP3PseudoHeadersOrder string
	HTTP3Settings           string
	QUICCIDLength           string
	QUICTransportParameters string

	TLSPermuteExtensions    bool
	TLSUseNewALPSCodepoint  bool
	TLSSignedCertTimestamps bool
	TLSTrustAnchors         []string
	ECH                     string
	TLSExtensionOrder       string
	HTTP3TLSExtensionOrder  string
	TLSDelegatedCredentials []string
	TLSRecordSizeLimit      int
	TLSKeySharesLimit       int
	TLSGrease               bool

	HTTP2StreamWeight      int
	HTTP2StreamExclusive   int
	HTTP2NoPriority        bool
	ProxyCredentialNoReuse bool

	SplitCookies bool
	FormBoundary string
}

// Browser returns the coarse browser family of the preset target.
func (p *Preset) Browser() string {
	switch {
	case strings.HasPrefix(p.Target, "chrome"):
		return "chrome"
	case strings.HasPrefix(p.Target, "edge"):
		return "edge"
	case strings.HasPrefix(p.Target, "firefox"):
		return "firefox"
	case strings.HasPrefix(p.Target, "tor"):
		return "tor"
	case strings.HasPrefix(p.Target, "safari"):
		return "safari"
	case strings.HasPrefix(p.Target, "okhttp"):
		return "okhttp"
	case p.Target == CustomTarget:
		return "custom"
	default:
		return ""
	}
}

// PseudoHeaderOrder returns the HTTP/2 pseudo header order as an ordered list of
// ":method", ":authority", ":scheme", ":path" strings. The presets store this as
// a compact string such as "masp".
func (p *Preset) PseudoHeaderOrder() []string {
	return pseudoOrder(p.HTTP2PseudoHeadersOrder, p.Browser())
}

// HTTP3PseudoHeaderOrderList behaves like PseudoHeaderOrder for HTTP/3.
func (p *Preset) HTTP3PseudoHeaderOrderList() []string {
	return pseudoOrder(p.HTTP3PseudoHeadersOrder, p.Browser())
}

func pseudoOrder(order, browser string) []string {
	if order == "" {
		// Chrome/Edge default to :method, :authority, :scheme, :path.
		if browser == "chrome" || browser == "edge" {
			order = "masp"
		} else {
			order = "masp"
		}
	}
	order = strings.ReplaceAll(order, ",", "")
	out := make([]string, 0, 4)
	for _, c := range order {
		switch c {
		case 'm':
			out = append(out, ":method")
		case 'a':
			out = append(out, ":authority")
		case 's':
			out = append(out, ":scheme")
		case 'p':
			out = append(out, ":path")
		}
	}
	return out
}

// TLSProfile maps a preset to the closest maintained TLS ClientHello profile.
// The transport uses this to reproduce a real browser's TLS stack.
func (p *Preset) TLSProfile() string {
	if v, ok := tlsProfileMap[p.Target]; ok {
		return v
	}
	if p.Browser() == "chrome" || p.Browser() == "edge" {
		return "chrome_150"
	}
	return ""
}

var (
	byTarget = map[string]*Preset{}
	byAlias  = map[string]*Preset{}
	targets  []string
)

func init() {
	for i := range presets {
		p := &presets[i]
		byTarget[p.Target] = p
		if p.Alias != "" {
			byAlias[p.Alias] = p
		}
		// The target is also usable as an alias.
		byAlias[p.Target] = p
		targets = append(targets, p.Target)
	}
	// Hand-written target (see custom.go).
	byTarget[customPreset.Target] = &customPreset
	byAlias[customPreset.Target] = &customPreset
	targets = append(targets, customPreset.Target)
	sort.Strings(targets)
}

// Get resolves a browser name or alias (for example "chrome", "chrome136" or the
// deprecated "safari15_3") to its concrete preset.
func Get(name string) (*Preset, error) {
	if name == "" {
		return nil, fmt.Errorf("impersonate: empty browser name")
	}
	if resolved, ok := aliases[strings.ToLower(name)]; ok {
		name = resolved
	}
	if p, ok := byTarget[name]; ok {
		return p, nil
	}
	if p, ok := byAlias[name]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("impersonate: unsupported browser name %q", name)
}

// MustGet is like Get but panics on unknown names. Useful for static defaults.
func MustGet(name string) *Preset {
	p, err := Get(name)
	if err != nil {
		panic(err)
	}
	return p
}

// Targets returns all concrete preset names, sorted.
func Targets() []string {
	return append([]string(nil), targets...)
}

// Aliases returns the long-form alias table ("chrome" -> "chrome150").
func Aliases() map[string]string {
	out := make(map[string]string, len(aliases))
	for k, v := range aliases {
		out[k] = v
	}
	return out
}

// BrowserTypeLiteral lists the canonical preset names. The values match the
// Python curl_cffi BrowserTypeLiteral so code can be ported mechanically.
type BrowserType string

// Default browser targets for the "chrome", "firefox", ... aliases.
const (
	DefaultChrome        = "chrome150"
	DefaultEdge          = "edge101"
	DefaultSafari        = "safari2601"
	DefaultSafariIOS     = "safari260_ios"
	DefaultSafariBeta    = "safari2601"
	DefaultSafariIOSBeta = "safari260_ios"
	DefaultChromeAndroid = "chrome131_android"
	DefaultFirefox       = "firefox147"
	DefaultTor           = "tor145"
)

// aliases maps short names to their concrete target. Kept in sync with
// curl_cffi.requests.impersonate.REAL_TARGET_MAP.
var aliases = map[string]string{
	"chrome":          DefaultChrome,
	"edge":            DefaultEdge,
	"safari":          DefaultSafari,
	"safari_beta":     DefaultSafariBeta,
	"safari_ios":      DefaultSafariIOS,
	"safari_ios_beta": DefaultSafariIOSBeta,
	"chrome_android":  DefaultChromeAndroid,
	"firefox":         DefaultFirefox,
	"tor":             DefaultTor,

	// deprecated curl_cffi aliases
	"safari15_3":     "safari153",
	"safari15_5":     "safari155",
	"safari17_0":     "safari170",
	"safari17_2_ios": "safari172_ios",
	"safari18_0":     "safari180",
	"safari18_0_ios": "safari180_ios",
	"safari18_4":     "safari184",
	"safari18_4_ios": "safari184_ios",
}

// tlsProfileMap maps a curl-impersonate preset to the nearest ClientHello
// profile in github.com/bogdanfinn/tls-client. The TLS spec itself comes from
// that library; header and HTTP/2/3 behaviour come from this preset.
var tlsProfileMap = map[string]string{
	"chrome99":          "chrome_103",
	"chrome99_android":  "chrome_103",
	"chrome100":         "chrome_103",
	"chrome101":         "chrome_103",
	"chrome104":         "chrome_104",
	"chrome107":         "chrome_107",
	"chrome110":         "chrome_110",
	"chrome116":         "chrome_117",
	"chrome119":         "chrome_120",
	"chrome120":         "chrome_120",
	"chrome123":         "chrome_124",
	"chrome124":         "chrome_124",
	"chrome131":         "chrome_131",
	"chrome131_android": "chrome_131",
	"chrome133a":        "chrome_133",
	"chrome136":         "chrome_144",
	"chrome142":         "chrome_144",
	"chrome145":         "chrome_146",
	"chrome146":         "chrome_146",
	"chrome150":         "chrome_150",

	"edge99":  "chrome_103",
	"edge101": "chrome_103",

	"firefox133": "firefox_133",
	"firefox135": "firefox_135",
	"firefox144": "firefox_147",
	"firefox147": "firefox_147",
	"tor145":     "firefox_132",

	"okhttp4_android": "okhttp4_android_13",

	"safari153":     "safari_15_6_1",
	"safari155":     "safari_16_0",
	"safari170":     "safari_16_0",
	"safari172_ios": "safari_ios_17_0",
	"safari180":     "safari_ios_18_0",
	"safari180_ios": "safari_ios_18_0",
	"safari184":     "safari_ios_18_5",
	"safari184_ios": "safari_ios_18_5",
	"safari260":     "safari_ios_26_0",
	"safari2601":    "safari_ios_26_0",
	"safari260_ios": "safari_ios_26_0",
}
