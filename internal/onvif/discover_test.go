// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package onvif

import (
	"net"
	"testing"
)

// tapoC211 is the ProbeMatches a Tapo C211 sent (trimmed, uuid changed).
const tapoC211 = `<?xml version="1.0" encoding="UTF-8"?>
<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://www.w3.org/2003/05/soap-envelope" xmlns:wsa="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:wsdd="http://schemas.xmlsoap.org/ws/2005/04/discovery" xmlns:tdn="http://www.onvif.org/ver10/network/wsdl">
<SOAP-ENV:Body><wsdd:ProbeMatches><wsdd:ProbeMatch>
<wsa:EndpointReference><wsa:Address>uuid:00000000-0000-4000-8000-000000000000</wsa:Address></wsa:EndpointReference>
<wsdd:Types>tdn:NetworkVideoTransmitter</wsdd:Types>
<wsdd:Scopes>onvif://www.onvif.org/name/C211 onvif://www.onvif.org/hardware/C211 onvif://www.onvif.org/Profile/Streaming onvif://www.onvif.org/location/Hong%20Kong onvif://www.onvif.org/type/NetworkVideoTransmitter</wsdd:Scopes>
<wsdd:XAddrs>http://192.168.0.60:2020/onvif/device_service</wsdd:XAddrs>
</wsdd:ProbeMatch></wsdd:ProbeMatches></SOAP-ENV:Body></SOAP-ENV:Envelope>`

func TestParseProbeMatch(t *testing.T) {
	ip := net.ParseIP("192.168.0.60")
	d, ok := parseProbeMatch([]byte(tapoC211), ip)
	if !ok || d != (Device{Host: "192.168.0.60", Port: 2020, Name: "C211", Model: "C211"}) {
		t.Fatalf("got %+v %v", d, ok)
	}
	// An answer may not point at another host: one device must not be able
	// to list (and get credentials typed in for) some other address.
	if _, ok := parseProbeMatch([]byte(tapoC211), net.ParseIP("192.168.0.99")); ok {
		t.Error("accepted an XAddr for another IP")
	}
	for name, body := range map[string]string{
		"not xml":    "hello",
		"no xaddr":   `<e:Envelope xmlns:e="x"><e:Body><ProbeMatches><ProbeMatch><Scopes>onvif://www.onvif.org/name/x</Scopes></ProbeMatch></ProbeMatches></e:Body></e:Envelope>`,
		"https only": `<e:Envelope xmlns:e="x"><e:Body><ProbeMatches><ProbeMatch><XAddrs>https://192.168.0.60/onvif</XAddrs></ProbeMatch></ProbeMatches></e:Body></e:Envelope>`,
		"bad port":   `<e:Envelope xmlns:e="x"><e:Body><ProbeMatches><ProbeMatch><XAddrs>http://192.168.0.60:99999/onvif</XAddrs></ProbeMatch></ProbeMatches></e:Body></e:Envelope>`,
	} {
		if _, ok := parseProbeMatch([]byte(body), ip); ok {
			t.Errorf("%s: accepted", name)
		}
	}
	d, ok = parseProbeMatch([]byte(`<e:Envelope xmlns:e="x"><e:Body><ProbeMatches><ProbeMatch><XAddrs>http://192.168.0.60/onvif</XAddrs><Scopes>onvif://www.onvif.org/name/Front%0Adoor</Scopes></ProbeMatch></ProbeMatches></e:Body></e:Envelope>`), ip)
	if !ok || d.Port != 80 || d.Name != "Frontdoor" {
		t.Errorf("default port / control chars in name: %+v %v", d, ok)
	}
}

func FuzzParseProbeMatch(f *testing.F) {
	f.Add([]byte(tapoC211))
	ip := net.ParseIP("192.168.0.60")
	f.Fuzz(func(t *testing.T, b []byte) {
		d, ok := parseProbeMatch(b, ip)
		if ok && (d.Host != "192.168.0.60" || d.Port < 1 || d.Port > 65535 || len(d.Name) > 64 || len(d.Model) > 64) {
			t.Fatalf("bad device %+v", d)
		}
	})
}
