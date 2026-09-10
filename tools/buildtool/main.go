// Command buildtool implements the verification recipes of SDD §10 and §12:
// gen, forbid, trace, ledger-check, verify, evidence, evidence-check, size-check and setup.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: buildtool <gen|forbid|trace|ledger-check|verify|evidence|evidence-check|size-check|setup>")
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
	case "ledger-check":
		err = cmdLedgerCheck()
	case "ledger-set":
		err = cmdLedgerSet(os.Args[2:])
	case "verify":
		err = cmdVerify(os.Args[2:])
	case "evidence":
		err = cmdEvidence()
	case "evidence-check":
		err = cmdEvidenceCheck()
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
