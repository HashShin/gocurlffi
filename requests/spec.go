package requests

import (
	"fmt"
	"strconv"
	"strings"

	tls "github.com/bogdanfinn/utls"

	"gocurlffi/impersonate"
)

// chromeDefaultCurves and chromeDefaultSigAlgs are the values BoringSSL uses
// when a Chrome/Edge preset leaves them unspecified. They were read from live
// ClientHellos captured by scripts/capture_clienthello.py.
var chromeDefaultCurves = []string{"X25519", "P-256", "P-384"}

var chromeDefaultSigAlgs = []string{
	"ecdsa_secp256r1_sha256",
	"rsa_pss_rsae_sha256",
	"rsa_pkcs1_sha256",
	"ecdsa_secp384r1_sha384",
	"rsa_pss_rsae_sha384",
	"rsa_pkcs1_sha384",
	"rsa_pss_rsae_sha512",
	"rsa_pkcs1_sha512",
}

// cipherByName maps every cipher name used in curl-impersonate presets to its
// IANA code point.
var cipherByName = map[string]uint16{
	// IANA names (Firefox/OkHttp style)
	"TLS_AES_128_GCM_SHA256":                        tls.TLS_AES_128_GCM_SHA256,
	"TLS_AES_256_GCM_SHA384":                        tls.TLS_AES_256_GCM_SHA384,
	"TLS_CHACHA20_POLY1305_SHA256":                  tls.TLS_CHACHA20_POLY1305_SHA256,
	"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256":       tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256":         tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	"TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384":       tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
	"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384":         tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
	"TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256": tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
	"TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256":   tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
	"TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA":          tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
	"TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA":          tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
	"TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA":            tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
	"TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA":            tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
	"TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256":       tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256,
	"TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA384":       0xc024,
	"TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256":         tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256,
	"TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA384":         0xc028,
	"TLS_ECDHE_ECDSA_WITH_3DES_EDE_CBC_SHA":         tls.TLS_ECDHE_ECDSA_WITH_3DES_EDE_CBC_SHA,
	"TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA":           tls.TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA,
	"TLS_RSA_WITH_AES_128_GCM_SHA256":               tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
	"TLS_RSA_WITH_AES_256_GCM_SHA384":               tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
	"TLS_RSA_WITH_AES_128_CBC_SHA":                  tls.TLS_RSA_WITH_AES_128_CBC_SHA,
	"TLS_RSA_WITH_AES_256_CBC_SHA":                  tls.TLS_RSA_WITH_AES_256_CBC_SHA,
	"TLS_RSA_WITH_AES_128_CBC_SHA256":               tls.TLS_RSA_WITH_AES_128_CBC_SHA256,
	"TLS_RSA_WITH_AES_256_CBC_SHA256":               0x003d,
	"TLS_RSA_WITH_3DES_EDE_CBC_SHA":                 tls.TLS_RSA_WITH_3DES_EDE_CBC_SHA,

	// OpenSSL names (Chrome style)
	"ECDHE-ECDSA-AES128-GCM-SHA256": tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	"ECDHE-RSA-AES128-GCM-SHA256":   tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	"ECDHE-ECDSA-AES256-GCM-SHA384": tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
	"ECDHE-RSA-AES256-GCM-SHA384":   tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
	"ECDHE-ECDSA-CHACHA20-POLY1305": tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
	"ECDHE-RSA-CHACHA20-POLY1305":   tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
	"ECDHE-RSA-AES128-SHA":          tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
	"ECDHE-RSA-AES256-SHA":          tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
	"AES128-GCM-SHA256":             tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
	"AES256-GCM-SHA384":             tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
	"AES128-SHA":                    tls.TLS_RSA_WITH_AES_128_CBC_SHA,
	"AES256-SHA":                    tls.TLS_RSA_WITH_AES_256_CBC_SHA,
}

// curveByName maps supported group names to utls CurveIDs.
var curveByName = map[string]tls.CurveID{
	"P-256":                 tls.CurveP256,
	"P-384":                 tls.CurveP384,
	"P-521":                 tls.CurveP521,
	"X25519":                tls.X25519,
	"X25519MLKEM768":        tls.X25519MLKEM768,
	"X25519Kyber768Draft00": tls.X25519Kyber768Draft00,
	"ffdhe2048":             tls.FakeCurveFFDHE2048,
	"ffdhe3072":             tls.FakeCurveFFDHE3072,
}

// keyExchangeGroups are the groups utls can actually generate key shares for.
var keyExchangeGroups = map[tls.CurveID]bool{
	tls.X25519:         true,
	tls.X25519MLKEM768: true,
	tls.CurveP256:      true,
	tls.CurveP384:      true,
	tls.CurveP521:      true,
}

// sigByName maps signature algorithm names to utls schemes.
var sigByName = map[string]tls.SignatureScheme{
	"ecdsa_secp256r1_sha256": tls.ECDSAWithP256AndSHA256,
	"ecdsa_secp384r1_sha384": tls.ECDSAWithP384AndSHA384,
	"ecdsa_secp521r1_sha512": tls.ECDSAWithP521AndSHA512,
	"rsa_pss_rsae_sha256":    tls.PSSWithSHA256,
	"rsa_pss_rsae_sha384":    tls.PSSWithSHA384,
	"rsa_pss_rsae_sha512":    tls.PSSWithSHA512,
	"rsa_pkcs1_sha256":       tls.PKCS1WithSHA256,
	"rsa_pkcs1_sha384":       tls.PKCS1WithSHA384,
	"rsa_pkcs1_sha512":       tls.PKCS1WithSHA512,
	"ecdsa_sha1":             tls.ECDSAWithSHA1,
	"rsa_pkcs1_sha1":         tls.PKCS1WithSHA1,
	// ML-DSA (FIPS 204) schemes used by recent Chrome. utls has no named
	// constants for these, but they are ordinary uint16 code points.
	"mldsa44": tls.SignatureScheme(0x0904),
	"mldsa65": tls.SignatureScheme(0x0905),
	"mldsa87": tls.SignatureScheme(0x0906),
}

// canonicalExtensionOrder is the extension order curl-impersonate's patched
// BoringSSL emits when a preset does not pin tls_extension_order and does not
// permute extensions. It was confirmed against live ClientHellos captured by
// scripts/capture_clienthello.py (chrome99, edge101 and the Safari presets all
// reproduce this order once disabled extensions are filtered out).
//
// ECH (65037) has no observable position for non-permuting presets, so it sits
// just before the padding extension.
const canonicalExtensionOrder = "0-23-65281-10-11-35-16-5-13-18-51-45-43-27-17513-65037-21"

// presetExtensionOrder returns the TLS extension order to reproduce for a
// preset, or "" when the transport library's ClientHello profile should be
// used instead. Chrome 110+ randomly permutes its extensions, so the maintained
// profile is a better (and equally valid) sample there.
func presetExtensionOrder(p *impersonate.Preset) string {
	if p.TLSExtensionOrder != "" {
		return p.TLSExtensionOrder
	}
	// Chrome 110+ permutes its extensions per connection. JA3N and JA4 sort the
	// extension list, so a fixed canonical order reproduces them exactly; only
	// the (already randomized) JA3 byte order differs.
	return canonicalExtensionOrder
}

// presetOrder returns the extension order for a preset, tolerating nil.
func presetOrder(p *impersonate.Preset) string {
	if p == nil {
		return ""
	}
	return presetExtensionOrder(p)
}

// buildSpecForPreset constructs an exact utls ClientHelloSpec from a
// curl-impersonate preset and an extension order.
func buildSpecForPreset(p *impersonate.Preset, order string) (*tls.ClientHelloSpec, error) {
	if order == "" {
		return nil, fmt.Errorf("impersonate: preset %s has no extension order", p.Target)
	}

	ciphers := make([]uint16, 0, len(p.Ciphers)+1)
	if p.TLSGrease {
		ciphers = append(ciphers, tls.GREASE_PLACEHOLDER)
	}
	for _, name := range p.Ciphers {
		id, ok := cipherByName[name]
		if !ok {
			continue
		}
		ciphers = append(ciphers, id)
	}

	curveNames := p.Curves
	if len(curveNames) == 0 {
		// Chrome and Edge presets rely on BoringSSL's defaults.
		curveNames = chromeDefaultCurves
	}
	curves := make([]tls.CurveID, 0, len(curveNames)+1)
	if p.TLSGrease {
		curves = append(curves, tls.GREASE_PLACEHOLDER)
	}
	for _, name := range curveNames {
		if id, ok := curveByName[name]; ok {
			curves = append(curves, id)
		}
	}

	sigNames := p.SigHashAlgs
	if len(sigNames) == 0 {
		sigNames = chromeDefaultSigAlgs
	}
	sigs := make([]tls.SignatureScheme, 0, len(sigNames))
	for _, name := range sigNames {
		if id, ok := sigByName[name]; ok {
			sigs = append(sigs, id)
		}
	}
	delegated := make([]tls.SignatureScheme, 0, len(p.TLSDelegatedCredentials))
	for _, name := range p.TLSDelegatedCredentials {
		if id, ok := sigByName[name]; ok {
			delegated = append(delegated, id)
		}
	}

	keyShares := buildKeyShares(p, curves)

	exts := make([]tls.TLSExtension, 0, 20)
	greaseBeforePadding := false
	if p.TLSGrease {
		exts = append(exts, &tls.UtlsGREASEExtension{})
	}
	for _, tok := range strings.Split(strings.ReplaceAll(order, " ", ""), "-") {
		id, err := strconv.ParseUint(tok, 10, 16)
		if err != nil {
			continue
		}
		ext, ok := extensionFor(uint16(id), p, curves, sigs, delegated, keyShares)
		if !ok {
			continue
		}
		if uint16(id) == 21 && p.TLSGrease {
			// Browsers place a second GREASE value just before padding.
			exts = append(exts, &tls.UtlsGREASEExtension{}, ext)
			greaseBeforePadding = true
			continue
		}
		exts = append(exts, ext)
	}
	if p.TLSGrease && !greaseBeforePadding {
		exts = append(exts, &tls.UtlsGREASEExtension{})
	}

	return &tls.ClientHelloSpec{
		TLSVersMin:         tls.VersionTLS12,
		TLSVersMax:         tls.VersionTLS13,
		CipherSuites:       ciphers,
		CompressionMethods: []uint8{tls.CompressionNone},
		Extensions:         exts,
	}, nil
}

func buildKeyShares(p *impersonate.Preset, curves []tls.CurveID) []tls.KeyShare {
	limit := p.TLSKeySharesLimit
	if limit <= 0 {
		// BoringSSL defaults to two key shares, but utls mis-handles the exact
		// pair {X25519MLKEM768, X25519}, so request a third when a PQ group is
		// offered (this does not change JA3/JA4, which ignore key share data).
		limit = 2
		for _, c := range curves {
			if c == tls.X25519MLKEM768 || c == tls.X25519Kyber768Draft00 {
				limit = 3
				break
			}
		}
	}
	var shares []tls.KeyShare
	if p.TLSGrease {
		shares = append(shares, tls.KeyShare{Group: tls.GREASE_PLACEHOLDER, Data: []byte{0}})
	}
	for _, c := range curves {
		if len(shares) >= limit {
			break
		}
		if !keyExchangeGroups[c] {
			continue
		}
		shares = append(shares, tls.KeyShare{Group: c})
	}
	return shares
}

func extensionFor(id uint16, p *impersonate.Preset, curves []tls.CurveID, sigs []tls.SignatureScheme, delegated []tls.SignatureScheme, keyShares []tls.KeyShare) (tls.TLSExtension, bool) {
	switch id {
	case 0:
		return &tls.SNIExtension{}, true
	case 23:
		return &tls.ExtendedMasterSecretExtension{}, true
	case 65281:
		return &tls.RenegotiationInfoExtension{Renegotiation: tls.RenegotiateOnceAsClient}, true
	case 10:
		return &tls.SupportedCurvesExtension{Curves: curves}, true
	case 11:
		return &tls.SupportedPointsExtension{SupportedPoints: []byte{tls.PointFormatUncompressed}}, true
	case 35:
		if !p.TLSSessionTicket {
			return nil, false
		}
		return &tls.SessionTicketExtension{}, true
	case 16:
		if !p.ALPN {
			return nil, false
		}
		return &tls.ALPNExtension{AlpnProtocols: []string{"h2", "http/1.1"}}, true
	case 5:
		return &tls.StatusRequestExtension{}, true
	case 34:
		if len(delegated) == 0 {
			return nil, false
		}
		return &tls.FakeDelegatedCredentialsExtension{SupportedSignatureAlgorithms: delegated}, true
	case 18:
		if !p.TLSSignedCertTimestamps {
			return nil, false
		}
		return &tls.SCTExtension{}, true
	case 51:
		return &tls.KeyShareExtension{KeyShares: keyShares}, true
	case 43:
		return &tls.SupportedVersionsExtension{Versions: []uint16{tls.VersionTLS13, tls.VersionTLS12}}, true
	case 13:
		return &tls.SignatureAlgorithmsExtension{SupportedSignatureAlgorithms: sigs}, true
	case 45:
		return &tls.PSKKeyExchangeModesExtension{Modes: []uint8{tls.PskModeDHE}}, true
	case 28:
		limit := p.TLSRecordSizeLimit
		if limit <= 0 {
			return nil, false
		}
		return &tls.FakeRecordSizeLimitExtension{Limit: uint16(limit)}, true
	case 27:
		algs := certCompressionAlgs(p.CertCompression)
		if len(algs) == 0 {
			return nil, false
		}
		return &tls.UtlsCompressCertExtension{Algorithms: algs}, true
	case 65037:
		if p.ECH == "" {
			return nil, false
		}
		return &tls.GREASEECHExtension{
			CandidateCipherSuites: []tls.HPKESymmetricCipherSuite{
				// RFC 9180: HKDF-SHA256 = 0x0001, AES-128-GCM = 0x0001.
				{KdfId: 0x0001, AeadId: 0x0001},
			},
			CandidatePayloadLens: []uint16{128, 223},
		}, true
	case 17513:
		if !p.ALPS {
			return nil, false
		}
		if p.TLSUseNewALPSCodepoint {
			return &tls.ApplicationSettingsExtensionNew{SupportedProtocols: []string{"h2"}}, true
		}
		return &tls.ApplicationSettingsExtension{SupportedProtocols: []string{"h2"}}, true
	case 21:
		return &tls.UtlsPaddingExtension{GetPaddingLen: tls.BoringPaddingStyle}, true
	default:
		return nil, false
	}
}

func certCompressionAlgs(names []string) []tls.CertCompressionAlgo {
	var out []tls.CertCompressionAlgo
	for _, n := range names {
		switch strings.ToLower(n) {
		case "zlib":
			out = append(out, tls.CertCompressionZlib)
		case "brotli":
			out = append(out, tls.CertCompressionBrotli)
		case "zstd":
			out = append(out, tls.CertCompressionZstd)
		}
	}
	return out
}
