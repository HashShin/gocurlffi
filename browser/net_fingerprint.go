package browser

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"
)

// This file builds the parts of the environment a browser exposes for
// feature-detection and fingerprinting rather than for rendering: the
// window.chrome namespace, the PDF plugin and mime-type lists, the client-hint
// brand list, and Intl. None of it changes a page's layout, but a script that
// reads one of these and finds nothing - or finds something that contradicts the
// User-Agent it was given - has a strong signal that it is not talking to a
// browser.

// defaultTimeZone is what Intl reports. A specific region would contradict the
// network the request actually comes from, so this stays at UTC.
const defaultTimeZone = "UTC"

var chromeVersionRe = regexp.MustCompile(`(?:Chrome|Chromium)/(\d+)`)

// uaVendor is the navigator.vendor that agrees with the impersonated UA.
// Chrome answers "Google Inc."; an empty string beside a Chrome UA is a
// mismatch a fingerprinting script can see without any other probe.
func uaVendor(ua string) string {
	switch {
	case strings.Contains(ua, "Chrome/") || strings.Contains(ua, "Chromium/"):
		return "Google Inc."
	case strings.Contains(ua, "Safari/"):
		return "Apple Computer, Inc."
	}
	return ""
}

// uaDataPlatform is the navigator.userAgentData.platform value for a UA.
func uaDataPlatform(ua string) string {
	switch {
	case strings.Contains(ua, "Android"):
		return "Android"
	case strings.Contains(ua, "Windows"):
		return "Windows"
	case strings.Contains(ua, "Macintosh") || strings.Contains(ua, "Mac OS X"):
		return "macOS"
	case strings.Contains(ua, "Linux"):
		return "Linux"
	}
	return ""
}

// uaFullVersion is the four-part browser version, padded the way Chrome pads it.
func uaFullVersion(ua string) string {
	m := chromeVersionRe.FindStringSubmatch(ua)
	if m == nil {
		return ""
	}
	return m[1] + ".0.0.0"
}

// greaseMajor derives the deliberately meaningless "Not_A Brand" version that
// Chrome reports alongside the real brand. It is not validated against the
// major version, but it does advance with it, and the sequence starts at 0 for
// Chrome 107.
func greaseMajor(ua string) string {
	m := chromeVersionRe.FindStringSubmatch(ua)
	if m == nil {
		return "0"
	}
	major, err := strconv.Atoi(m[1])
	if err != nil || major < 107 {
		return "0"
	}
	return strconv.Itoa(major - 107)
}

// uaBrands returns the low- and high-entropy brand lists for a UA.
func uaBrands(ua string) (brands, full []map[string]string) {
	m := chromeVersionRe.FindStringSubmatch(ua)
	if m == nil {
		return []map[string]string{}, []map[string]string{}
	}
	v, gv := m[1], greaseMajor(ua)
	brands = []map[string]string{
		{"brand": "Not_A Brand", "version": gv},
		{"brand": "Chromium", "version": v},
		{"brand": "Google Chrome", "version": v},
	}
	full = []map[string]string{
		{"brand": "Not_A Brand", "version": gv + ".0.0.0"},
		{"brand": "Chromium", "version": v + ".0.0.0"},
		{"brand": "Google Chrome", "version": v + ".0.0.0"},
	}
	return brands, full
}

// platformVersionFor is the client-hint platform version for a UA.
func platformVersionFor(ua string) string {
	switch {
	case strings.Contains(ua, "Android 10"):
		return "10"
	case strings.Contains(ua, "Windows"):
		return "10.0.0"
	case strings.Contains(ua, "Mac OS X 10_15"):
		return "15.7.0"
	}
	return ""
}

// highEntropyValues answers navigator.userAgentData.getHighEntropyValues,
// returning exactly the hints that were asked for.
func (e *jsEnv) highEntropyValues(hints goja.Value, ua string, brands, full []map[string]string, mobile bool) goja.Value {
	o := e.vm.NewObject()
	if !hintRequested(hints, "brands") && !hintRequested(hints, "fullVersionList") &&
		!hintRequested(hints, "mobile") && !hintRequested(hints, "platform") &&
		!hintRequested(hints, "platformVersion") && !hintRequested(hints, "architecture") &&
		!hintRequested(hints, "bitness") && !hintRequested(hints, "model") &&
		!hintRequested(hints, "uaFullVersion") && !hintRequested(hints, "wow64") {
		return o
	}
	if hintRequested(hints, "brands") {
		_ = o.Set("brands", brands)
	}
	if hintRequested(hints, "fullVersionList") {
		_ = o.Set("fullVersionList", full)
	}
	if hintRequested(hints, "mobile") {
		_ = o.Set("mobile", mobile)
	}
	if hintRequested(hints, "platform") {
		_ = o.Set("platform", uaDataPlatform(ua))
	}
	if hintRequested(hints, "platformVersion") {
		_ = o.Set("platformVersion", platformVersionFor(ua))
	}
	if hintRequested(hints, "architecture") {
		_ = o.Set("architecture", "x86")
	}
	if hintRequested(hints, "bitness") {
		_ = o.Set("bitness", "64")
	}
	if hintRequested(hints, "model") {
		_ = o.Set("model", "")
	}
	if hintRequested(hints, "uaFullVersion") {
		_ = o.Set("uaFullVersion", uaFullVersion(ua))
	}
	if hintRequested(hints, "wow64") {
		_ = o.Set("wow64", false)
	}
	return o
}

// hintRequested reports whether the hint array passed to getHighEntropyValues
// names one hint.
func hintRequested(hints goja.Value, name string) bool {
	obj, ok := hints.(*goja.Object)
	if !ok || obj == nil {
		return false
	}
	lv := obj.Get("length")
	if lv == nil || goja.IsUndefined(lv) {
		return false
	}
	n := int(lv.ToInteger())
	for i := 0; i < n; i++ {
		if argString(obj.Get(strconv.Itoa(i))) == name {
			return true
		}
	}
	return false
}

// pluginEntries is the PDF viewer set a stock Chrome reports. An empty
// navigator.plugins is one of the oldest automation tells there is; Google's own
// interstitial carries a stealth script that patches it for exactly that reason.
var pluginEntries = []struct{ name, filename, description string }{
	{"PDF Viewer", "internal-pdf-viewer", "Portable Document Format"},
	{"Chrome PDF Viewer", "internal-pdf-viewer", "Portable Document Format"},
	{"Chromium PDF Viewer", "internal-pdf-viewer", "Portable Document Format"},
	{"Microsoft Edge PDF Viewer", "internal-pdf-viewer", "Portable Document Format"},
	{"WebKit built-in PDF", "internal-pdf-viewer", "Portable Document Format"},
}

var mimeTypeEntries = []struct{ typ, suffixes, description string }{
	{"application/pdf", "pdf", "Portable Document Format"},
	{"text/pdf", "pdf", "Portable Document Format"},
}

func (e *jsEnv) mimeTypeObject(i int) goja.Value {
	o := e.vm.NewObject()
	_ = o.Set("type", mimeTypeEntries[i].typ)
	_ = o.Set("suffixes", mimeTypeEntries[i].suffixes)
	_ = o.Set("description", mimeTypeEntries[i].description)
	_ = o.Set("enabledPlugin", goja.Null())
	return o
}

// pluginArray builds navigator.plugins: array-like, each entry carrying the
// mime types it handles, with the item()/namedItem() accessors a PluginArray
// has.
func (e *jsEnv) pluginArray() goja.Value {
	items := make([]interface{}, 0, len(pluginEntries))
	for _, pe := range pluginEntries {
		p := e.vm.NewObject()
		_ = p.Set("name", pe.name)
		_ = p.Set("filename", pe.filename)
		_ = p.Set("description", pe.description)
		_ = p.Set("length", len(mimeTypeEntries))
		for i := range mimeTypeEntries {
			_ = p.Set(strconv.Itoa(i), e.mimeTypeObject(i))
		}
		_ = p.Set("item", func(call goja.FunctionCall) goja.Value {
			i := int(call.Argument(0).ToInteger())
			if i < 0 || i >= len(mimeTypeEntries) {
				return goja.Null()
			}
			return e.mimeTypeObject(i)
		})
		_ = p.Set("namedItem", func(call goja.FunctionCall) goja.Value {
			want := argString(call.Argument(0))
			for i, m := range mimeTypeEntries {
				if m.typ == want {
					return e.mimeTypeObject(i)
				}
			}
			return goja.Null()
		})
		items = append(items, p)
	}
	arr := e.vm.NewArray(items...)
	_ = arr.Set("item", func(call goja.FunctionCall) goja.Value {
		i := int(call.Argument(0).ToInteger())
		if i < 0 || i >= len(pluginEntries) {
			return goja.Null()
		}
		return arr.Get(strconv.Itoa(i))
	})
	_ = arr.Set("namedItem", func(call goja.FunctionCall) goja.Value {
		want := argString(call.Argument(0))
		for i, pe := range pluginEntries {
			if pe.name == want {
				return arr.Get(strconv.Itoa(i))
			}
		}
		return goja.Null()
	})
	return arr
}

// mimeTypeArray builds navigator.mimeTypes.
func (e *jsEnv) mimeTypeArray() goja.Value {
	items := make([]interface{}, 0, len(mimeTypeEntries))
	for i := range mimeTypeEntries {
		items = append(items, e.mimeTypeObject(i))
	}
	arr := e.vm.NewArray(items...)
	_ = arr.Set("item", func(call goja.FunctionCall) goja.Value {
		i := int(call.Argument(0).ToInteger())
		if i < 0 || i >= len(mimeTypeEntries) {
			return goja.Null()
		}
		return e.mimeTypeObject(i)
	})
	_ = arr.Set("namedItem", func(call goja.FunctionCall) goja.Value {
		want := argString(call.Argument(0))
		for i, m := range mimeTypeEntries {
			if m.typ == want {
				return e.mimeTypeObject(i)
			}
		}
		return goja.Null()
	})
	return arr
}

// chromeObject is the window.chrome namespace every Chrome build exposes. Its
// absence is a long-standing automation tell, and it is the first thing the
// usual stealth scripts install.
func (e *jsEnv) chromeObject() *goja.Object {
	o := e.vm.NewObject()
	runtime := e.vm.NewObject()
	_ = runtime.Set("id", goja.Undefined())
	_ = o.Set("runtime", runtime)
	_ = o.Set("loadTimes", func(goja.FunctionCall) goja.Value { return e.vm.NewObject() })
	_ = o.Set("csi", func(goja.FunctionCall) goja.Value { return e.vm.NewObject() })
	app := e.vm.NewObject()
	_ = app.Set("isInstalled", false)
	_ = app.Set("InstallState", map[string]int{"DISABLED": 0, "INSTALLED": 1, "NOT_INSTALLED": 2})
	_ = app.Set("RunningState", map[string]int{"CANNOT_RUN": 0, "READY_TO_RUN": 1, "RUNNING": 2})
	_ = o.Set("app", app)
	return o
}

// intlObject is a compact Intl. goja does not ship one, and a ReferenceError on
// `Intl` aborts whatever script touched it. The time zone in particular is read
// by nearly every fingerprinting library.
func (e *jsEnv) intlObject() *goja.Object {
	o := e.vm.NewObject()

	dateTimeFormat := e.vm.ToValue(func(call goja.ConstructorCall) *goja.Object {
		this := call.This
		_ = this.Set("resolvedOptions", func(goja.FunctionCall) goja.Value {
			r := e.vm.NewObject()
			_ = r.Set("locale", "en-US")
			_ = r.Set("timeZone", defaultTimeZone)
			_ = r.Set("calendar", "gregory")
			_ = r.Set("numberingSystem", "latn")
			_ = r.Set("hourCycle", "h23")
			return r
		})
		_ = this.Set("format", func(call goja.FunctionCall) goja.Value {
			return e.vm.ToValue(intlFormatDate(call.Argument(0)))
		})
		_ = this.Set("formatToParts", func(call goja.FunctionCall) goja.Value {
			part := e.vm.NewObject()
			_ = part.Set("type", "literal")
			_ = part.Set("value", intlFormatDate(call.Argument(0)))
			return e.vm.NewArray(part)
		})
		_ = this.Set("resolvedOptions", this.Get("resolvedOptions"))
		return this
	}).(*goja.Object)
	_ = dateTimeFormat.Set("prototype", e.vm.NewObject())
	_ = e.vm.Set("__DateTimeFormat", dateTimeFormat)
	_ = o.Set("DateTimeFormat", dateTimeFormat)

	numberFormat := e.vm.ToValue(func(call goja.ConstructorCall) *goja.Object {
		this := call.This
		_ = this.Set("format", func(call goja.FunctionCall) goja.Value {
			return e.vm.ToValue(intlFormatNumber(call.Argument(0)))
		})
		_ = this.Set("resolvedOptions", func(goja.FunctionCall) goja.Value {
			r := e.vm.NewObject()
			_ = r.Set("locale", "en-US")
			_ = r.Set("numberingSystem", "latn")
			_ = r.Set("style", "decimal")
			return r
		})
		return this
	}).(*goja.Object)
	_ = e.vm.Set("__NumberFormat", numberFormat)
	_ = o.Set("NumberFormat", numberFormat)

	collator := e.vm.ToValue(func(call goja.ConstructorCall) *goja.Object {
		this := call.This
		_ = this.Set("compare", func(call goja.FunctionCall) goja.Value {
			a, b := argString(call.Argument(0)), argString(call.Argument(1))
			switch {
			case a < b:
				return e.vm.ToValue(-1)
			case a > b:
				return e.vm.ToValue(1)
			}
			return e.vm.ToValue(0)
		})
		_ = this.Set("resolvedOptions", func(goja.FunctionCall) goja.Value { return e.vm.NewObject() })
		return this
	}).(*goja.Object)
	_ = e.vm.Set("__Collator", collator)
	_ = o.Set("Collator", collator)

	_ = o.Set("getCanonicalLocales", func(call goja.FunctionCall) goja.Value {
		obj, ok := call.Argument(0).(*goja.Object)
		if !ok {
			return e.vm.NewArray()
		}
		lv := obj.Get("length")
		n := 0
		if lv != nil && !goja.IsUndefined(lv) {
			n = int(lv.ToInteger())
		}
		out := make([]interface{}, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, argString(obj.Get(strconv.Itoa(i))))
		}
		return e.vm.NewArray(out...)
	})
	return o
}

// intlFormatDate renders a Date the way a default-locale formatter would.
func intlFormatDate(v goja.Value) string {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return "Invalid Date"
	}
	if n, ok := v.Export().(int64); ok {
		return time.UnixMilli(n).UTC().Format("1/2/2006")
	}
	if f, ok := v.Export().(float64); ok {
		return time.UnixMilli(int64(f)).UTC().Format("1/2/2006")
	}
	s := v.String()
	if s == "" || s == "Invalid Date" {
		return "Invalid Date"
	}
	return s
}

// intlFormatNumber renders a number the way a default-locale formatter would.
func intlFormatNumber(v goja.Value) string {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return "NaN"
	}
	f := v.ToFloat()
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return fmt.Sprintf("%g", f)
}
