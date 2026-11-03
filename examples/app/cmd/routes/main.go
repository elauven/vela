// Command routes prints the route of each path given on the command line,
// using the generated table. Task 075 adds the server.
package main

import (
	"fmt"
	"os"

	"example.com/site/internal/appgen"
)

func main() {
	routes := appgen.Routes()
	for _, path := range os.Args[1:] {
		match, ok := routes.Match(path)
		if !ok {
			fmt.Printf("%s\t404\n", path)
			continue
		}
		fmt.Printf("%s\t%s\t%v\n", path, match.Route.ID, match.Params)
	}
}
