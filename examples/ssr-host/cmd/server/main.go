// Command server renders the cart on the server and hydrates it in the
// browser. Build the Vela project first (see README.md).
package main

import (
	"log"
	"net/http"

	components "example.com/ssrhostdemo/out/server/components"
)

const head = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Vela SSR host demo</title>
<link rel="stylesheet" href="/styles/Cart.css"></head>
<body>
<div id="app">`

// The client module is the ordinary CSR module; hydrate() adopts the server
// HTML and the payload script the renderer wrote after it.
const tail = `</div>
<script type="module">
  import { hydrate } from '/client/runtime-hydrate.js';
  import Cart from '/client/Cart.js';
  hydrate(Cart, document.getElementById('app'), {
    onMismatch: (report) => console.warn('hydration mismatch', report),
  });
</script>
</body>
</html>`

func main() {
	http.Handle("/client/", http.StripPrefix("/client/", http.FileServer(http.Dir("out/client"))))
	http.Handle("/styles/", http.StripPrefix("/styles/", http.FileServer(http.Dir("out/styles"))))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		props := components.CartProps{Set: map[string]bool{}}
		if c := r.URL.Query().Get("currency"); c != "" {
			props.Currency = c
			props.Set["currency"] = true
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(head))
		// RenderCartContext passes the request context to hosts that take a
		// context.Context first parameter.
		if err := components.RenderCartContext(r.Context(), w, props, components.CartSlots{}); err != nil {
			log.Printf("render: %v", err)
			return
		}
		w.Write([]byte(tail))
	})
	log.Println("listening on http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
