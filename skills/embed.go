// Package skills embeds the agent skills that `sluice init` writes into a repository.
package skills

import _ "embed"

// Sluice is skills/sluice/SKILL.md. The binary carries it, so the skill always matches the
// version of the binary.
//
//go:embed sluice/SKILL.md
var Sluice []byte
