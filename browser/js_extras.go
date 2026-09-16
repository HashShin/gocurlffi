package browser

import (
	"strconv"

	"github.com/dop251/goja"
)

// The navigator members every Chrome carries that this environment did not, and
// a real AudioContext. Each is a "falsey environment" signal: not a fingerprint,
// just something a page can read to see that the object in front of it is
// incomplete. navigator.permissions is worth having on its own - the usual
// stealth scripts patch it, so it is known to be read.

// installNavigatorExtras adds the missing navigator surface.
func (e *jsEnv) installNavigatorExtras(nav *goja.Object) {
	_ = nav.Set("pdfViewerEnabled", true)
	_ = nav.Set("doNotTrack", goja.Null())
	_ = nav.Set("globalPrivacyControl", false)

	// permissions.query resolves {state, onchange}. The name decides the state
	// a browser would report for an ordinary page.
	perm := e.vm.NewObject()
	_ = perm.Set("query", func(call goja.FunctionCall) goja.Value {
		name := ""
		if o, ok := call.Argument(0).(*goja.Object); ok && o != nil {
			name = argString(o.Get("name"))
		}
		state := "prompt"
		switch name {
		case "geolocation", "notifications", "camera", "microphone":
			state = "prompt"
		case "clipboard-read", "clipboard-write":
			state = "prompt"
		}
		st := e.vm.NewObject()
		_ = st.Set("state", state)
		_ = st.Set("onchange", goja.Null())
		return e.resolvedPromise(st)
	})
	e.tagObject(perm, "Permissions")
	_ = nav.Set("permissions", perm)

	// NetworkInformation.
	conn := e.vm.NewObject()
	_ = conn.Set("effectiveType", "4g")
	_ = conn.Set("type", "wifi")
	_ = conn.Set("rtt", 50)
	_ = conn.Set("downlink", 10)
	_ = conn.Set("downlinkMax", 0)
	_ = conn.Set("saveData", false)
	_ = conn.Set("onchange", goja.Null())
	e.tagObject(conn, "NetworkInformation")
	_ = nav.Set("connection", conn)
}

// audioNode builds a node-shaped object with the members an audio fingerprint
// script reaches for.
func (e *jsEnv) audioNode(iface string, inputs, outputs int) *goja.Object {
	o := e.vm.NewObject()
	_ = o.Set("numberOfInputs", inputs)
	_ = o.Set("numberOfOutputs", outputs)
	_ = o.Set("channelCount", 2)
	_ = o.Set("channelCountMode", "max")
	_ = o.Set("channelInterpretation", "speakers")
	_ = o.Set("connect", func(call goja.FunctionCall) goja.Value { return call.Argument(0) })
	_ = o.Set("disconnect", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	_ = o.Set("context", goja.Null())
	e.tagHost(o, iface)
	_ = o.DefineDataProperty("constructor", e.namedConstructor(iface, nil),
		goja.FLAG_TRUE, goja.FLAG_FALSE, goja.FLAG_TRUE)
	return o
}

// audioContextObject is an AudioContext. Chrome always has one, and an audio
// fingerprint is built from it, so its absence is both a tell and a gap.
func (e *jsEnv) audioContextObject() *goja.Object {
	o := e.vm.NewObject()
	_ = o.Set("sampleRate", 44100)
	_ = o.Set("currentTime", 0)
	_ = o.Set("state", "running")
	_ = o.Set("baseLatency", 0.01)
	_ = o.Set("outputLatency", 0.02)
	_ = o.Set("destination", e.audioNode("AudioDestinationNode", 0, 1))
	_ = o.Set("listener", e.audioNode("AudioListener", 0, 0))

	node := func(name, iface string, extra func(*goja.Object)) func(goja.FunctionCall) goja.Value {
		return func(goja.FunctionCall) goja.Value {
			n := e.audioNode(iface, 0, 1)
			if extra != nil {
				extra(n)
			}
			return n
		}
	}
	_ = o.Set("createOscillator", node("createOscillator", "OscillatorNode", func(n *goja.Object) {
		_ = n.Set("type", "sine")
		_ = n.Set("frequency", e.audioParam(440))
		_ = n.Set("detune", e.audioParam(0))
		_ = n.Set("start", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
		_ = n.Set("stop", func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	}))
	_ = o.Set("createGain", node("createGain", "GainNode", func(n *goja.Object) {
		_ = n.Set("gain", e.audioParam(1))
	}))
	analyser := node("createAnalyser", "AnalyserNode", func(n *goja.Object) {
		_ = n.Set("fftSize", 2048)
		_ = n.Set("frequencyBinCount", 1024)
		_ = n.Set("minDecibels", -100)
		_ = n.Set("maxDecibels", -30)
		_ = n.Set("smoothingTimeConstant", 0.8)
		_ = n.Set("getFloatFrequencyData", func(call goja.FunctionCall) goja.Value {
			if a, ok := call.Argument(0).(*goja.Object); ok && a != nil {
				n := int(a.Get("length").ToInteger())
				for i := 0; i < n; i++ {
					_ = a.Set(strconv.Itoa(i), -100.0)
				}
			}
			return goja.Undefined()
		})
		_ = n.Set("getByteFrequencyData", func(call goja.FunctionCall) goja.Value {
			if a, ok := call.Argument(0).(*goja.Object); ok && a != nil {
				n := int(a.Get("length").ToInteger())
				for i := 0; i < n; i++ {
					_ = a.Set(strconv.Itoa(i), 0)
				}
			}
			return goja.Undefined()
		})
	})
	_ = o.Set("createAnalyser", analyser)
	_ = o.Set("createDynamicsCompressor", node("createDynamicsCompressor", "DynamicsCompressorNode", func(n *goja.Object) {
		_ = n.Set("threshold", e.audioParam(-24))
		_ = n.Set("knee", e.audioParam(30))
		_ = n.Set("ratio", e.audioParam(12))
		_ = n.Set("attack", e.audioParam(0.003))
		_ = n.Set("release", e.audioParam(0.25))
		_ = n.Set("reduction", 0)
	}))
	_ = o.Set("createScriptProcessor", node("createScriptProcessor", "ScriptProcessorNode", nil))
	_ = o.Set("createBufferSource", node("createBufferSource", "AudioBufferSourceNode", nil))
	_ = o.Set("createBuffer", func(call goja.FunctionCall) goja.Value {
		b := e.vm.NewObject()
		_ = b.Set("numberOfChannels", int(call.Argument(0).ToInteger()))
		_ = b.Set("length", int(call.Argument(1).ToInteger()))
		_ = b.Set("sampleRate", int(call.Argument(2).ToInteger()))
		_ = b.Set("duration", 0)
		_ = b.Set("getChannelData", func(goja.FunctionCall) goja.Value { return e.newUint8Array(nil) })
		e.tagHost(b, "AudioBuffer")
		return b
	})
	for _, m := range []string{"close", "resume", "suspend"} {
		_ = o.Set(m, func(goja.FunctionCall) goja.Value { return e.resolvedPromise(goja.Undefined()) })
	}
	e.tagHost(o, "AudioContext")
	_ = o.DefineDataProperty("constructor", e.namedConstructor("AudioContext", nil),
		goja.FLAG_TRUE, goja.FLAG_FALSE, goja.FLAG_TRUE)
	return o
}

// audioParam is an AudioParam with a fixed value.
func (e *jsEnv) audioParam(value float64) *goja.Object {
	o := e.vm.NewObject()
	_ = o.Set("value", value)
	_ = o.Set("defaultValue", value)
	_ = o.Set("minValue", -3.4028235e38)
	_ = o.Set("maxValue", 3.4028235e38)
	for _, m := range []string{"setValueAtTime", "linearRampToValueAtTime",
		"exponentialRampToValueAtTime", "setTargetAtTime", "cancelScheduledValues"} {
		_ = o.Set(m, func(goja.FunctionCall) goja.Value { return goja.Undefined() })
	}
	e.tagHost(o, "AudioParam")
	return o
}

// installAudioContexts publishes the AudioContext constructors. Chrome also
// exposes the webkit alias, and every one of them must actually build a context
// rather than a bare object.
func (e *jsEnv) installAudioContexts() {
	ctor := func(name string) *goja.Object {
		fn := e.vm.ToValue(func(call goja.ConstructorCall) *goja.Object {
			return e.audioContextObject()
		}).(*goja.Object)
		_ = fn.DefineDataProperty("name", e.vm.ToValue(name), goja.FLAG_FALSE, goja.FLAG_TRUE, goja.FLAG_TRUE)
		return fn
	}
	_ = e.vm.Set("AudioContext", ctor("AudioContext"))
	_ = e.vm.Set("webkitAudioContext", ctor("webkitAudioContext"))
	_ = e.vm.Set("OfflineAudioContext", ctor("OfflineAudioContext"))
}
