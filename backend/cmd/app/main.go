// Command app is the package backend. DSM runs it as a CGI (ui/cgi/api.cgi in
// the package) under the package account, once per request.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http/cgi"
	"os"
	"path/filepath"

	appapi "dsmapp/internal/api"
	"dsmapp/internal/app"
	"dsmapp/internal/helper"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
	"github.com/synopackageland/syno-pkg-land-sdk/dsm"
)

// Set by `synopkgland build-spk` through -ldflags -X; never read from a request.
var (
	packageID       = "dev"
	version         = "0.0.0"
	helperInventory = ""
	// dsm-package.yaml entry: "admin-only" or "app-privilege"; empty keeps the route policy alone.
	entry = ""
)

func main() {
	log.SetFlags(0)
	log.SetPrefix(packageID + ": ")
	if os.Getenv("GATEWAY_INTERFACE") == "" && len(os.Args) == 2 && os.Args[1] == "--describe" {
		permissions, err := appapi.Permissions()
		if err != nil {
			log.Fatal(err)
		}
		if err := api.ValidateOperationRegistry(appapi.Operations(), permissions, app.Routes(app.Config{PackageID: packageID, Version: version})...); err != nil {
			log.Fatal(err)
		}
		methods, err := api.DescribeRoutes(permissions, app.Routes(app.Config{PackageID: packageID, Version: version})...)
		if err != nil {
			log.Fatal(err)
		}
		if err := json.NewEncoder(os.Stdout).Encode(methods); err != nil {
			log.Fatal(err)
		}
		return
	}
	if os.Getenv("GATEWAY_INTERFACE") == "" {
		fmt.Fprintf(os.Stderr, "%s %s: this program runs as the package's DSM CGI; open the app from the DSM main menu.\n", packageID, version)
		os.Exit(2)
	}
	cfg := app.Config{
		PackageID: packageID,
		Version:   version,
		DataDir:   "/var/packages/" + packageID + "/var",
	}
	if helperInventory != "" {
		if json.Unmarshal([]byte(helperInventory), &cfg.HelperInventory) != nil || cfg.HelperInventory.Package != packageID || cfg.HelperInventory.Version != version {
			cfg.HelperInventory = helper.Inventory{Package: packageID}
		}
	}
	// Kernels without memfd_create stage DSM's bridges in the package's own temp.
	dsm.SetBridgeFallbackDir("/var/packages/" + packageID + "/tmp")
	auth, err := dsm.NewEntryGate(dsm.NewAuthenticator(), entry, packageID, cfg.DataDir)
	if err != nil {
		log.Fatal(err)
	}
	permissions, err := appapi.Permissions()
	if err != nil {
		log.Fatal(err)
	}
	srv, err := api.NewServerWithOperations(app.RetryAuthentication(auth), appapi.Operations(), permissions, app.Routes(cfg)...)
	if err != nil {
		log.Fatal(err)
	}
	handler := api.WithRequestDeadline(api.WithDescription(srv, app.RetryAuthentication(auth), filepath.Join("/var/packages", packageID, "target", api.DescriptionFile)))
	if err := cgi.Serve(handler); err != nil {
		log.Fatal(err)
	}
}
