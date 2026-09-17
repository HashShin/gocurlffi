// Package gocurlffi is the whole client in one namespace, written so that it
// can be dot imported and a request reads without prefixes:
//
//	package main
//
//	import (
//		"fmt"
//
//		. "github.com/HashShin/gocurlffi"
//	)
//
//	func main() {
//		sess := NewSession()
//		defer sess.Close()
//
//		rsp, err := sess.Send(Request{
//			Method: "GET",
//			URL:    "https://httpbun.com/get",
//			Headers: Headers{
//				"Accept: application/json",
//				"X-Custom: value",
//			},
//			Impersonate: Chrome146,
//		})
//		if err != nil {
//			panic(err)
//		}
//		fmt.Println(rsp.StatusCode, rsp.Text())
//	}
//
// Everything here is an alias of the same name in [requests] or [impersonate],
// so the unqualified form and the qualified one are the same value, type and
// function, and the two can be mixed freely. There is nothing to choose
// between them but spelling.
//
// A request can also be loaded in the browser, which runs the page's scripts
// and returns the document as it settled:
//
//	rsp, err := Send(Request{URL: "https://example.com/", Browser: true})
//
// That needs the browser package linked into the program, since it is the one
// that brings a JavaScript engine:
//
//	import _ "github.com/HashShin/gocurlffi/browser"
//
// Importing it here instead would double the size of every program that only
// makes requests, so the browser stays opt-in. A Browser request without it
// reports the import that is missing.
//
// A dot import is a deliberate trade. Go's tooling discourages it, and
// staticcheck reports it as ST1001, because a dotted package puts every name it
// exports into the file: this one brings in 32 types, 51 functions and 49
// constants. Import the requests package normally instead if any of those names
// is already taken in your file. To keep the dot import and silence the check,
// add "//lint:file-ignore ST1001 reason" at the top of the file, or exclude
// ST1001 in staticcheck.conf.
//
// The package has no code of its own: it is a facade so that the shortest
// import path and a single namespace are available to a caller who wants them.
package gocurlffi

import "github.com/HashShin/gocurlffi/requests"

// Types, aliased so that a value of one is a value of the other.
type (
	BasicAuth              = requests.BasicAuth
	BrowserRenderer        = requests.BrowserRenderer
	CertificateVerifyError = requests.CertificateVerifyError
	ClientCert             = requests.ClientCert
	ConnectionError        = requests.ConnectionError
	Cookie                 = requests.Cookie
	CookieTypes            = requests.CookieTypes
	Cookies                = requests.Cookies
	DNSError               = requests.DNSError
	ExtraFingerprints      = requests.ExtraFingerprints
	HTTPError              = requests.HTTPError
	HeaderPair             = requests.HeaderPair
	HeaderTypes            = requests.HeaderTypes
	Headers                = requests.Headers
	ImpersonateError       = requests.ImpersonateError
	IncompleteRead         = requests.IncompleteRead
	InterfaceError         = requests.InterfaceError
	InvalidSchema          = requests.InvalidSchema
	InvalidURL             = requests.InvalidURL
	Option                 = requests.Option
	Param                  = requests.Param
	Params                 = requests.Params
	ProxyError             = requests.ProxyError
	Request                = requests.Request
	RequestException       = requests.RequestException
	Response               = requests.Response
	SSLError               = requests.SSLError
	Session                = requests.Session
	SessionClosed          = requests.SessionClosed
	Timeout                = requests.Timeout
	TooManyRedirects       = requests.TooManyRedirects
	UnrewindableBodyError  = requests.UnrewindableBodyError
	UnsupportedOptionError = requests.UnsupportedOptionError
)

// Constructors and one-shot request helpers.
var (
	Delete              = requests.Delete
	Do                  = requests.Do
	Get                 = requests.Get
	Head                = requests.Head
	IsTimeout           = requests.IsTimeout
	NewCookies          = requests.NewCookies
	NewHeaders          = requests.NewHeaders
	NewRequestException = requests.NewRequestException
	NewResponse         = requests.NewResponse
	NewSession          = requests.NewSession
	Options             = requests.Options
	Patch               = requests.Patch
	Post                = requests.Post
	Put                 = requests.Put
	Send                = requests.Send
	Trace               = requests.Trace
	// UseBrowser installs the renderer a Browser request delegates to. The
	// browser package calls it from its init, so this is only for a program
	// that brings a browser of its own.
	UseBrowser = requests.UseBrowser
)

// Request options.
var (
	WithAcceptEncoding  = requests.WithAcceptEncoding
	WithAkamai          = requests.WithAkamai
	WithAllowRedirects  = requests.WithAllowRedirects
	WithAuth            = requests.WithAuth
	WithBaseURL         = requests.WithBaseURL
	WithCert            = requests.WithCert
	WithContent         = requests.WithContent
	WithContentCallback = requests.WithContentCallback
	WithCookies         = requests.WithCookies
	WithData            = requests.WithData
	WithDebug           = requests.WithDebug
	WithDefaultEncoding = requests.WithDefaultEncoding
	WithDefaultHeaders  = requests.WithDefaultHeaders
	WithDiscardCookies  = requests.WithDiscardCookies
	WithExtraFP         = requests.WithExtraFP
	WithHTTPVersion     = requests.WithHTTPVersion
	WithHeader          = requests.WithHeader
	WithHeaders         = requests.WithHeaders
	WithImpersonate     = requests.WithImpersonate
	WithInterface       = requests.WithInterface
	WithJA3             = requests.WithJA3
	WithJSON            = requests.WithJSON
	WithMaxRecvSpeed    = requests.WithMaxRecvSpeed
	WithMaxRedirects    = requests.WithMaxRedirects
	WithParams          = requests.WithParams
	WithProxies         = requests.WithProxies
	WithProxy           = requests.WithProxy
	WithProxyAuth       = requests.WithProxyAuth
	WithQuote           = requests.WithQuote
	WithRaiseForStatus  = requests.WithRaiseForStatus
	WithReferer         = requests.WithReferer
	WithRetry           = requests.WithRetry
	WithStream          = requests.WithStream
	WithTimeout         = requests.WithTimeout
	WithTimeoutSeconds  = requests.WithTimeoutSeconds
	WithTrustEnv        = requests.WithTrustEnv
	WithVerify          = requests.WithVerify
)

// Errors and sentinels.
var (
	ErrTimeout = requests.ErrTimeout
)

// Impersonation targets, including the family defaults such as DefaultChrome.
const (
	Chrome100            = requests.Chrome100
	Chrome101            = requests.Chrome101
	Chrome104            = requests.Chrome104
	Chrome107            = requests.Chrome107
	Chrome110            = requests.Chrome110
	Chrome116            = requests.Chrome116
	Chrome119            = requests.Chrome119
	Chrome120            = requests.Chrome120
	Chrome123            = requests.Chrome123
	Chrome124            = requests.Chrome124
	Chrome131            = requests.Chrome131
	Chrome131Android     = requests.Chrome131Android
	Chrome133a           = requests.Chrome133a
	Chrome136            = requests.Chrome136
	Chrome142            = requests.Chrome142
	Chrome145            = requests.Chrome145
	Chrome146            = requests.Chrome146
	Chrome150            = requests.Chrome150
	Chrome99             = requests.Chrome99
	Chrome99Android      = requests.Chrome99Android
	Custom               = requests.Custom
	DefaultChrome        = requests.DefaultChrome
	DefaultChromeAndroid = requests.DefaultChromeAndroid
	DefaultEdge          = requests.DefaultEdge
	DefaultFirefox       = requests.DefaultFirefox
	DefaultSafari        = requests.DefaultSafari
	DefaultSafariBeta    = requests.DefaultSafariBeta
	DefaultSafariIOS     = requests.DefaultSafariIOS
	DefaultSafariIOSBeta = requests.DefaultSafariIOSBeta
	DefaultTor           = requests.DefaultTor
	Edge101              = requests.Edge101
	Edge99               = requests.Edge99
	Firefox133           = requests.Firefox133
	Firefox135           = requests.Firefox135
	Firefox144           = requests.Firefox144
	Firefox147           = requests.Firefox147
	OkHTTP4Android       = requests.OkHTTP4Android
	Safari153            = requests.Safari153
	Safari155            = requests.Safari155
	Safari170            = requests.Safari170
	Safari172Ios         = requests.Safari172Ios
	Safari180            = requests.Safari180
	Safari180Ios         = requests.Safari180Ios
	Safari184            = requests.Safari184
	Safari184Ios         = requests.Safari184Ios
	Safari260            = requests.Safari260
	Safari2601           = requests.Safari2601
	Safari260Ios         = requests.Safari260Ios
	Tor145               = requests.Tor145
)
