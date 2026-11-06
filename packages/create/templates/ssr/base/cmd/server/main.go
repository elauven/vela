// The server: renders App with the Go renderer `vela` generates into
// out/server, and adds the client from the Vite dev server (-dev) or from
// the output of vite build (-dist).
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"

	"github.com/elauven/vela/ssr_runtime/vite"

	components "__MODULE__/out/server/components"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	dev := flag.String("dev", "", "URL of the Vite dev server; serve dist/ when empty")
	dist := flag.String("dist", "dist", "output directory of vite build")
	flag.Parse()

	mux := http.NewServeMux()
	var assets *vite.Assets
	if *dev != "" {
		assets = vite.Dev(*dev)
	} else {
		var err error
		if assets, err = vite.Load(filepath.Join(*dist, ".vite", "manifest.json"), "/"); err != nil {
			log.Fatal(err)
		}
		mux.Handle("/assets/", http.FileServer(http.Dir(*dist)))
	}
	tags, err := assets.Tags("src/client.__EXT__")
	if err != nil {
		log.Fatal(err)
	}

	mux.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<!doctype html><html><head><meta charset=\"utf-8\">%s</head><body><div id=\"app\">", tags)
		props := components.AppProps{Set: map[string]bool{"title": true}, Title: "Vela"}
		if err := components.RenderAppContext(r.Context(), w, props, components.AppSlots{}); err != nil {
			log.Print(err)
		}
		fmt.Fprint(w, "</div></body></html>")
	})

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("http://" + listener.Addr().String() + "/")
	log.Fatal(http.Serve(listener, mux))
}
