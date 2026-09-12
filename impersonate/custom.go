package impersonate

// customPreset is a hand-written target that pairs the curl/OpenSSL TLS and
// HTTP/2 fingerprint with a fixed Android Chrome header set. It reproduces the
// curl invocation that reaches APIs which allow the curl client class but
// challenge browser-shaped fingerprints:
//
//	curl --http2 -X GET '...' \
//	  -H 'User-Agent: Mozilla/5.0 (Linux; Android 10; K) ... Chrome/150.0.0.0 Mobile Safari/537.36' \
//	  -H 'Accept: text/html,application/xhtml+xml,...' \
//	  -H 'Accept-Encoding: gzip, deflate, br, zstd' \
//	  -H 'sec-ch-ua: "Not;A=Brand";v="8", "Chromium";v="150", "Google Chrome";v="150"' \
//	  -H 'sec-ch-ua-mobile: ?1' -H 'sec-ch-ua-platform: "Android"' \
//	  -H 'upgrade-insecure-requests: 1' -H 'sec-fetch-site: none' \
//	  -H 'sec-fetch-mode: navigate' -H 'sec-fetch-user: ?1' \
//	  -H 'sec-fetch-dest: document' -H 'accept-language: en-US,en;q=0.9' \
//	  -H 'priority: u=0, i'
//
// The TLS/HTTP2 half comes from the transport's "curl" target; only the headers
// are defined here.
var customPreset = Preset{
	Target: "custom",
	Alias:  "custom",
	NPN:    false,
	ALPN:   true,
	HTTPHeaders: []Header{
		{"User-Agent", "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Mobile Safari/537.36"},
		{"Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"},
		{"Accept-Encoding", "gzip, deflate, br, zstd"},
		{"sec-ch-ua", `"Not;A=Brand";v="8", "Chromium";v="150", "Google Chrome";v="150"`},
		{"sec-ch-ua-mobile", "?1"},
		{"sec-ch-ua-platform", `"Android"`},
		{"upgrade-insecure-requests", "1"},
		{"sec-fetch-site", "none"},
		{"sec-fetch-mode", "navigate"},
		{"sec-fetch-user", "?1"},
		{"sec-fetch-dest", "document"},
		{"accept-language", "en-US,en;q=0.9"},
		{"priority", "u=0, i"},
	},
}

// CustomTarget is the name of the hand-written Android-Chrome-over-curl target.
const CustomTarget = "custom"
