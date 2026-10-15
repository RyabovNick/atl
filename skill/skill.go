// Package skill holds the agent-facing text atl installs: the atlassian skill
// and the short section added to an agent's global instructions.
package skill

import _ "embed"

// Skill is the atlassian skill, SKILL.md.
//
//go:embed atlassian/SKILL.md
var Skill []byte

// Instructions is the section added to CLAUDE.md or AGENTS.md.
//
//go:embed agents-section.md
var Instructions string
