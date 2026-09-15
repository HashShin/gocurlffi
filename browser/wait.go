package browser

import (
	"fmt"
	"time"
)

// Waiting. A synchronous loader gets a page as far as its own timers and script
// budget allow, which is not always far enough: a page that fetches its data and
// renders on a later turn needs to be waited for explicitly. These are the waits
// a driver exposes, and the CLI maps its flags onto them.
//
// Options.WaitUntil decides how far the load itself goes:
//
//	"load"              (default) scripts, DOMContentLoaded, load, then the
//	                    timer budget -- the existing behaviour
//	"domcontentloaded"  stop once DOMContentLoaded has fired and its timers ran
//	"networkidle0"      after load, keep draining until nothing is pending
//
// The waits below then run on top of whatever the load reached.

// WaitUntilNames are the accepted Options.WaitUntil values.
var WaitUntilNames = []string{"load", "domcontentloaded", "networkidle0"}

// normalizeWaitUntil maps a user value to a known one, defaulting to load.
func normalizeWaitUntil(v string) string {
	switch v {
	case "domcontentloaded", "DOMContentLoaded":
		return "domcontentloaded"
	case "networkidle0", "networkidle":
		return "networkidle0"
	default:
		return "load"
	}
}

// pendingWork counts the work the page still has queued: un-cleared timers, open
// sockets and live workers. Zero means the page has gone quiet.
func (p *Page) pendingWork() int {
	if p.env == nil {
		return 0
	}
	return p.env.pendingWork()
}

func (e *jsEnv) pendingWork() int {
	n := 0
	for _, t := range e.timers {
		if !t.cleared {
			n++
		}
	}
	if e.hasOpenWebSocket() {
		n++
	}
	if e.hasLiveWorker() {
		n++
	}
	return n
}

// WaitForTime advances the page by at least d, running timers as their turn
// comes. It is --wait-ms: a page whose content appears on a setTimeout needs the
// clock moved, and sleeping alone would not run the callback.
func (p *Page) WaitForTime(d time.Duration) {
	if p.env == nil || d <= 0 {
		return
	}
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		p.env.runTimers(1000)
		if p.env.pendingWork() == 0 {
			// Nothing left to run, so the rest of the wait is a plain sleep.
			if remaining := time.Until(deadline); remaining > 0 {
				time.Sleep(remaining)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.env.runTimers(1000)
}

// WaitForScript evaluates script repeatedly until it returns a truthy value,
// which is how a page signals that it has finished rendering. It is
// --wait-script. The timeout bounds the wait; an error from the script is
// returned rather than retried, because a broken expression never becomes true.
func (p *Page) WaitForScript(script string, timeout time.Duration) error {
	if p.env == nil {
		return errNoScriptEnv
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		v, err := p.Eval(script)
		if err != nil {
			return fmt.Errorf("wait-script: %w", err)
		}
		// undefined and null are both falsy, so no separate nil check is needed.
		if v != nil && v.ToBoolean() {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("wait-script: condition was still false after %s", timeout)
		}
		p.env.runTimers(50)
		time.Sleep(20 * time.Millisecond)
	}
}

// WaitForNetworkIdle drains the page until nothing has been pending for the
// quiet period, or the timeout expires. It is --wait-until networkidle0. The
// timeout is not an error: a page holding a socket open legitimately never goes
// idle, and the caller still wants whatever it rendered.
func (p *Page) WaitForNetworkIdle(quiet, timeout time.Duration) {
	if p.env == nil {
		return
	}
	if quiet <= 0 {
		quiet = 500 * time.Millisecond
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	deadline := time.Now().Add(timeout)
	var idleSince time.Time
	for time.Now().Before(deadline) {
		p.env.runTimers(1000)
		if p.pendingWork() > 0 {
			idleSince = time.Time{}
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if idleSince.IsZero() {
			idleSince = time.Now()
		}
		if time.Since(idleSince) >= quiet {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
