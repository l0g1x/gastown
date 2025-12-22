package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/tmux"
)

// AgentType represents the type of Gas Town agent.
type AgentType int

const (
	AgentMayor AgentType = iota
	AgentDeacon
	AgentWitness
	AgentRefinery
	AgentCrew
	AgentPolecat
)

// AgentSession represents a categorized tmux session.
type AgentSession struct {
	Name      string
	Type      AgentType
	Rig       string // For rig-specific agents
	AgentName string // e.g., crew name, polecat name
}

// AgentTypeColors maps agent types to tmux color codes.
var AgentTypeColors = map[AgentType]string{
	AgentMayor:    "#[fg=red,bold]",
	AgentDeacon:   "#[fg=yellow,bold]",
	AgentWitness:  "#[fg=cyan]",
	AgentRefinery: "#[fg=blue]",
	AgentCrew:     "#[fg=green]",
	AgentPolecat:  "#[fg=white,dim]",
}

// AgentTypeIcons maps agent types to display icons.
var AgentTypeIcons = map[AgentType]string{
	AgentMayor:    "🎩",
	AgentDeacon:   "🦉",
	AgentWitness:  "👁",
	AgentRefinery: "🏭",
	AgentCrew:     "👷",
	AgentPolecat:  "😺",
}

var agentsCmd = &cobra.Command{
	Use:     "agents",
	Aliases: []string{"ag"},
	Short:   "Switch between Gas Town agent sessions",
	Long: `Display a popup menu of core Gas Town agent sessions.

Shows Mayor, Deacon, Witnesses, Refineries, and Crew workers.
Polecats are hidden (use 'gt polecats' to see them).

The menu appears as a tmux popup for quick session switching.`,
	RunE: runAgents,
}

var agentsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List agent sessions (no popup)",
	Long:  `List all agent sessions to stdout without the popup menu.`,
	RunE:  runAgentsList,
}

var agentsAllFlag bool

func init() {
	agentsCmd.PersistentFlags().BoolVarP(&agentsAllFlag, "all", "a", false, "Include polecats in the menu")
	agentsCmd.AddCommand(agentsListCmd)
	rootCmd.AddCommand(agentsCmd)
}

// categorizeSession determines the agent type from a session name.
func categorizeSession(name string) *AgentSession {
	// Must start with gt- prefix
	if !strings.HasPrefix(name, "gt-") {
		return nil
	}

	session := &AgentSession{Name: name}
	suffix := strings.TrimPrefix(name, "gt-")

	// Town-level agents
	if suffix == "mayor" {
		session.Type = AgentMayor
		return session
	}
	if suffix == "deacon" {
		session.Type = AgentDeacon
		return session
	}

	// Rig-level agents: gt-<rig>-<type> or gt-<rig>-crew-<name>
	parts := strings.SplitN(suffix, "-", 2)
	if len(parts) < 2 {
		return nil // Invalid format
	}

	session.Rig = parts[0]
	remainder := parts[1]

	// Check for crew: gt-<rig>-crew-<name>
	if strings.HasPrefix(remainder, "crew-") {
		session.Type = AgentCrew
		session.AgentName = strings.TrimPrefix(remainder, "crew-")
		return session
	}

	// Check for other agent types
	switch remainder {
	case "witness":
		session.Type = AgentWitness
		return session
	case "refinery":
		session.Type = AgentRefinery
		return session
	}

	// Everything else is a polecat
	session.Type = AgentPolecat
	session.AgentName = remainder
	return session
}

// getAgentSessions returns all categorized Gas Town sessions.
func getAgentSessions(includePolecats bool) ([]*AgentSession, error) {
	t := tmux.NewTmux()
	sessions, err := t.ListSessions()
	if err != nil {
		return nil, err
	}

	var agents []*AgentSession
	for _, name := range sessions {
		agent := categorizeSession(name)
		if agent == nil {
			continue
		}
		if agent.Type == AgentPolecat && !includePolecats {
			continue
		}
		agents = append(agents, agent)
	}

	// Sort: mayor, deacon first, then by rig, then by type
	sort.Slice(agents, func(i, j int) bool {
		a, b := agents[i], agents[j]

		// Town-level agents first
		if a.Type == AgentMayor {
			return true
		}
		if b.Type == AgentMayor {
			return false
		}
		if a.Type == AgentDeacon {
			return true
		}
		if b.Type == AgentDeacon {
			return false
		}

		// Then by rig name
		if a.Rig != b.Rig {
			return a.Rig < b.Rig
		}

		// Within rig: refinery, witness, crew, polecat
		typeOrder := map[AgentType]int{
			AgentRefinery: 0,
			AgentWitness:  1,
			AgentCrew:     2,
			AgentPolecat:  3,
		}
		if typeOrder[a.Type] != typeOrder[b.Type] {
			return typeOrder[a.Type] < typeOrder[b.Type]
		}

		// Same type: alphabetical by agent name
		return a.AgentName < b.AgentName
	})

	return agents, nil
}

// displayLabel returns the menu display label for an agent.
func (a *AgentSession) displayLabel() string {
	color := AgentTypeColors[a.Type]
	icon := AgentTypeIcons[a.Type]

	switch a.Type {
	case AgentMayor:
		return fmt.Sprintf("%s%s Mayor#[default]", color, icon)
	case AgentDeacon:
		return fmt.Sprintf("%s%s Deacon#[default]", color, icon)
	case AgentWitness:
		return fmt.Sprintf("%s%s %s/witness#[default]", color, icon, a.Rig)
	case AgentRefinery:
		return fmt.Sprintf("%s%s %s/refinery#[default]", color, icon, a.Rig)
	case AgentCrew:
		return fmt.Sprintf("%s%s %s/crew/%s#[default]", color, icon, a.Rig, a.AgentName)
	case AgentPolecat:
		return fmt.Sprintf("%s%s %s/%s#[default]", color, icon, a.Rig, a.AgentName)
	}
	return a.Name
}

// shortcutKey returns a keyboard shortcut for the menu item.
func shortcutKey(index int) string {
	if index < 9 {
		return fmt.Sprintf("%d", index+1)
	}
	if index < 35 {
		// a-z after 1-9
		return string(rune('a' + index - 9))
	}
	return ""
}

func runAgents(cmd *cobra.Command, args []string) error {
	agents, err := getAgentSessions(agentsAllFlag)
	if err != nil {
		return fmt.Errorf("listing sessions: %w", err)
	}

	if len(agents) == 0 {
		fmt.Println("No agent sessions running.")
		fmt.Println("\nStart agents with:")
		fmt.Println("  gt mayor start")
		fmt.Println("  gt deacon start")
		return nil
	}

	// Build display-menu arguments
	menuArgs := []string{
		"display-menu",
		"-T", "#[fg=cyan,bold]⚙️  Gas Town Agents",
		"-x", "C", // Center horizontally
		"-y", "C", // Center vertically
	}

	var currentRig string
	keyIndex := 0

	for _, agent := range agents {
		// Add rig header when rig changes (skip for town-level agents)
		if agent.Rig != "" && agent.Rig != currentRig {
			if currentRig != "" || keyIndex > 0 {
				// Add separator before new rig section
				menuArgs = append(menuArgs, "")
			}
			// Add rig header (non-selectable)
			menuArgs = append(menuArgs, fmt.Sprintf("#[fg=white,dim]── %s ──", agent.Rig), "", "")
			currentRig = agent.Rig
		}

		key := shortcutKey(keyIndex)
		label := agent.displayLabel()
		action := fmt.Sprintf("switch-client -t '%s'", agent.Name)

		menuArgs = append(menuArgs, label, key, action)
		keyIndex++
	}

	// Execute tmux display-menu
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return fmt.Errorf("tmux not found: %w", err)
	}

	execCmd := exec.Command(tmuxPath, menuArgs...)
	execCmd.Stdin = os.Stdin
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr

	return execCmd.Run()
}

func runAgentsList(cmd *cobra.Command, args []string) error {
	agents, err := getAgentSessions(agentsAllFlag)
	if err != nil {
		return fmt.Errorf("listing sessions: %w", err)
	}

	if len(agents) == 0 {
		fmt.Println("No agent sessions running.")
		return nil
	}

	var currentRig string
	for _, agent := range agents {
		// Print rig header
		if agent.Rig != "" && agent.Rig != currentRig {
			if currentRig != "" {
				fmt.Println()
			}
			fmt.Printf("── %s ──\n", agent.Rig)
			currentRig = agent.Rig
		}

		icon := AgentTypeIcons[agent.Type]
		switch agent.Type {
		case AgentMayor:
			fmt.Printf("  %s Mayor\n", icon)
		case AgentDeacon:
			fmt.Printf("  %s Deacon\n", icon)
		case AgentWitness:
			fmt.Printf("  %s witness\n", icon)
		case AgentRefinery:
			fmt.Printf("  %s refinery\n", icon)
		case AgentCrew:
			fmt.Printf("  %s crew/%s\n", icon, agent.AgentName)
		case AgentPolecat:
			fmt.Printf("  %s %s\n", icon, agent.AgentName)
		}
	}

	return nil
}
