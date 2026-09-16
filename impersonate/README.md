# impersonate

Browser TLS/HTTP fingerprint presets ported from curl-impersonate, plus the
hand-written `custom` target, with name/alias resolution and the mapping from a
preset to the transport's TLS, HTTP/2 and HTTP/3 parameters.

## Role in the project

The package owns three things:

1. preset definitions, matching one `impersonate_opts` entry from
   curl-impersonate, and the data generated from its `lib/impersonate.c`;
2. name and alias resolution: `Get`, `MustGet`, `Targets` and `Aliases`;
3. the mapping from a preset to a transport TLS ClientHello profile and to its
   HTTP/2 and HTTP/3 settings, exposed as methods on `Preset`.

It performs no I/O and no handshake. The `gocurlffi/requests` package builds the
transport from this data, and the underlying TLS/HTTP2/HTTP3 stack comes from
`github.com/bogdanfinn/utls`, `fhttp` and `tls-client`.

## Types

### Header

`Header` is one ordered HTTP header line.

| Field | Type |
| --- | --- |
| `Name` | `string` |
| `Value` | `string` |

Values keep their original case, because header order and case are part of the
fingerprint.

### Preset

`Preset` mirrors a single `impersonate_opts` entry. Slices are pre-split from
the colon- and comma-separated C strings. The fields group as follows.

| Group | Fields |
| --- | --- |
| Identity | `Target`, `Alias`, `HTTPVersion`, `SSLVersion` |
| TLS | `Ciphers`, `Curves`, `SigHashAlgs`, `TLSGrease`, `TLSSessionTicket`, `NPN`, `ALPN`, `ALPS`, `TLSUseNewALPSCodepoint`, `TLSSignedCertTimestamps`, `CertCompression`, `TLSRecordSizeLimit`, `TLSKeySharesLimit`, `TLSDelegatedCredentials`, `TLSExtensionOrder`, `ECH`, `TLSTrustAnchors`, `TLSPermuteExtensions` |
| HTTP/2 | `HTTP2Settings`, `HTTP2PseudoHeadersOrder`, `HTTP2WindowUpdate`, `HTTP2StreamWeight`, `HTTP2StreamExclusive`, `HTTP2NoPriority`, `HTTP2Streams` |
| HTTP/3 and QUIC | `HTTP3Settings`, `HTTP3PseudoHeadersOrder`, `HTTP3Curves`, `HTTP3SigHashAlgs`, `HTTP3Headers`, `HTTP3HTTPHeaderOrder`, `HTTP3TLSExtensionOrder`, `QUICCIDLength`, `QUICTransportParameters` |
| Request headers | `HTTPHeaders`, `HTTPHeaderOrder`, `SplitCookies`, `FormBoundary` |
| WebSocket | `WSHeaders`, `WSHTTPHeaderOrder`, `WSCertCompression`, `WSDisableSessionTicket` |
| Proxy | `ProxyCredentialNoReuse` |

### BrowserType

`type BrowserType string` names the canonical preset type, matching the Python
curl_cffi `BrowserTypeLiteral` so code can be ported mechanically. The type has
no methods and no constants in this port.

## Preset methods

| Method | Returns |
| --- | --- |
| `Browser() string` | coarse family from the target prefix: `chrome`, `edge`, `firefox`, `tor`, `safari`, `okhttp`, `custom`, or `""` |
| `TLSProfile() string` | the closest maintained ClientHello profile name, or `""` |
| `PseudoHeaderOrder() []string` | HTTP/2 pseudo headers decoded from the compact order string; defaults to `:method, :authority, :scheme, :path` |
| `HTTP3PseudoHeaderOrderList() []string` | the same decoding for HTTP/3 |

The compact form uses `m`, `a`, `s` and `p` for `:method`, `:authority`,
`:scheme` and `:path`.

## Lookup and resolution

| Function | Signature |
| --- | --- |
| `Get` | `func Get(name string) (*Preset, error)` |
| `MustGet` | `func MustGet(name string) *Preset` |
| `Targets` | `func Targets() []string` |
| `Aliases` | `func Aliases() map[string]string` |

`Get` resolves a name in this order:

1. the lower-cased short-name table, so `chrome` becomes `chrome150`;
2. the exact target name;
3. the exact `Alias` field or target name recorded on a preset.

An empty name returns `impersonate: empty browser name`; an unknown name returns
`impersonate: unsupported browser name %q`. Concrete target names are matched
exactly, while the short-name table is case-insensitive. `MustGet` panics
instead of returning an error.

`Targets` returns a sorted copy of every concrete name, which is the 39
generated browser presets plus `custom`. `Aliases` returns a copy of the
short-name table only; it does not include target names.

| Short name | Resolves to |
| --- | --- |
| `chrome` | `chrome150` |
| `edge` | `edge101` |
| `safari` | `safari2601` |
| `safari_beta` | `safari2601` |
| `safari_ios` | `safari260_ios` |
| `safari_ios_beta` | `safari260_ios` |
| `chrome_android` | `chrome131_android` |
| `firefox` | `firefox147` |
| `tor` | `tor145` |
| `safari15_3` | `safari153` |
| `safari15_5` | `safari155` |
| `safari17_0` | `safari170` |
| `safari17_2_ios` | `safari172_ios` |
| `safari18_0` | `safari180` |
| `safari18_0_ios` | `safari180_ios` |
| `safari18_4` | `safari184` |
| `safari18_4_ios` | `safari184_ios` |

The `Default...` constants name the same defaults: `DefaultChrome`,
`DefaultEdge`, `DefaultSafari`, `DefaultSafariIOS`, `DefaultSafariBeta`,
`DefaultSafariIOSBeta`, `DefaultChromeAndroid`, `DefaultFirefox` and
`DefaultTor`. `CustomTarget` is `"custom"`.

## Mapping to TLS, HTTP/2 and HTTP/3

`Preset.TLSProfile()` looks the target up in `tlsProfileMap` and returns a name
from `github.com/bogdanfinn/tls-client/profiles.MappedTLSClients`, for example
`chrome131` to `chrome_131`, `firefox147` to `firefox_147`, `tor145` to
`firefox_132`, `okhttp4_android` to `okhttp4_android_13` and `chrome99` to
`chrome_103`. A chrome or edge target with no explicit entry falls back to
`chrome_150`; anything else returns `""`.

The `requests` transport then replaces the base profile with values taken from
the preset:

- TLS: `requests.buildSpecForPreset` builds a `utls.ClientHelloSpec` from
  `Ciphers`, `Curves`, `SigHashAlgs`, `TLSDelegatedCredentials`, `TLSGrease`,
  `TLSKeySharesLimit`, `TLSRecordSizeLimit`, `CertCompression`, `ECH`,
  `TLSSignedCertTimestamps`, `TLSSessionTicket`, `ALPN`, `ALPS` and
  `TLSUseNewALPSCodepoint`, in the extension order given by
  `TLSExtensionOrder` or a fixed canonical order when the preset does not pin
  one. Empty `Curves` and `SigHashAlgs` use BoringSSL's Chrome defaults.
- HTTP/2: `HTTP2Settings` and their order, the pseudo-header order from
  `PseudoHeaderOrder`, `HTTP2WindowUpdate` as the connection flow, and
  `HTTP2StreamWeight`/`HTTP2StreamExclusive` as the header priority.
- HTTP/3: `HTTP3Settings` and their order, plus the pseudo-header order from
  `HTTP3PseudoHeaderOrderList`.
- Request headers: `HTTPHeaders` are merged under the user's headers, and
  `SplitCookies` sends one `Cookie` header per cookie instead of one joined
  header.

Fields the transport reads: `Target`, `Alias` (for resolution), `Ciphers`,
`Curves`, `SigHashAlgs`, `TLSDelegatedCredentials`, `TLSGrease`,
`TLSKeySharesLimit`, `TLSRecordSizeLimit`, `CertCompression`, `ECH`,
`TLSSessionTicket`, `ALPN`, `ALPS`, `TLSUseNewALPSCodepoint`,
`TLSSignedCertTimestamps`, `TLSExtensionOrder`, `HTTPHeaders`, `SplitCookies`,
`HTTP2Settings`, `HTTP2PseudoHeadersOrder`, `HTTP2WindowUpdate`,
`HTTP2StreamWeight`, `HTTP2StreamExclusive`, `HTTP3Settings` and
`HTTP3PseudoHeadersOrder`.

Present in the generated data but not read by this port's transport:
`HTTPVersion`, `SSLVersion`, `NPN`, `WSHeaders`, `WSHTTPHeaderOrder`,
`WSCertCompression`, `WSDisableSessionTicket`, `HTTP3Curves`,
`HTTP3SigHashAlgs`, `HTTP3Headers`, `HTTP3HTTPHeaderOrder`,
`HTTP3TLSExtensionOrder`, `HTTPHeaderOrder`, `HTTP2Streams`, `HTTP2NoPriority`,
`TLSPermuteExtensions`, `TLSTrustAnchors`, `QUICCIDLength`,
`QUICTransportParameters`, `ProxyCredentialNoReuse` and `FormBoundary`. The
WebSocket, QUIC, extra HTTP/3 and header-order fields describe features that are
not ported.

## Generated preset data

`presets_gen.go` declares the unexported `var presets = []Preset{...}`. Its
header reads:

```go
// Code generated by internal/genpresets from impersonate/upstream/impersonate.c. DO NOT EDIT.
//
// Source: curl-impersonate lib/impersonate.c (MIT, see upstream/LICENSE.curl-impersonate).
```

The file is generated and must not be hand-edited. Its source of truth is the
vendored `upstream/impersonate.c`; `internal/genpresets` strips comments, parses
the `impersonations[]` array, renders one `Preset` per entry with a non-empty
`target`, sorts by target and runs `gofmt`. Regenerate it from the repository
root:

```sh
go run ./internal/genpresets     # make preprocess, or make gen
```

The package also carries `//go:generate go run ../internal/genpresets -root ..`
in `preset.go`, so `go generate ./...` from the package works too.
`internal/genpresets/main_test.go` re-parses the C source and fails if the
committed file differs; it expects 39 parsed presets.

## Non-browser targets

Three non-browser targets exist, and only `custom` is a preset in this package.
`native` and `curl` are names that the `requests` transport resolves itself, so
`Get("native")` and `Get("curl")` return errors and neither is in `Targets()`.

| Target | Where it is defined | Behaviour |
| --- | --- | --- |
| `native` | `requests/transport.go` | Also selected by `""`, `none` or `go`. Go's `crypto/tls` and `net/http` stacks with the transport's default `Accept-Encoding`; no browser headers. This mirrors curl_cffi when no impersonation is set. |
| `curl` | `requests/curl.go` | Reproduces the system curl's OpenSSL 3.x ClientHello and HTTP/2 settings (3:100, 4:65536, 2:0; connection window 1048510465; pseudo-header order `m,s,a,p`; no priority frame) and curl's default headers, `User-Agent: curl/8.21.0` and `Accept: */*`. User headers win. |
| `custom` | `custom.go` | The one hand-written `Preset`, named by `CustomTarget`. Pairs the curl/OpenSSL TLS and HTTP/2 fingerprint with 13 fixed Android Chrome headers. |

The `custom` header list is, in order: `User-Agent`, `Accept`,
`Accept-Encoding: gzip, deflate, br, zstd`, `sec-ch-ua`, `sec-ch-ua-mobile`,
`sec-ch-ua-platform`, `upgrade-insecure-requests`, `sec-fetch-site`,
`sec-fetch-mode`, `sec-fetch-user`, `sec-fetch-dest`, `accept-language` and
`priority: u=0, i`. The `requests` transport routes it through the curl TLS
path, so the TLS half comes from the `curl` target and only the headers come
from the preset.

## Vendored files

| File | Contents |
| --- | --- |
| `upstream/impersonate.c` | curl-impersonate `lib/impersonate.c`, the source of truth for the generated presets |
| `upstream/LICENSE.curl-impersonate` | the MIT license covering that file |
| `upstream/captured_clienthellos.txt` | recorded ClientHello summaries for 43 target names: `ciphers`, `exts` (with GREASE) and `exts_raw` (GREASE removed), used as reference when checking the built spec |

## Tests

`impersonate_test.go` checks that the short names resolve to the expected
targets, that an unknown name errors, that `Targets()` is sorted and holds 40
entries (39 generated presets plus `custom`), and that every target has headers
and a known browser family.
