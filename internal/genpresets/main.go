// Command genpresets parses curl-impersonate's lib/impersonate.c and generates
// the Go preset data used by the impersonate package.
//
// It replaces the previous Python pipeline (scripts/gen_presets.py and
// scripts/gen_go.py) so that building gocurlffi needs only the Go toolchain.
//
// Usage (from the repository root):
//
//	go run ./internal/genpresets
//
// or from the impersonate package:
//
//	go generate ./...
package main

import (
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type field struct {
	name  string
	value string // raw value text
}

type preset struct {
	fields []field
	// values populated after parsing
	strings map[string]string
	bools   map[string]bool
	ints    map[string]int
	arrays  map[string][]string
}

var (
	intFields = map[string]bool{
		"httpversion":            true,
		"ssl_version":            true,
		"http2_window_update":    true,
		"http2_stream_weight":    true,
		"http2_stream_exclusive": true,
		"tls_record_size_limit":  true,
		"tls_key_shares_limit":   true,
	}
	boolFields = map[string]bool{
		"npn": true, "alpn": true, "alps": true,
		"tls_session_ticket": true, "ws_disable_session_ticket": true,
		"tls_permute_extensions": true, "tls_use_new_alps_codepoint": true,
		"tls_signed_cert_timestamps": true, "tls_grease": true,
		"http2_no_priority": true, "proxy_credential_no_reuse": true,
		"split_cookies": true,
	}
)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()

	if err := run(*root); err != nil {
		fmt.Fprintln(os.Stderr, "genpresets:", err)
		os.Exit(1)
	}
}

func run(root string) error {
	src := filepath.Join(root, "impersonate", "upstream", "impersonate.c")
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	presets, err := parsePresets(stripComments(string(raw)))
	if err != nil {
		return err
	}
	if len(presets) == 0 {
		return fmt.Errorf("no presets parsed from %s", src)
	}

	code, err := renderGo(presets)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "impersonate", "presets_gen.go"), code, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %d presets\n", len(presets))
	return nil
}

// stripComments removes /* */ and // comments while preserving string literals.
func stripComments(text string) string {
	var out strings.Builder
	inStr := false
	for i := 0; i < len(text); i++ {
		c := text[i]
		if inStr {
			out.WriteByte(c)
			if c == '\\' && i+1 < len(text) {
				out.WriteByte(text[i+1])
				i++
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			out.WriteByte(c)
			continue
		}
		if c == '/' && i+1 < len(text) && text[i+1] == '*' {
			j := strings.Index(text[i+2:], "*/")
			if j < 0 {
				break
			}
			i = i + 2 + j + 1
			continue
		}
		if c == '/' && i+1 < len(text) && text[i+1] == '/' {
			j := strings.IndexByte(text[i:], '\n')
			if j < 0 {
				break
			}
			i += j - 1
			continue
		}
		out.WriteByte(c)
	}
	return out.String()
}

func parsePresets(text string) ([]*preset, error) {
	start := strings.Index(text, "impersonations[]")
	if start < 0 {
		return nil, fmt.Errorf("could not find impersonations[]")
	}
	bodyStart := strings.IndexByte(text[start:], '{')
	if bodyStart < 0 {
		return nil, fmt.Errorf("could not find impersonations array")
	}
	i := start + bodyStart + 1
	depth := 0
	inStr := false
	var block strings.Builder
	var presets []*preset
	capturing := false
	for ; i < len(text); i++ {
		c := text[i]
		if inStr {
			block.WriteByte(c)
			if c == '\\' && i+1 < len(text) {
				i++
				block.WriteByte(text[i])
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
			block.WriteByte(c)
		case '{':
			depth++
			capturing = true
			block.WriteByte(c)
		case '}':
			depth--
			block.WriteByte(c)
			if depth == 0 {
				if capturing {
					p, err := parsePreset(block.String()[1 : block.Len()-1])
					if err != nil {
						return nil, err
					}
					if v, ok := p.strings["target"]; ok && v != "" {
						presets = append(presets, p)
					}
				}
				block.Reset()
				capturing = false
			}
		default:
			if capturing {
				block.WriteByte(c)
			}
		}
	}
	return presets, nil
}

func parsePreset(body string) (*preset, error) {
	p := &preset{
		strings: map[string]string{},
		bools:   map[string]bool{},
		ints:    map[string]int{},
		arrays:  map[string][]string{},
	}
	for _, item := range splitTopLevel(body, ',') {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		eq := strings.IndexByte(item, '=')
		if eq < 0 || !strings.HasPrefix(item, ".") {
			continue
		}
		name := strings.TrimSpace(item[1:eq])
		value := strings.TrimSpace(item[eq+1:])

		switch {
		case strings.HasPrefix(value, "{") && strings.HasSuffix(value, "}"):
			p.arrays[name] = parseStrings(value[1 : len(value)-1])
		case intFields[name]:
			n, _ := strconv.Atoi(firstInt(value))
			p.ints[name] = n
		case boolFields[name]:
			p.bools[name] = value == "true"
		default:
			p.strings[name] = parseScalar(value)
		}
	}
	return p, nil
}

func firstInt(s string) string {
	re := regexp.MustCompile(`0x[0-9a-fA-F]+|-?\d+`)
	m := re.FindString(s)
	if m == "" {
		return "0"
	}
	if strings.HasPrefix(m, "0x") {
		n, _ := strconv.ParseInt(m, 0, 64)
		return strconv.FormatInt(n, 10)
	}
	return m
}

func parseScalar(value string) string {
	value = strings.TrimSpace(value)
	if value == "NULL" {
		return ""
	}
	return strings.Join(parseStrings(value), "")
}

// parseStrings extracts and unescapes every C string literal in s.
func parseStrings(s string) []string {
	var out []string
	inStr := false
	var cur strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !inStr {
			if c == '"' {
				inStr = true
				cur.Reset()
			}
			continue
		}
		if c == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n':
				cur.WriteByte('\n')
			case 't':
				cur.WriteByte('\t')
			case '"':
				cur.WriteByte('"')
			case '\\':
				cur.WriteByte('\\')
			default:
				cur.WriteByte('\\')
				cur.WriteByte(s[i])
			}
			continue
		}
		if c == '"' {
			inStr = false
			out = append(out, cur.String())
			continue
		}
		cur.WriteByte(c)
	}
	return out
}

// splitTopLevel splits on sep at brace depth 0 while respecting strings.
func splitTopLevel(s string, sep byte) []string {
	var parts []string
	depth := 0
	inStr := false
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			if c == '\\' && i+1 < len(s) {
				i++
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{', '[', '(':
			depth++
		case '}', ']', ')':
			depth--
		case sep:
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}

// ---------------------------------------------------------------------------
// output
// ---------------------------------------------------------------------------

const generatedHeader = `// Code generated by internal/genpresets from impersonate/upstream/impersonate.c. DO NOT EDIT.
//
// Source: curl-impersonate lib/impersonate.c (MIT, see upstream/LICENSE.curl-impersonate).

package impersonate

var presets = []Preset{
`

func renderGo(presets []*preset) ([]byte, error) {
	sortPresets(presets)
	var b strings.Builder
	b.WriteString(generatedHeader)
	for _, p := range presets {
		b.WriteString(renderPreset(p))
	}
	b.WriteString("}\n")
	src, err := format.Source([]byte(b.String()))
	if err != nil {
		return nil, fmt.Errorf("gofmt generated source: %w", err)
	}
	return src, nil
}

func sortPresets(presets []*preset) {
	for i := 1; i < len(presets); i++ {
		for j := i; j > 0; j-- {
			if presets[j-1].strings["target"] <= presets[j].strings["target"] {
				break
			}
			presets[j-1], presets[j] = presets[j], presets[j-1]
		}
	}
}

func (p *preset) str(name string) string   { return p.strings[name] }
func (p *preset) boolean(name string) bool { return p.bools[name] }
func (p *preset) integer(name string) int  { return p.ints[name] }

func goStr(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\x%02x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func goSlice(values []string) string {
	if len(values) == 0 {
		return "nil"
	}
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = goStr(v)
	}
	return "[]string{" + strings.Join(parts, ", ") + "}"
}

func goHeaders(values []string) string {
	if len(values) == 0 {
		return "nil"
	}
	parts := make([]string, len(values))
	for i, line := range values {
		name, value, _ := strings.Cut(line, ":")
		parts[i] = "{Name: " + goStr(name) + ", Value: " + goStr(strings.TrimLeft(value, " ")) + "}"
	}
	return "[]Header{" + strings.Join(parts, ", ") + "}"
}

func splitColon(v string) []string {
	if v == "" {
		return nil
	}
	var out []string
	for _, x := range strings.Split(v, ":") {
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}

func splitComma(v string) []string {
	if v == "" {
		return nil
	}
	var out []string
	for _, x := range strings.Split(v, ",") {
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}

func renderPreset(p *preset) string {
	line := func(format string, args ...any) string {
		return "    " + fmt.Sprintf(format, args...) + "\n"
	}
	var b strings.Builder
	b.WriteString("  {\n")
	b.WriteString(line("Target: %s,", goStr(p.str("target"))))
	b.WriteString(line("Alias: %s,", goStr(p.str("alias"))))
	b.WriteString(line("HTTPVersion: %d,", p.integer("httpversion")))
	b.WriteString(line("SSLVersion: %d,", p.integer("ssl_version")))
	b.WriteString(line("Ciphers: %s,", goSlice(splitColon(p.str("ciphers")))))
	b.WriteString(line("Curves: %s,", goSlice(splitColon(p.str("curves")))))
	b.WriteString(line("HTTP3Curves: %s,", goSlice(splitColon(p.str("http3_curves")))))
	b.WriteString(line("SigHashAlgs: %s,", goSlice(splitColon(p.str("sig_hash_algs")))))
	b.WriteString(line("HTTP3SigHashAlgs: %s,", goSlice(splitColon(p.str("http3_sig_hash_algs")))))
	b.WriteString(line("NPN: %t,", p.boolean("npn")))
	b.WriteString(line("ALPN: %t,", p.boolean("alpn")))
	b.WriteString(line("ALPS: %t,", p.boolean("alps")))
	b.WriteString(line("TLSSessionTicket: %t,", p.boolean("tls_session_ticket")))
	b.WriteString(line("WSDisableSessionTicket: %t,", p.boolean("ws_disable_session_ticket")))
	b.WriteString(line("CertCompression: %s,", goSlice(splitComma(p.str("cert_compression")))))
	b.WriteString(line("WSCertCompression: %s,", goSlice(splitComma(p.str("ws_cert_compression")))))
	b.WriteString(line("HTTPHeaders: %s,", goHeaders(p.arrays["http_headers"])))
	b.WriteString(line("HTTP3Headers: %s,", goHeaders(p.arrays["http3_headers"])))
	b.WriteString(line("WSHeaders: %s,", goHeaders(p.arrays["ws_headers"])))
	b.WriteString(line("HTTP2PseudoHeadersOrder: %s,", goStr(p.str("http2_pseudo_headers_order"))))
	b.WriteString(line("HTTP2Settings: %s,", goStr(p.str("http2_settings"))))
	b.WriteString(line("HTTPHeaderOrder: %s,", goStr(p.str("http_header_order"))))
	b.WriteString(line("HTTP3HTTPHeaderOrder: %s,", goStr(p.str("http3_http_header_order"))))
	b.WriteString(line("WSHTTPHeaderOrder: %s,", goStr(p.str("ws_http_header_order"))))
	b.WriteString(line("HTTP2WindowUpdate: %d,", p.integer("http2_window_update")))
	b.WriteString(line("HTTP2Streams: %s,", goStr(p.str("http2_streams"))))
	b.WriteString(line("HTTP3PseudoHeadersOrder: %s,", goStr(p.str("http3_pseudo_headers_order"))))
	b.WriteString(line("HTTP3Settings: %s,", goStr(p.str("http3_settings"))))
	b.WriteString(line("QUICCIDLength: %s,", goStr(p.str("quic_cid_length"))))
	b.WriteString(line("QUICTransportParameters: %s,", goStr(p.str("quic_transport_parameters"))))
	b.WriteString(line("TLSPermuteExtensions: %t,", p.boolean("tls_permute_extensions")))
	b.WriteString(line("TLSUseNewALPSCodepoint: %t,", p.boolean("tls_use_new_alps_codepoint")))
	b.WriteString(line("TLSSignedCertTimestamps: %t,", p.boolean("tls_signed_cert_timestamps")))
	b.WriteString(line("TLSTrustAnchors: %s,", goSlice(p.arrays["tls_trust_anchors"])))
	b.WriteString(line("ECH: %s,", goStr(p.str("ech"))))
	b.WriteString(line("TLSExtensionOrder: %s,", goStr(p.str("tls_extension_order"))))
	b.WriteString(line("HTTP3TLSExtensionOrder: %s,", goStr(p.str("http3_tls_extension_order"))))
	b.WriteString(line("TLSDelegatedCredentials: %s,", goSlice(splitColon(p.str("tls_delegated_credentials")))))
	b.WriteString(line("TLSRecordSizeLimit: %d,", p.integer("tls_record_size_limit")))
	b.WriteString(line("TLSKeySharesLimit: %d,", p.integer("tls_key_shares_limit")))
	b.WriteString(line("TLSGrease: %t,", p.boolean("tls_grease")))
	b.WriteString(line("HTTP2StreamWeight: %d,", p.integer("http2_stream_weight")))
	b.WriteString(line("HTTP2StreamExclusive: %d,", p.integer("http2_stream_exclusive")))
	b.WriteString(line("HTTP2NoPriority: %t,", p.boolean("http2_no_priority")))
	b.WriteString(line("ProxyCredentialNoReuse: %t,", p.boolean("proxy_credential_no_reuse")))
	b.WriteString(line("SplitCookies: %t,", p.boolean("split_cookies")))
	b.WriteString(line("FormBoundary: %s,", goStr(p.str("form_boundary"))))
	b.WriteString("  },\n")
	return b.String()
}
