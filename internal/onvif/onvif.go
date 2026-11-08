// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

// Package onvif is the small part of ONVIF that Jaga uses: find the PTZ
// service and profile, then ContinuousMove and Stop. SOAP 1.2 with a
// WS-Security UsernameToken (password digest), which is what cameras such
// as the Tapo C2xx series expect.
package onvif

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // G505: the WS-Security password digest is defined as SHA-1
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrONVIF wraps every failure caused by the camera (unreachable, refused,
// SOAP fault, no PTZ), as opposed to a bug on our side.
var ErrONVIF = errors.New("onvif")

// Client talks to one camera. Host is the host:port of its ONVIF service.
type Client struct {
	Host       string
	User, Pass string
	// HTTP defaults to a client with a 5 s timeout that never follows redirects.
	HTTP *http.Client
}

// PTZ is a discovered PTZ service and the media profile to move.
type PTZ struct {
	URL     string
	Profile string
}

// DiscoverPTZ finds the PTZ service and the first profile with a PTZ
// configuration. Service addresses reported by the camera are rewritten to
// c.Host: cameras behind NAT or with a stale IP report the wrong host, and
// it keeps requests on the camera the admin configured.
func (c *Client) DiscoverPTZ(ctx context.Context) (PTZ, error) {
	var caps struct {
		Media string `xml:"Body>GetCapabilitiesResponse>Capabilities>Media>XAddr"`
		PTZ   string `xml:"Body>GetCapabilitiesResponse>Capabilities>PTZ>XAddr"`
	}
	err := c.call(ctx, c.url("/onvif/device_service"),
		`<GetCapabilities xmlns="http://www.onvif.org/ver10/device/wsdl"><Category>All</Category></GetCapabilities>`, &caps)
	if err != nil {
		return PTZ{}, err
	}
	if caps.PTZ == "" {
		return PTZ{}, fmt.Errorf("%w: camera has no PTZ service", ErrONVIF)
	}
	var profiles struct {
		List []struct {
			Token string `xml:"token,attr"`
			PTZ   *struct {
				Token string `xml:"token,attr"`
			} `xml:"PTZConfiguration"`
		} `xml:"Body>GetProfilesResponse>Profiles"`
	}
	if err := c.call(ctx, c.rehost(caps.Media), `<GetProfiles xmlns="http://www.onvif.org/ver10/media/wsdl"/>`, &profiles); err != nil {
		return PTZ{}, err
	}
	for _, p := range profiles.List {
		if p.PTZ != nil && p.Token != "" {
			return PTZ{URL: c.rehost(caps.PTZ), Profile: p.Token}, nil
		}
	}
	return PTZ{}, fmt.Errorf("%w: no media profile with PTZ", ErrONVIF)
}

// Move starts a continuous move. pan, tilt and zoom are speeds in -1..1
// (pan right, tilt up and zoom in are positive). The camera stops by itself
// after timeout, so a lost Stop (closed browser tab) cannot leave it moving.
func (c *Client) Move(ctx context.Context, p PTZ, pan, tilt, zoom float64, timeout time.Duration) error {
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }
	body := `<ContinuousMove xmlns="http://www.onvif.org/ver20/ptz/wsdl" xmlns:tt="http://www.onvif.org/ver10/schema">` +
		`<ProfileToken>` + esc(p.Profile) + `</ProfileToken><Velocity>` +
		`<tt:PanTilt x="` + f(pan) + `" y="` + f(tilt) + `"/>`
	if zoom != 0 {
		body += `<tt:Zoom x="` + f(zoom) + `"/>`
	}
	body += `</Velocity><Timeout>PT` + strconv.FormatFloat(timeout.Seconds(), 'f', -1, 64) + `S</Timeout></ContinuousMove>`
	return c.call(ctx, p.URL, body, nil)
}

// Stop stops pan, tilt and zoom.
func (c *Client) Stop(ctx context.Context, p PTZ) error {
	return c.call(ctx, p.URL, `<Stop xmlns="http://www.onvif.org/ver20/ptz/wsdl"><ProfileToken>`+esc(p.Profile)+
		`</ProfileToken><PanTilt>true</PanTilt><Zoom>true</Zoom></Stop>`, nil)
}

func (c *Client) url(path string) string { return "http://" + c.Host + path }

// rehost keeps the path of a service address and points it at c.Host.
func (c *Client) rehost(xaddr string) string {
	u, err := url.Parse(strings.TrimSpace(xaddr))
	if err != nil || u.Path == "" {
		return c.url("/onvif/device_service")
	}
	return c.url(u.EscapedPath())
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// call posts one SOAP request and decodes the response envelope into out.
func (c *Client) call(ctx context.Context, endpoint, body string, out any) error {
	env := `<?xml version="1.0" encoding="UTF-8"?><s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Header>` +
		c.security(time.Now()) + `</s:Header><s:Body>` + body + `</s:Body></s:Envelope>`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(env))
	if err != nil {
		return fmt.Errorf("%w: %w", ErrONVIF, err)
	}
	req.Header.Set("Content-Type", "application/soap+xml; charset=utf-8")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s unreachable", ErrONVIF, c.Host) // the error text may carry the URL; keep it short
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%w: reading response: %w", ErrONVIF, err)
	}
	var fault struct {
		Reason string `xml:"Body>Fault>Reason>Text"`
	}
	_ = xml.Unmarshal(data, &fault) // not every error body is XML
	switch {
	case fault.Reason != "":
		return fmt.Errorf("%w: %s", ErrONVIF, strings.TrimSpace(fault.Reason))
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("%w: wrong camera username or password", ErrONVIF)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("%w: HTTP %d", ErrONVIF, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := xml.NewDecoder(bytes.NewReader(data)).Decode(out); err != nil {
		return fmt.Errorf("%w: bad response: %w", ErrONVIF, err)
	}
	return nil
}

// security is the WS-Security header: Digest = Base64(SHA1(nonce + created + password)).
func (c *Client) security(now time.Time) string {
	if c.User == "" {
		return ""
	}
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce) // crypto/rand never fails on supported platforms
	created := now.UTC().Format("2006-01-02T15:04:05.000Z")
	h := sha1.New() //nolint:gosec // G401: mandated by the WS-Security UsernameToken profile
	h.Write(nonce)
	h.Write([]byte(created))
	h.Write([]byte(c.Pass))
	b64 := base64.StdEncoding.EncodeToString
	return `<Security s:mustUnderstand="1" xmlns="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd">` +
		`<UsernameToken><Username>` + esc(c.User) + `</Username>` +
		`<Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest">` + b64(h.Sum(nil)) + `</Password>` +
		`<Nonce EncodingType="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-soap-message-security-1.0#Base64Binary">` + b64(nonce) + `</Nonce>` +
		`<Created xmlns="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd">` + created + `</Created>` +
		`</UsernameToken></Security>`
}

func esc(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
