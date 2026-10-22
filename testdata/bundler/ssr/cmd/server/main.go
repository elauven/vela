// The Go server of the server-rendered fixture (task 064): renders App and
// takes the client's tags from the Vite dev server (-dev) or the manifest
// of a Vite build (-dist), whose files it serves.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"

	"github.com/elauven/vela/ssr_runtime/vite"

	components "example.com/site/out/server/components"
)

func main() {
	dev := flag.String("dev", "", "URL of the Vite dev server")
	dist := flag.String("dist", "", "output directory of vite build")
	flag.Parse()

	assets := vite.Dev(*dev)
	if *dev == "" {
		var err error
		if assets, err = vite.Load(filepath.Join(*dist, ".vite", "manifest.json"), "/"); err != nil {
			log.Fatal(err)
		}
	}
	tags, err := assets.Tags("src/client.js")
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	if *dist != "" {
		mux.Handle("/assets/", http.FileServer(http.Dir(*dist)))
	}
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<!doctype html><html><head>%s</head><body><div id=\"app\">", tags)
		props := components.AppProps{Set: map[string]bool{"start": true}, Start: 7}
		if err := components.RenderAppContext(r.Context(), w, props, components.AppSlots{}); err != nil {
			log.Print(err)
		}
		fmt.Fprint(w, "</div></body></html>")
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("http://" + listener.Addr().String() + "/")
	log.Fatal(http.Serve(listener, mux))
}
