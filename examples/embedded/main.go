// Example: go run ./examples/embedded --allow-root /absolute/code --state-dir /private/state
package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/hamedfathi/TreeSitterMCP/server"
	"os"
)

func main() {
	root := flag.String("allow-root", "", "permitted project parent directory")
	state := flag.String("state-dir", "", "private state directory")
	flag.Parse()
	engine, err := server.New(server.Options{AllowRoots: []string{*root}, StateDir: *state})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer engine.Close()
	result, err := engine.Execute(context.Background(), "list_projects", server.Input{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("%+v\n", result)
}
