// Command sluice is the server, runner and CLI (C-01).
package main

import (
	"os"

	"github.com/alternayte/sluice/internal/app"
)

func main() {
	os.Exit(app.Main(os.Args[1:]))
}
