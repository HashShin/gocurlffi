package requests

import (
	"strings"

	"github.com/bogdanfinn/fhttp/http2"
	"github.com/bogdanfinn/tls-client/profiles"
	tls "github.com/bogdanfinn/utls"
)

// The "curl" target reproduces the OpenSSL 3.x ClientHello and HTTP/2 settings
// that the system curl sends over HTTP/2. Some bot managers (for example
// Akamai rules protecting adidas' product API) allow a generic OpenSSL/curl
// client class over HTTP/2 while challenging browser-shaped fingerprints that
// lack their sensor cookie, so this gives gocurlffi a way to look like curl.
//
// Every value below was captured from a live curl ClientHello with
// scripts/capture_clienthello.py curl.

const curlImpersonateName = "curl"

// curlDefaultUserAgent is the User-Agent curl sends by default. curl's TLS and
// HTTP/2 fingerprint is tied to a "curl client" identity, so the UA has to
// match rather than claiming to be Go or a browser.
const curlDefaultUserAgent = "curl/8.21.0"

func isCurlImpersonation(name string) bool {
	return strings.EqualFold(name, curlImpersonateName)
}

// applyCurlDefaults adds curl's own default request headers so the TLS/HTTP2
// fingerprint and the headers describe the same client. User supplied headers
// are never overridden.
func applyCurlDefaults(h *Headers) {
	if !h.Has("User-Agent") {
		h.Set("User-Agent", curlDefaultUserAgent)
	}
	if !h.Has("Accept") {
		h.Set("Accept", "*/*")
	}
}

// curlCipherSuites is curl's OpenSSL 3.x cipher list, in order.
var curlCipherSuites = []uint16{
	0x1302, 0x1303, 0x1301,
	0xc02c, 0xc030, 0x009f,
	0xcca9, 0xcca8, 0xccaa,
	0xc02b, 0xc02f, 0x009e,
	0xc024, 0xc028, 0x006b,
	0xc023, 0xc027, 0x0067,
	0xc00a, 0xc014, 0x0039,
	0xc009, 0xc013, 0x0033,
	0x009d, 0x009c, 0x003d,
	0x003c, 0x0035, 0x002f,
}

// curlSupportedGroups is curl's supported_groups list (X25519MLKEM768, X25519,
// P-256, X448, P-384, P-521, ffdhe2048, ffdhe3072).
var curlSupportedGroups = []tls.CurveID{
	tls.X25519MLKEM768, tls.X25519, tls.CurveP256, tls.CurveID(30),
	tls.CurveP384, tls.CurveP521, tls.FakeCurveFFDHE2048, tls.FakeCurveFFDHE3072,
}

// curlSignatureAlgorithms is curl's signature_algorithms list, raw code points.
var curlSignatureAlgorithms = []tls.SignatureScheme{
	0x0905, 0x0906, 0x0904,
	0x0403, 0x0503, 0x0603,
	0x0807, 0x0808, 0x081a, 0x081b, 0x081c,
	0x0809, 0x080a, 0x080b,
	0x0804, 0x0805, 0x0806,
	0x0401, 0x0501, 0x0601,
	0x0303, 0x0301, 0x0302,
	0x0402, 0x0502, 0x0602,
}

// curlExtensionOrder is the exact extension order curl sends.
var curlExtensionOrder = []uint16{
	65281, 0, 11, 10, 16, 22, 23, 49, 13, 43, 45, 51, 27,
}

// buildCurlClientHelloSpec builds curl's ClientHello.
func buildCurlClientHelloSpec() tls.ClientHelloSpec {
	exts := make([]tls.TLSExtension, 0, len(curlExtensionOrder))
	for _, id := range curlExtensionOrder {
		switch id {
		case 65281:
			exts = append(exts, &tls.RenegotiationInfoExtension{Renegotiation: tls.RenegotiateOnceAsClient})
		case 0:
			exts = append(exts, &tls.SNIExtension{})
		case 11:
			exts = append(exts, &tls.SupportedPointsExtension{SupportedPoints: []byte{tls.PointFormatUncompressed}})
		case 10:
			exts = append(exts, &tls.SupportedCurvesExtension{Curves: curlSupportedGroups})
		case 16:
			exts = append(exts, &tls.ALPNExtension{AlpnProtocols: []string{"h2", "http/1.1"}})
		case 22: // encrypt_then_mac
			exts = append(exts, &tls.GenericExtension{Id: 22})
		case 23:
			exts = append(exts, &tls.ExtendedMasterSecretExtension{})
		case 49: // post_handshake_auth
			exts = append(exts, &tls.GenericExtension{Id: 49})
		case 13:
			exts = append(exts, &tls.SignatureAlgorithmsExtension{SupportedSignatureAlgorithms: curlSignatureAlgorithms})
		case 43:
			exts = append(exts, &tls.SupportedVersionsExtension{Versions: []uint16{tls.VersionTLS13, tls.VersionTLS12}})
		case 45:
			exts = append(exts, &tls.PSKKeyExchangeModesExtension{Modes: []uint8{tls.PskModeDHE}})
		case 51:
			exts = append(exts, &tls.KeyShareExtension{KeyShares: []tls.KeyShare{
				{Group: tls.X25519MLKEM768},
				{Group: tls.X25519},
			}})
		case 27:
			exts = append(exts, &tls.UtlsCompressCertExtension{Algorithms: []tls.CertCompressionAlgo{tls.CertCompressionZlib}})
		}
	}
	return tls.ClientHelloSpec{
		TLSVersMin:         tls.VersionTLS12,
		TLSVersMax:         tls.VersionTLS13,
		CipherSuites:       curlCipherSuites,
		CompressionMethods: []uint8{tls.CompressionNone},
		Extensions:         exts,
	}
}

// curlClientProfile builds the transport profile for the curl target: curl's
// ClientHello plus its HTTP/2 settings (3:100;4:65536;2:0, connection window
// 1048510465, m,s,a,p pseudo header order, no priority frame).
func curlClientProfile(base profiles.ClientProfile) profiles.ClientProfile {
	hello := tls.ClientHelloID{
		Client:               "curl",
		RandomExtensionOrder: false,
		Version:              "openssl",
		SpecFactory: func() (tls.ClientHelloSpec, error) {
			return buildCurlClientHelloSpec(), nil
		},
	}
	settings := map[http2.SettingID]uint32{
		http2.SettingMaxConcurrentStreams: 100,
		http2.SettingInitialWindowSize:    65536,
		http2.SettingEnablePush:           0,
	}
	order := []http2.SettingID{
		http2.SettingMaxConcurrentStreams,
		http2.SettingInitialWindowSize,
		http2.SettingEnablePush,
	}
	return profiles.NewClientProfile(
		hello,
		settings,
		order,
		[]string{":method", ":scheme", ":authority", ":path"},
		1048510465,
		nil,
		nil,
		1,
		true,
		nil,
		nil,
		0,
		nil,
		false,
	)
}
