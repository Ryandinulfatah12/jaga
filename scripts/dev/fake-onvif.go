// SPDX-License-Identifier: Apache-2.0
// Copyright The Jaga Authors

//go:build ignore

// Fake ONVIF PTZ camera for UI tests: go run ./scripts/dev/fake-onvif.go -listen :2020 -user u -pass p
// Logs every PTZ call to stdout.
package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/ryandinulfatah12/jaga/internal/onvif/onviftest"
)

func main() {
	listen := flag.String("listen", ":2020", "address")
	user := flag.String("user", "admin", "username")
	pass := flag.String("pass", "admin", "password")
	flag.Parse()
	cam := &onviftest.Camera{User: *user, Pass: *pass}
	go func() {
		seen := 0
		for range time.Tick(200 * time.Millisecond) {
			calls := cam.Calls()
			for _, c := range calls[seen:] {
				log.Println(c)
			}
			seen = len(calls)
		}
	}()
	srv := &http.Server{Addr: *listen, Handler: cam, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
