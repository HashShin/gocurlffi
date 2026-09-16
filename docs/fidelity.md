# Fidelity and checking it

What the fingerprints reproduce, how that was verified, and the two scripts
that re-check it.

## Fidelity

The fingerprint presets are transcribed from curl-impersonate's
`lib/impersonate.c`, the same source curl_cffi uses. For every preset, the TLS
ClientHello (cipher suites, supported groups, signature algorithms, extension
set and order, GREASE, ALPS, cert compression, record size limit, key shares),
the HTTP/2 settings, pseudo-header order and stream priority, the HTTP/3
settings, and the default browser headers are reproduced from that data.

This was verified against the installed curl_cffi by comparing the JA3N cipher
and extension sets, curves, Akamai HTTP/2 hash and, via `tls.peet.ws`, the full
HTTP/2 header order:

```
42/42 recorded targets: exact match (0 mismatches)
```

`make test-live` runs `TestLiveFingerprintBaseline`, which compares this port's
live fingerprints against `requests/testdata/fingerprint_baseline.json`, a
baseline recorded once from the Python curl_cffi. No Python is needed to run it.
Presets that send GREASE ECH randomise the ECH payload length, which makes
BoringSSL add the padding extension only when the ClientHello is short; the
comparison therefore ignores padding, exactly as the behaviour varies in
curl_cffi too.

A matching fingerprint is not a bypass. Heavily protected sites need more than
that, and their decisions are stateful. Against `www.adidas.co.uk/api/...` the
*same* `curl` command returned 404 and then 403 within 30 seconds, and plain Go,
curl and this client tracked each other rather than any fixed client class. The
block follows the IP and rate state, not the fingerprint.

## Checking the presets

The preset table lives in `impersonate/presets.go`, transcribed from
curl-impersonate's `lib/impersonate.c`. It is ordinary Go source, not a build
product: there is no code generation step and nothing outside Go is needed to
build or test the project.

```sh
make test         # unit tests, including the preset invariants
make test-live    # live fingerprints vs the recorded curl_cffi baseline
make capture      # capture a ClientHello with internal/capturehello
```

`make test-live` is the strong check: it compares live handshakes against
`requests/testdata/fingerprint_baseline.json`, a baseline recorded from the
Python curl_cffi. To re-check a preset against its origin, clone
[curl-impersonate](https://github.com/lexiforest/curl-impersonate) and compare
`lib/impersonate.c`.

The only optional non-Go pieces are `scripts/check_sites.sh` (bash) and the
one-off recording of the fingerprint baseline, which used the Python curl_cffi
as the reference.

## Checking sites

`scripts/check_sites.sh` fetches a list of URLs with several impersonation
targets and prints a status matrix, so you can see which target works where.

```sh
bash scripts/check_sites.sh                       # built-in sites + default targets
bash scripts/check_sites.sh https://a.com https://b.com
bash scripts/check_sites.sh -i custom,chrome131,native -t 15
bash scripts/check_sites.sh --all-targets -b      # every target, best per site
bash scripts/check_sites.sh -B quote.toscrape.com # the browser path instead
make sites
```

Defaults to `marriott.com`, `ritzcarlton.com`, `foodnetwork.com`,
`gocomics.com`, `bsky.app`, `mstdn.social` and `ubiqueros.com`.

Pass `-B` to run the same matrix with the headless browser (`--render`) instead
of the plain HTTP client; cells then show `status/rendered-size`, which makes it
obvious which sites need JavaScript.

Observed results, where marriott is the discriminating site:

| target | sites passed | marriott |
| --- | --- | --- |
| `custom` | 7/7 | 200 (3/3 runs) |
| `chrome131_android`, `chrome99_android` | 6-7/7 | 403 (3/3 runs) |
| `chrome150`, `safari260_ios` | 6-7/7 | 200/403 (flaps) |
| `chrome131`, `safari2601`, `firefox147`, `edge101`, `tor145` | 6/7 | 403 |
| `native` | 5/7 | 403 |
| `curl` | 4/7 | 403 |

`custom` is the most reliable target for Akamai-style sites: it is the only one
that stayed 200 on marriott across repeated runs, while a plain browser
fingerprint is challenged there.
