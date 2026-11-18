// Command dev is a local test fixture, never included in an SPK.
package main

import (
	appapi "dsmapp/internal/api"
	"dsmapp/internal/app"
	"flag"
	"fmt"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"log"
	"net"
	"net/http"
	"os"
)

type fixtureAuth struct{}

func (fixtureAuth) Authenticate(r *http.Request) (api.Identity, error) {
	switch r.Header.Get("X-Fixture-Role") {
	case "anonymous":
		return api.Identity{}, api.ErrNoSession
	case "user":
		return api.Identity{User: "normal1"}, nil
	default:
		return api.Identity{User: "admin", Admin: true}, nil
	}
}
func main() {
	addr := flag.String("addr", "127.0.0.1:0", "loopback address")
	dir := flag.String("data", "", "fixture directory")
	connections := flag.Bool("connections-fixture", false, "synthetic connection sessions; no external access")
	flag.Parse()
	host, _, err := net.SplitHostPort(*addr)
	if err != nil || host != "127.0.0.1" {
		log.Fatal("fixture only binds IPv4 loopback")
	}
	if *dir == "" {
		*dir, err = os.MkdirTemp("", "syno-connect-dev-")
		if err != nil {
			log.Fatal(err)
		}
		defer os.RemoveAll(*dir)
	}
	cfg := app.Config{PackageID: "SynoPkgConnect", Version: "fixture", DataDir: *dir}
	if *connections {
		closeFixture, e := configureConnectionsFixture(&cfg)
		if e != nil {
			log.Fatal(e)
		}
		defer closeFixture()
	}
	permissions, err := appapi.Permissions()
	if err != nil {
		log.Fatal(err)
	}
	srv, err := api.NewServerWithOperations(fixtureAuth{}, appapi.Operations(), permissions, app.Routes(cfg)...)
	if err != nil {
		log.Fatal(err)
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("http://" + ln.Addr().String())
	log.Fatal(http.Serve(ln, srv))
}
