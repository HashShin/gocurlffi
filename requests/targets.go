package requests

import "github.com/HashShin/shade/impersonate"

// Impersonation targets, re-exported from the impersonate package so that a
// request needs one import rather than two:
//
//	sess.Send(requests.Request{
//		URL:         "https://httpbun.com/get",
//		Impersonate: requests.Chrome146,
//	})
//
// These are aliases of the constants in impersonate/targets.go, not a second
// copy of the values, so a preset change reaches both. TestTargetsMatchImpersonate
// fails if a target is added there and not here.
//
// The Impersonate field is a plain string, so "chrome146" works too. Prefer a
// constant: a misspelt string is only caught when the request is made, whereas
// a misspelt constant does not compile. impersonate.Get reports an unknown
// target as an *ImpersonateError.
const (
	Chrome100        = impersonate.Chrome100
	Chrome101        = impersonate.Chrome101
	Chrome104        = impersonate.Chrome104
	Chrome107        = impersonate.Chrome107
	Chrome110        = impersonate.Chrome110
	Chrome116        = impersonate.Chrome116
	Chrome119        = impersonate.Chrome119
	Chrome120        = impersonate.Chrome120
	Chrome123        = impersonate.Chrome123
	Chrome124        = impersonate.Chrome124
	Chrome131        = impersonate.Chrome131
	Chrome131Android = impersonate.Chrome131Android
	Chrome133a       = impersonate.Chrome133a
	Chrome136        = impersonate.Chrome136
	Chrome142        = impersonate.Chrome142
	Chrome145        = impersonate.Chrome145
	Chrome146        = impersonate.Chrome146
	Chrome150        = impersonate.Chrome150
	Chrome99         = impersonate.Chrome99
	Chrome99Android  = impersonate.Chrome99Android
	Custom           = impersonate.Custom
	Edge101          = impersonate.Edge101
	Edge99           = impersonate.Edge99
	Firefox133       = impersonate.Firefox133
	Firefox135       = impersonate.Firefox135
	Firefox144       = impersonate.Firefox144
	Firefox147       = impersonate.Firefox147
	OkHTTP4Android   = impersonate.OkHTTP4Android
	Safari153        = impersonate.Safari153
	Safari155        = impersonate.Safari155
	Safari170        = impersonate.Safari170
	Safari172Ios     = impersonate.Safari172Ios
	Safari180        = impersonate.Safari180
	Safari180Ios     = impersonate.Safari180Ios
	Safari184        = impersonate.Safari184
	Safari184Ios     = impersonate.Safari184Ios
	Safari260        = impersonate.Safari260
	Safari2601       = impersonate.Safari2601
	Safari260Ios     = impersonate.Safari260Ios
	Tor145           = impersonate.Tor145
)

// allTargets lists every constant above, so a test can check the set against
// the impersonate package rather than trusting the two lists to stay in step.
// The exported list is impersonate.Targets().
var allTargets = []string{
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

// The family defaults: the target each short alias resolves to, so "chrome"
// resolves to DefaultChrome. These are the better choice for an example or a
// default setting, because they follow the family as the presets are updated,
// where naming an exact version pins a browser that will age.
const (
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
