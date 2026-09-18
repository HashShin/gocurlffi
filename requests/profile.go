package requests

import (
	"strconv"
	"strings"

	"github.com/bogdanfinn/fhttp/http2"
	"github.com/bogdanfinn/tls-client/profiles"
	tls "github.com/bogdanfinn/utls"

	"github.com/HashShin/shade/impersonate"
)

// settingIDFor maps a numeric HTTP/2 setting id to the fhttp constant.
func settingIDFor(id uint64) (http2.SettingID, bool) {
	switch id {
	case 1:
		return http2.SettingHeaderTableSize, true
	case 2:
		return http2.SettingEnablePush, true
	case 3:
		return http2.SettingMaxConcurrentStreams, true
	case 4:
		return http2.SettingInitialWindowSize, true
	case 5:
		return http2.SettingMaxFrameSize, true
	case 6:
		return http2.SettingMaxHeaderListSize, true
	case 8:
		return http2.SettingEnableConnectProtocol, true
	case 9:
		return http2.SettingNoRFC7540Priorities, true
	default:
		return 0, false
	}
}

// parseHTTP2Settings turns curl-impersonate's "1:65536;2:0;..." form into the
// fhttp settings map plus the exact setting order.
func parseHTTP2Settings(spec string) (map[http2.SettingID]uint32, []http2.SettingID) {
	settings := map[http2.SettingID]uint32{}
	var order []http2.SettingID
	if spec == "" {
		return nil, nil
	}
	for _, part := range strings.Split(spec, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		id, err := strconv.ParseUint(strings.TrimSpace(k), 10, 16)
		if err != nil {
			continue
		}
		val, err := strconv.ParseUint(strings.TrimSpace(v), 10, 32)
		if err != nil {
			continue
		}
		sid, ok := settingIDFor(id)
		if !ok {
			continue
		}
		settings[sid] = uint32(val)
		order = append(order, sid)
	}
	return settings, order
}

// parseHTTP3Settings turns curl-impersonate's HTTP/3 settings form into the
// transport's representation. GREASE entries are dropped because the transport
// re-adds its own GREASE setting when appropriate.
func parseHTTP3Settings(spec string) (map[uint64]uint64, []uint64) {
	settings := map[uint64]uint64{}
	var order []uint64
	if spec == "" {
		return nil, nil
	}
	for _, part := range strings.Split(spec, ";") {
		part = strings.TrimSpace(part)
		if part == "" || strings.EqualFold(part, "GREASE") {
			continue
		}
		k, v, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		id, err := strconv.ParseUint(strings.TrimSpace(k), 10, 64)
		if err != nil {
			continue
		}
		val, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
		if err != nil {
			continue
		}
		settings[id] = val
		order = append(order, id)
	}
	return settings, order
}

// profileForPreset builds a transport ClientProfile whose TLS ClientHello comes
// from the preset (exactly, for non-permuting presets) and whose HTTP/2 and
// HTTP/3 parameters come from the preset data.
func profileForPreset(p *impersonate.Preset, base profiles.ClientProfile) profiles.ClientProfile {
	if p == nil {
		return base
	}
	helloID := base.GetClientHelloId()

	if order := presetOrder(p); order != "" {
		if _, err := buildSpecForPreset(p, order); err == nil {
			helloID = tls.ClientHelloID{
				Client:               "custom",
				RandomExtensionOrder: false,
				Version:              p.Target,
				// Build the spec on every handshake. Returning a shared spec
				// (even by value) shares its extension pointers, and utls
				// mutates those during the handshake, so a second connection
				// would send a corrupted ClientHello.
				SpecFactory: func() (tls.ClientHelloSpec, error) {
					spec, err := buildSpecForPreset(p, order)
					if err != nil {
						return tls.ClientHelloSpec{}, err
					}
					return *spec, nil
				},
			}
		}
	}

	settings, settingsOrder := parseHTTP2Settings(p.HTTP2Settings)
	if settings == nil {
		settings = base.GetSettings()
		settingsOrder = base.GetSettingsOrder()
	}

	pseudo := p.PseudoHeaderOrder()
	if len(pseudo) == 0 {
		pseudo = base.GetPseudoHeaderOrder()
	}

	flow := uint32(p.HTTP2WindowUpdate)
	if flow == 0 {
		flow = base.GetConnectionFlow()
	}

	var headerPriority *http2.PriorityParam
	if p.HTTP2StreamWeight > 0 {
		w := p.HTTP2StreamWeight - 1
		if w < 0 {
			w = 0
		}
		if w > 255 {
			w = 255
		}
		headerPriority = &http2.PriorityParam{
			StreamDep: 0,
			Exclusive: p.HTTP2StreamExclusive != 0,
			Weight:    uint8(w),
		}
	} else {
		headerPriority = base.GetHeaderPriority()
	}

	h3settings, h3order := parseHTTP3Settings(p.HTTP3Settings)
	if h3settings == nil {
		h3settings = base.GetHttp3Settings()
		h3order = base.GetHttp3SettingsOrder()
	}
	h3pseudo := p.HTTP3PseudoHeaderOrderList()
	if len(h3pseudo) == 0 {
		h3pseudo = base.GetHttp3PseudoHeaderOrder()
	}

	return profiles.NewClientProfile(
		helloID,
		settings,
		settingsOrder,
		pseudo,
		flow,
		base.GetPriorities(),
		headerPriority,
		base.GetStreamID(),
		base.GetAllowHTTP(),
		h3settings,
		h3order,
		base.GetHttp3PriorityParam(),
		h3pseudo,
		base.GetHttp3SendGreaseFrames(),
	)
}
