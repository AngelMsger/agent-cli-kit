package skill

import (
	"io/fs"
	"path/filepath"
)

// Agent describes where a coding agent loads Skills from, and how to tell
// that the agent is in use on this machine / in this project.
type Agent struct {
	ID            string   // stable --agent id
	homeSub       string   // dir under $HOME; global dest is $HOME/<homeSub>/skills/<name>
	homeMarkers   []string // paths under $HOME proving the agent is installed
	projectSkills string   // skills dir relative to the project root
	projectMarks  []string // paths under the cwd proving the agent is used here
}

// agents lists every coding agent the Skill can be installed for.
// Paths follow the Agent Skills ecosystem (vercel-labs/skills) and each
// product's documented native directory.
var agents = []Agent{
	{
		ID:            "claude-code",
		homeSub:       ".claude",
		homeMarkers:   []string{".claude"},
		projectSkills: ".claude/skills",
		projectMarks:  []string{".claude"},
	},
	{
		ID:            "codex",
		homeSub:       ".codex",
		homeMarkers:   []string{".codex"},
		projectSkills: ".agents/skills",
		projectMarks:  []string{".agents", "AGENTS.md"},
	},
	{
		ID:            "cursor",
		homeSub:       ".cursor",
		homeMarkers:   []string{".cursor"},
		projectSkills: ".cursor/skills",
		projectMarks:  []string{".cursor"},
	},
	{
		// Shared open-standard tree read by Cursor, Cline, Gemini, Copilot,
		// OpenCode, Amp, and many others (in addition to their native paths).
		ID:            "agents",
		homeSub:       ".agents",
		homeMarkers:   []string{".agents"},
		projectSkills: ".agents/skills",
		projectMarks:  []string{".agents", "AGENTS.md"},
	},
	{
		ID:            "gemini",
		homeSub:       ".gemini",
		homeMarkers:   []string{".gemini"},
		projectSkills: ".gemini/skills",
		projectMarks:  []string{".gemini"},
	},
	{
		ID:            "github-copilot",
		homeSub:       ".copilot",
		homeMarkers:   []string{".copilot"},
		projectSkills: ".agents/skills",
		projectMarks:  []string{".github", ".agents"},
	},
	{
		ID:            "opencode",
		homeSub:       ".config/opencode",
		homeMarkers:   []string{".config/opencode"},
		projectSkills: ".opencode/skills",
		projectMarks:  []string{".opencode"},
	},
	{
		ID:            "continue",
		homeSub:       ".continue",
		homeMarkers:   []string{".continue"},
		projectSkills: ".continue/skills",
		projectMarks:  []string{".continue"},
	},
	{
		ID:            "windsurf",
		homeSub:       ".codeium/windsurf",
		homeMarkers:   []string{".codeium/windsurf"},
		projectSkills: ".windsurf/skills",
		projectMarks:  []string{".windsurf", ".codeium"},
	},
	{
		ID:            "grok",
		homeSub:       ".grok",
		homeMarkers:   []string{".grok"},
		projectSkills: ".grok/skills",
		projectMarks:  []string{".grok"},
	},
	{
		ID:            "pi",
		homeSub:       ".pi/agent",
		homeMarkers:   []string{".pi"},
		projectSkills: ".pi/skills",
		projectMarks:  []string{".pi"},
	},
	{
		ID:            "kilo",
		homeSub:       ".kilocode",
		homeMarkers:   []string{".kilocode"},
		projectSkills: ".kilocode/skills",
		projectMarks:  []string{".kilocode"},
	},
	{
		ID:            "roo",
		homeSub:       ".roo",
		homeMarkers:   []string{".roo"},
		projectSkills: ".roo/skills",
		projectMarks:  []string{".roo"},
	},
}

func AgentByID(id string) (Agent, bool) {
	for _, s := range agents {
		if s.ID == id {
			return cloneAgent(s), true
		}
	}
	return Agent{}, false
}

func AgentIDs() []string {
	ids := make([]string, len(agents))
	for i, s := range agents {
		ids[i] = s.ID
	}
	return ids
}

// Agents returns independent copies in the established discovery order.
func Agents() []Agent {
	result := make([]Agent, len(agents))
	for i, a := range agents {
		result[i] = cloneAgent(a)
	}
	return result
}
func cloneAgent(a Agent) Agent {
	a.homeMarkers = append([]string(nil), a.homeMarkers...)
	a.projectMarks = append([]string(nil), a.projectMarks...)
	return a
}

// Destination computes a path only. The caller owns home lookup and write policy.
func (a Agent) Destination(home, name string, project bool) string {
	if project {
		return filepath.Join(a.projectSkills, name)
	}
	return filepath.Join(home, a.homeSub, "skills", name)
}

// Detect inspects markers in an explicitly supplied home or project filesystem.
// Shared destination directories do not collapse distinct agent identities.
func Detect(fsys fs.FS, project bool) []Agent {
	var found []Agent
	for _, a := range agents {
		markers := a.homeMarkers
		if project {
			markers = a.projectMarks
		}
		for _, marker := range markers {
			if _, err := fs.Stat(fsys, marker); err == nil {
				found = append(found, cloneAgent(a))
				break
			}
		}
	}
	return found
}
