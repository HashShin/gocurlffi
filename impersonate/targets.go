package impersonate

// Target names every impersonation target as a constant, so a target can be
// written impersonate.Chrome131 and the compiler spells it, rather than as a
// string literal that is only checked at run time.
//
// The values match the preset table in presets.go; TestTargetConstantsResolve
// fails if one stops resolving, which catches a typo or a preset that was
// removed.
//
// The Default* constants in preset.go name the current browser of each family
// and are the better choice when any recent version will do.
type Target = string

const (
	Chrome100        Target = "chrome100"
	Chrome101        Target = "chrome101"
	Chrome104        Target = "chrome104"
	Chrome107        Target = "chrome107"
	Chrome110        Target = "chrome110"
	Chrome116        Target = "chrome116"
	Chrome119        Target = "chrome119"
	Chrome120        Target = "chrome120"
	Chrome123        Target = "chrome123"
	Chrome124        Target = "chrome124"
	Chrome131        Target = "chrome131"
	Chrome131Android Target = "chrome131_android"
	Chrome133a       Target = "chrome133a"
	Chrome136        Target = "chrome136"
	Chrome142        Target = "chrome142"
	Chrome145        Target = "chrome145"
	Chrome146        Target = "chrome146"
	Chrome150        Target = "chrome150"
	Chrome99         Target = "chrome99"
	Chrome99Android  Target = "chrome99_android"
	Custom           Target = "custom"
	Edge101          Target = "edge101"
	Edge99           Target = "edge99"
	Firefox133       Target = "firefox133"
	Firefox135       Target = "firefox135"
	Firefox144       Target = "firefox144"
	Firefox147       Target = "firefox147"
	OkHTTP4Android   Target = "okhttp4_android"
	Safari153        Target = "safari153"
	Safari155        Target = "safari155"
	Safari170        Target = "safari170"
	Safari172Ios     Target = "safari172_ios"
	Safari180        Target = "safari180"
	Safari180Ios     Target = "safari180_ios"
	Safari184        Target = "safari184"
	Safari184Ios     Target = "safari184_ios"
	Safari260        Target = "safari260"
	Safari2601       Target = "safari2601"
	Safari260Ios     Target = "safari260_ios"
	Tor145           Target = "tor145"
)

// namedTargets lists every constant above so a test can prove each one is a
// real target. A constant missing from this list is the only way one can go
// unchecked, so keep it in step when adding a target.
var namedTargets = []Target{
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
