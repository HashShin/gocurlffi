package cli

// Shared flag plumbing. Both command paths parse with the standard flag
// package and share one set of helpers, so a flag behaves the same wherever it
// appears and the usage text is generated from the definitions rather than
// maintained beside them.

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// Exit codes. The process status is returned rather than set with os.Exit so
// that Main stays callable from tests and from a parent process.
const (
	exitOK    = 0 // success
	exitError = 1 // the command ran and failed
	exitUsage = 2 // the arguments were wrong, nothing was attempted
)

// stringList is a repeatable flag that keeps each value verbatim. It backs -H,
// --block, --click, --type, --fill and --select, all of which may be given more
// than once and are order-sensitive.
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ", ") }

func (l *stringList) Set(v string) error {
	*l = append(*l, v)
	return nil
}

// driveStep is one page interaction, kept in the order its flag was given: a
// flow is scripted from the command line, so --fill before --click has to run
// before it.
type driveStep struct {
	kind  string // click, type, fill or select
	value string
}

// driveFlags are the page interactions of the browser path. Each kind keeps its
// own list, which the generated flag help prints, and they share one ordered
// log, which the driver runs.
type driveFlags struct {
	clicks  stringList
	types   stringList
	fills   stringList
	selects stringList
	steps   []driveStep
}

// value returns the flag.Value for one kind, appending to both the kind's list
// and the ordered log.
func (d *driveFlags) value(kind string, list *stringList) flag.Value {
	return driveValue{drive: d, kind: kind, list: list}
}

type driveValue struct {
	drive *driveFlags
	kind  string
	list  *stringList
}

func (v driveValue) String() string { return v.list.String() }

func (v driveValue) Set(s string) error {
	*v.list = append(*v.list, s)
	v.drive.steps = append(v.drive.steps, driveStep{kind: v.kind, value: s})
	return nil
}

// newFlagSet builds a flag set that prints its own usage. Parsing stops at the
// first problem and reports it instead of exiting, so the caller decides the
// exit status. The synopsis and invocation are shown above the generated flag
// list, which is the only place flags are described.
func newFlagSet(name, invocation, synopsis string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "%s\n\nusage:\n  %s\n\nflags:\n", synopsis, invocation)
		fs.PrintDefaults()
	}
	return fs
}

// parse parses args, reordering them first so options may follow the positional
// URL. flag.ErrHelp is returned for -h/--help, which the caller treats as
// success rather than a usage error.
func parse(fs *flag.FlagSet, args []string) error {
	return fs.Parse(reorderFlags(args, boolFlagNames(fs)))
}

// boolFlagNames reports the flags that take no value, read from the flag set
// itself so a new boolean option never has to be registered twice. Without it,
// reorderFlags would treat the argument after a boolean flag as its value:
// "get URL --sheets -f text" tried to fetch the host "text".
func boolFlagNames(fs *flag.FlagSet) map[string]bool {
	names := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) {
		switch f.Value.String() {
		case "true", "false":
			names["-"+f.Name] = true
			names["--"+f.Name] = true
		}
	})
	return names
}

// reorderFlags moves options ahead of positional arguments so the standard
// flag package (which stops at the first non-flag) sees them, allowing
// "shade get URL -f text". boolFlags holds the options that take no value.
func reorderFlags(args []string, boolFlags map[string]bool) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && !boolFlags[a] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positional = append(positional, a)
	}
	return append(flags, positional...)
}
