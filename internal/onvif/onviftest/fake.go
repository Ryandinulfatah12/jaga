// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

// Package onviftest is a fake ONVIF PTZ camera for tests.
package onviftest

import (
	"crypto/sha1" //nolint:gosec // G505: verifying the WS-Security digest
	"encoding/base64"
	"encoding/xml"
	"io"
	"net/http"
	"strings"
	"sync"
)

// Camera is an ONVIF PTZ camera that checks the password digest and
// records PTZ calls. It reports a wrong host in its service addresses, as
// cameras behind NAT do.
type Camera struct {
	User, Pass string
	mu         sync.Mutex
	calls      []string
}

// Calls returns the PTZ calls seen so far, like "ContinuousMove x=0.500 y=0.000 PT1S".
func (f *Camera) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *Camera) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var env struct {
		Token struct {
			Username, Password, Nonce, Created string
		} `xml:"Header>Security>UsernameToken"`
		Body struct {
			Inner []byte `xml:",innerxml"`
		} `xml:"Body"`
	}
	if err := xml.Unmarshal(body, &env); err != nil {
		http.Error(w, "bad xml", http.StatusBadRequest)
		return
	}
	nonce, _ := base64.StdEncoding.DecodeString(env.Token.Nonce)
	h := sha1.New() //nolint:gosec // G401: WS-Security digest
	h.Write(nonce)
	h.Write([]byte(env.Token.Created))
	h.Write([]byte(f.Pass))
	if env.Token.Username != f.User || env.Token.Password != base64.StdEncoding.EncodeToString(h.Sum(nil)) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><s:Fault><s:Reason><s:Text>Authority failure</s:Text></s:Reason></s:Fault></s:Body></s:Envelope>`)
		return
	}
	in := string(env.Body.Inner)
	host := "http://192.0.2.99:9999" // a wrong host, as cameras behind NAT report
	resp := ""
	switch {
	case strings.Contains(in, "GetCapabilities"):
		resp = `<GetCapabilitiesResponse><Capabilities><Media><XAddr>` + host + `/onvif/media</XAddr></Media><PTZ><XAddr>` + host + `/onvif/ptz</XAddr></PTZ></Capabilities></GetCapabilitiesResponse>`
	case strings.Contains(in, "GetProfiles") && r.URL.Path == "/onvif/media":
		resp = `<GetProfilesResponse><Profiles token="noptz"/><Profiles token="main"><PTZConfiguration token="ptz0"/></Profiles></GetProfilesResponse>`
	case strings.Contains(in, "ContinuousMove") && r.URL.Path == "/onvif/ptz":
		var m struct {
			Profile string `xml:"ProfileToken"`
			PT      struct {
				X string `xml:"x,attr"`
				Y string `xml:"y,attr"`
			} `xml:"Velocity>PanTilt"`
			Timeout string `xml:"Timeout"`
		}
		_ = xml.Unmarshal(env.Body.Inner, &m)
		f.record("ContinuousMove " + m.Profile + " x=" + m.PT.X + " y=" + m.PT.Y + " " + m.Timeout)
		resp = `<ContinuousMoveResponse/>`
	case strings.Contains(in, "<Stop") && r.URL.Path == "/onvif/ptz":
		f.record("Stop")
		resp = `<StopResponse/>`
	default:
		http.Error(w, "unexpected "+r.URL.Path, http.StatusBadRequest)
		return
	}
	_, _ = io.WriteString(w, `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body>`+resp+`</s:Body></s:Envelope>`)
}

func (f *Camera) record(s string) {
	f.mu.Lock()
	f.calls = append(f.calls, s)
	f.mu.Unlock()
}
