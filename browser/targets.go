package browser

import "github.com/HashShin/gocurlffi/impersonate"

// Impersonation targets, re-exported from the impersonate package so that a
// caller who only drives the browser needs one import rather than two:
//
//	import . "github.com/HashShin/gocurlffi/browser"
//
//	p, err := Get("https://quotes.toscrape.com/js/", Chrome131)
//
// They are aliases of the constants in impersonate/targets.go, not a second
// copy of the values. A raw string works too, since Get takes a plain string,
// but a misspelt constant does not compile where a misspelt string only fails
// when the page is loaded.
const (
	Chrome100            = impersonate.Chrome100
	Chrome101            = impersonate.Chrome101
	Chrome104            = impersonate.Chrome104
	Chrome107            = impersonate.Chrome107
	Chrome110            = impersonate.Chrome110
	Chrome116            = impersonate.Chrome116
	Chrome119            = impersonate.Chrome119
	Chrome120            = impersonate.Chrome120
	Chrome123            = impersonate.Chrome123
	Chrome124            = impersonate.Chrome124
	Chrome131            = impersonate.Chrome131
	Chrome131Android     = impersonate.Chrome131Android
	Chrome133a           = impersonate.Chrome133a
	Chrome136            = impersonate.Chrome136
	Chrome142            = impersonate.Chrome142
	Chrome145            = impersonate.Chrome145
	Chrome146            = impersonate.Chrome146
	Chrome150            = impersonate.Chrome150
	Chrome99             = impersonate.Chrome99
	Chrome99Android      = impersonate.Chrome99Android
	Custom               = impersonate.Custom
	Edge101              = impersonate.Edge101
	Edge99               = impersonate.Edge99
	Firefox133           = impersonate.Firefox133
	Firefox135           = impersonate.Firefox135
	Firefox144           = impersonate.Firefox144
	Firefox147           = impersonate.Firefox147
	OkHTTP4Android       = impersonate.OkHTTP4Android
	Safari153            = impersonate.Safari153
	Safari155            = impersonate.Safari155
	Safari170            = impersonate.Safari170
	Safari172Ios         = impersonate.Safari172Ios
	Safari180            = impersonate.Safari180
	Safari180Ios         = impersonate.Safari180Ios
	Safari184            = impersonate.Safari184
	Safari184Ios         = impersonate.Safari184Ios
	Safari260            = impersonate.Safari260
	Safari2601           = impersonate.Safari2601
	Safari260Ios         = impersonate.Safari260Ios
	Tor145               = impersonate.Tor145
	DefaultChrome        = impersonate.DefaultChrome
	DefaultEdge          = impersonate.DefaultEdge
	DefaultSafari        = impersonate.DefaultSafari
	DefaultSafariIOS     = impersonate.DefaultSafariIOS
	DefaultSafariBeta    = impersonate.DefaultSafariBeta
	DefaultSafariIOSBeta = impersonate.DefaultSafariIOSBeta
	DefaultChromeAndroid = impersonate.DefaultChromeAndroid
	DefaultFirefox       = impersonate.DefaultFirefox
	DefaultTor           = impersonate.DefaultTor
)

// targetConstants and defaultConstants list the two groups above so a test can
// compare them with the impersonate package rather than trusting the lists to
// stay in step. allTargets is both, for a caller or test that wants either.
var (
	targetConstants = []string{
		Chrome100,
		Chrome101,
		Chrome104,
		Chrome107,
		Chrome110,
		Chrome116,
		Chrome119,
		Chrome120,
		Chrome123,
		Chrome124,
		Chrome131,
		Chrome131Android,
		Chrome133a,
		Chrome136,
		Chrome142,
		Chrome145,
		Chrome146,
		Chrome150,
		Chrome99,
		Chrome99Android,
		Custom,
		Edge101,
		Edge99,
		Firefox133,
		Firefox135,
		Firefox144,
		Firefox147,
		OkHTTP4Android,
		Safari153,
		Safari155,
		Safari170,
		Safari172Ios,
		Safari180,
		Safari180Ios,
		Safari184,
		Safari184Ios,
		Safari260,
		Safari2601,
		Safari260Ios,
		Tor145,
	}
	defaultConstants = []string{
		DefaultChrome,
		DefaultEdge,
		DefaultSafari,
		DefaultSafariIOS,
		DefaultSafariBeta,
		DefaultSafariIOSBeta,
		DefaultChromeAndroid,
		DefaultFirefox,
		DefaultTor,
	}
	allTargets = append(append([]string{}, targetConstants...), defaultConstants...)
)
