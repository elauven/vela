package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"__MODULE__/internal/appgen"
	"github.com/elauven/vela/app"
	"github.com/elauven/vela/ssr_runtime/vite"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	dev := flag.String("dev", "", "URL of the Vite dev server; serve dist/ when empty")
	dist := flag.String("dist", "dist", `directory of vite build; "none" serves pages without client`)
	flag.Parse()

	mux := http.NewServeMux()
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
		Routes:   appgen.Routes(),
		Assets:   assets,
		Manifest: filepath.Join("out", "client", "vela.manifest.json"),
		Head:     app.Head{Title: "__NAME__"},
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
