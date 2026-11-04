// Command server serves the example app (docs/APP.md).
//
//	vela-app gen && vela               # src/_app, internal/appgen, out/server
//	vite build && go run ./cmd/server  # production: dist/ from vite build
//	go run ./cmd/server -dev http://localhost:5173   # with vite (dev server)
//
// It prints its URL on the first line of the standard output.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"example.com/site/internal/appgen"
	"example.com/site/server/store"
	"github.com/elauven/vela/app"
	"github.com/elauven/vela/ssr_runtime/vite"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	dev := flag.String("dev", "", "URL of the Vite dev server; serve the build in dist/ when empty")
	dist := flag.String("dist", "dist", "directory of vite build; \"none\" serves pages without client")
	flag.Parse()

	mux := http.NewServeMux()
	// The app's own API handlers live next to it.
	mux.HandleFunc("GET /api/loads", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(store.Loads())
	})
	var assets *vite.Assets
	switch {
	case *dev != "":
		assets = vite.Dev(*dev)
	case *dist != "none":
		var err error
		if assets, err = vite.Load(filepath.Join(*dist, ".vite", "manifest.json"), "/"); err != nil {
			log.Fatal(err)
		}
		mux.Handle("/assets/", http.FileServer(http.Dir(*dist)))
	}
	handler, err := app.New(app.Config{
		Routes: appgen.Routes(),
		Assets: assets,
		// The root modes of vela: auto navigations into static sections
		// get HTML fragments (docs/APP.md R-9).
		Manifest: filepath.Join("out", "client", "vela.manifest.json"),
		Head:     app.Head{Title: "Vela App", Meta: []app.Meta{{Name: "description", Content: "An example of Vela App"}}},
		Dev:      *dev != "",
	})
	if err != nil {
		log.Fatal(err)
	}
	mux.Handle("/", handler)

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Fprintf(os.Stdout, "http://%s\n", listener.Addr())
	log.Fatal(http.Serve(listener, mux))
}
