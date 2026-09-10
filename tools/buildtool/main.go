// Command buildtool implements the checks of SDD §10: gen, forbid, trace, size-check and setup.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: buildtool <gen|forbid|trace|size-check|setup>")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "gen":
		err = cmdGen()
	case "forbid":
		err = cmdForbid()
	case "trace":
		err = cmdTrace()
	case "size-check":
		err = cmdSizeCheck()
	case "setup":
		err = cmdSetup()
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
