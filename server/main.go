// Command kandev-deepseek-credits is the backend half of this kandev plugin.
// It implements pluginsdk.Plugin (see plugin.go) and is spawned by kandev as
// a gRPC subprocess — there is no HTTP server, no listen address, and no
// secrets to configure: pluginsdk.Serve owns the entire transport.
package main

import "github.com/kandev/kandev/pkg/pluginsdk"

func main() {
	pluginsdk.Serve(newPlugin())
}
