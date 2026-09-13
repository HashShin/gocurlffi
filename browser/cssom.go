package browser

import (
	"strconv"
	"strings"

	"github.com/dop251/goja"
)

// The CSS object model, as much of it as pages actually use: enumerating the
// document's stylesheets, reading their rules and their declarations.
//
// This is how a page (or a script checking a page) answers "did my CSS load":
// document.styleSheets[0].href, .cssRules.length, .selectorText. It reports
// what the page itself declared, the same sheets that drive getComputedStyle
// and the renderer.

// styleSheetListObject builds the document.styleSheets array-like.
func (e *jsEnv) styleSheetListObject() goja.Value {
	sheets := e.page.styleSheets()
	o := e.vm.NewArray()
	for i, s := range sheets {
		_ = o.Set(strconv.Itoa(i), e.styleSheetObject(s))
	}
	_ = o.Set("length", len(sheets))
	_ = o.Set("item", func(call goja.FunctionCall) goja.Value {
		i := int(call.Argument(0).ToInteger())
		if i < 0 || i >= len(sheets) {
			return goja.Null()
		}
		return e.styleSheetObject(sheets[i])
	})
	return o
}

func (e *jsEnv) styleSheetObject(s *pageStyleSheet) goja.Value {
	o := e.vm.NewObject()
	if s.href != "" {
		_ = o.Set("href", s.href)
	} else {
		_ = o.Set("href", goja.Null())
	}
	_ = o.Set("type", "text/css")
	_ = o.Set("title", "")
	_ = o.Set("disabled", false)
	_ = o.Set("ownerNode", e.wrap(s.node))
	media := e.vm.NewObject()
	_ = media.Set("mediaText", s.media)
	_ = media.Set("length", mediaCount(s.media))
	_ = o.Set("media", media)
	// cssRules is a property, not a method, in the DOM. It is defined as a
	// getter so that reading an href never parses or fetches a sheet; only
	// reading the rules does.
	getRules := e.vm.ToValue(func(call goja.FunctionCall) goja.Value {
		return e.cssRuleList(s)
	})
	_ = o.DefineAccessorProperty("cssRules", getRules, goja.Undefined(), goja.FLAG_TRUE, goja.FLAG_TRUE)
	_ = o.DefineAccessorProperty("rules", getRules, goja.Undefined(), goja.FLAG_TRUE, goja.FLAG_TRUE)
	return o
}

// cssRuleList parses the sheet on demand, so that merely reading an href never
// fetches anything.
func (e *jsEnv) cssRuleList(s *pageStyleSheet) goja.Value {
	o := e.vm.NewArray()
	if !e.page.loadSheet(s) {
		_ = o.Set("length", 0)
		_ = o.Set("item", func(goja.FunctionCall) goja.Value { return goja.Null() })
		return o
	}
	order := 0
	rules, _, _ := parseCSSStylesheet(s.source, e.page.viewportWidth(), &order)
	for i := range rules {
		_ = o.Set(strconv.Itoa(i), e.cssRuleObject(s, &rules[i]))
	}
	_ = o.Set("length", len(rules))
	_ = o.Set("item", func(call goja.FunctionCall) goja.Value {
		i := int(call.Argument(0).ToInteger())
		if i < 0 || i >= len(rules) {
			return goja.Null()
		}
		return e.cssRuleObject(s, &rules[i])
	})
	return o
}

func (e *jsEnv) cssRuleObject(s *pageStyleSheet, r *cssRule) goja.Value {
	o := e.vm.NewObject()
	_ = o.Set("type", 1) // CSSRule.STYLE_RULE
	_ = o.Set("selectorText", r.text)
	_ = o.Set("parentStyleSheet", nil)
	_ = o.Set("style", e.cssStyleObject(r.decls))
	_ = o.Set("cssText", r.text+" { "+declText(r.decls)+" }")
	// insertRule and deleteRule would have to change the cascade; refusing
	// loudly beats silently doing nothing.
	throw := func(call goja.FunctionCall) goja.Value {
		panic(e.vm.NewTypeError("CSSStyleSheet: this browser does not implement rule mutation"))
	}
	_ = o.Set("insertRule", throw)
	_ = o.Set("deleteRule", throw)
	return o
}

// cssStyleObject exposes a rule's declarations like a CSSStyleDeclaration.
func (e *jsEnv) cssStyleObject(decls []cssDecl) goja.Value {
	o := e.vm.NewObject()
	values := map[string]string{}
	for _, d := range decls {
		values[d.prop] = d.val
		if d.important {
			values[d.prop] += " !important"
		}
	}
	_ = o.Set("length", len(values))
	_ = o.Set("cssText", declText(decls))
	_ = o.Set("getPropertyValue", func(call goja.FunctionCall) goja.Value {
		name := strings.ToLower(strings.TrimSpace(argString(call.Argument(0))))
		return e.vm.ToValue(values[name])
	})
	_ = o.Set("getPropertyPriority", func(call goja.FunctionCall) goja.Value {
		name := strings.ToLower(strings.TrimSpace(argString(call.Argument(0))))
		if v, ok := values[name]; ok && strings.HasSuffix(v, " !important") {
			return e.vm.ToValue("important")
		}
		return e.vm.ToValue("")
	})
	return o
}

func declText(decls []cssDecl) string {
	parts := make([]string, 0, len(decls))
	for _, d := range decls {
		s := d.prop + ": " + d.val
		if d.important {
			s += " !important"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "; ")
}

func mediaCount(media string) int {
	n := 0
	for _, part := range strings.Split(media, ",") {
		if strings.TrimSpace(part) != "" {
			n++
		}
	}
	return n
}
