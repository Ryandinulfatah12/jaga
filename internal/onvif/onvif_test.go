// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

package onvif_test

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ryandinulfatah12/jaga/internal/onvif"
	"github.com/ryandinulfatah12/jaga/internal/onvif/onviftest"
)

func TestPTZ(t *testing.T) {
	cam := &onviftest.Camera{User: "admin", Pass: "p@ss<&>"}
	srv := httptest.NewServer(cam)
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	c := &onvif.Client{Host: host, User: "admin", Pass: "p@ss<&>"}
	p, err := c.DiscoverPTZ(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if p.Profile != "main" || p.URL != "http://"+host+"/onvif/ptz" {
		t.Errorf("DiscoverPTZ = %+v (want profile main on %s, reported host rewritten)", p, host)
	}
	if err := c.Move(t.Context(), p, 0.5, -0.25, 0, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := c.Stop(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	want := []string{"ContinuousMove main x=0.500 y=-0.250 PT1S", "Stop"}
	if got := cam.Calls(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("calls = %q, want %q", got, want)
	}

	bad := &onvif.Client{Host: host, User: "admin", Pass: "wrong"}
	if _, err := bad.DiscoverPTZ(t.Context()); !errors.Is(err, onvif.ErrONVIF) || !strings.Contains(err.Error(), "Authority failure") {
		t.Errorf("wrong password: %v", err)
	}
	down := &onvif.Client{Host: "127.0.0.1:1", User: "a", Pass: "b"}
	if _, err := down.DiscoverPTZ(t.Context()); !errors.Is(err, onvif.ErrONVIF) {
		t.Errorf("unreachable: %v", err)
	}
}
