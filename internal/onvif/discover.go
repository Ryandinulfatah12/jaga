// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package onvif

import (
	"context"
	"crypto/rand"
	"encoding/xml"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Device is a camera that answered a WS-Discovery probe.
type Device struct {
	Host  string `json:"host"`            // IP, as seen on the wire
	Port  int    `json:"port"`            // ONVIF port from its XAddrs
	Name  string `json:"name,omitempty"`  // onvif://www.onvif.org/name/<name>
	Model string `json:"model,omitempty"` // onvif://www.onvif.org/hardware/<model>
}

var wsDiscovery = &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 3702}

// Discover sends one WS-Discovery Probe for NetworkVideoTransmitter to the
// local network and collects the answers until ctx is done (callers give it
// a few seconds). Unauthenticated and read-only: cameras only announce
// their service address. An answer is accepted only when its XAddr points
// at the IP it came from, so one device cannot list another.
func Discover(ctx context.Context) ([]Device, error) {
	conn, err := (&net.ListenConfig{}).ListenPacket(ctx, "udp4", ":0")
	if err != nil {
		return nil, fmt.Errorf("%w: listen: %w", ErrONVIF, err)
	}
	defer conn.Close() //nolint:errcheck // nothing to flush
	if _, err := conn.WriteTo(probeMessage(), wsDiscovery); err != nil {
		return nil, fmt.Errorf("%w: send probe: %w", ErrONVIF, err)
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(3 * time.Second)
	}
	_ = conn.SetReadDeadline(deadline)

	seen := map[string]Device{}
	buf := make([]byte, 64<<10)
	for len(seen) < 256 {
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			break // deadline: done
		}
		ua, ok := from.(*net.UDPAddr)
		if !ok {
			continue
		}
		if d, ok := parseProbeMatch(buf[:n], ua.IP); ok {
			seen[d.Host] = d
		}
	}
	out := make([]Device, 0, len(seen))
	for _, d := range seen {
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b Device) int { return strings.Compare(a.Host, b.Host) })
	return out, nil
}

func probeMessage() []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>` +
		`<e:Envelope xmlns:e="http://www.w3.org/2003/05/soap-envelope" xmlns:w="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery" xmlns:dn="http://www.onvif.org/ver10/network/wsdl">` +
		`<e:Header><w:MessageID>uuid:` + rand.Text() + `</w:MessageID>` +
		`<w:To e:mustUnderstand="true">urn:schemas-xmlsoap-org:ws:2005:04:discovery</w:To>` +
		`<w:Action e:mustUnderstand="true">http://schemas.xmlsoap.org/ws/2005/04/discovery/Probe</w:Action></e:Header>` +
		`<e:Body><d:Probe><d:Types>dn:NetworkVideoTransmitter</d:Types></d:Probe></e:Body></e:Envelope>`)
}

// parseProbeMatch reads one ProbeMatches answer from ip. It keeps the first
// http XAddr whose host is ip itself.
func parseProbeMatch(b []byte, ip net.IP) (Device, bool) {
	var m struct {
		XAddrs string `xml:"Body>ProbeMatches>ProbeMatch>XAddrs"`
		Scopes string `xml:"Body>ProbeMatches>ProbeMatch>Scopes"`
	}
	if xml.Unmarshal(b, &m) != nil || ip == nil {
		return Device{}, false
	}
	d := Device{Host: ip.String()}
	for _, x := range strings.Fields(m.XAddrs) {
		u, err := url.Parse(x)
		if err != nil || u.Scheme != "http" || !net.ParseIP(u.Hostname()).Equal(ip) {
			continue
		}
		port := 80
		if p := u.Port(); p != "" {
			n, err := strconv.Atoi(p)
			if err != nil || n < 1 || n > 65535 {
				continue
			}
			port = n
		}
		d.Port = port
		break
	}
	if d.Port == 0 {
		return Device{}, false
	}
	for _, s := range strings.Fields(m.Scopes) {
		if v, ok := strings.CutPrefix(s, "onvif://www.onvif.org/name/"); ok {
			d.Name = scope(v)
		} else if v, ok := strings.CutPrefix(s, "onvif://www.onvif.org/hardware/"); ok {
			d.Model = scope(v)
		}
	}
	return d, true
}

// scope decodes a scope value and keeps it short and printable.
func scope(v string) string {
	if u, err := url.PathUnescape(v); err == nil {
		v = u
	}
	v = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, v)
	if len(v) > 64 {
		v = v[:64]
	}
	return v
}
