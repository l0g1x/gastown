package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/mail"
	"github.com/steveyegge/gastown/internal/polecat"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/suggest"
	"github.com/steveyegge/gastown/internal/tmux"
	"github.com/steveyegge/gastown/internal/workspace"
)

// Sling command flags
var (
	slingWisp     bool   // Create wisp instead of durable mol
	slingMolecule string // Molecule proto when slinging an issue
	slingPriority int    // Override priority (P0-P4)
	slingForce    bool   // Re-sling even if hook has work
	slingNoStart  bool   // Assign work but don't start session
	slingCreate   bool   // Create polecat if it doesn't exist
)

var slingCmd = &cobra.Command{
	Use:   "sling <thing> <target>",
	Short: "Unified work dispatch command",
	Long: `Sling work at an agent - the universal Gas Town work dispatch.

This command implements spawn + assign + pin in one operation.
Based on the Universal Gas Town Propulsion Principle:

  "If you find something on your hook, YOU RUN IT."

SLING MECHANICS:
  ┌─────────┐      ┌───────────────────────────────────────────┐
  │  THING  │─────▶│              SLING PIPELINE               │
  └─────────┘      │                                           │
   proto           │  1. SPAWN   Proto → Molecule instance     │
   issue           │  2. ASSIGN  Molecule → Target agent       │
   epic            │  3. PIN     Work → Agent's hook           │
                   │  4. IGNITE  Session starts automatically  │
                   │                                           │
                   │        ┌─────────────────────────────┐    │
                   │        │  🪝 TARGET's HOOK           │    │
                   │        │  └── [work lands here]     │    │
                   │        └─────────────────────────────┘    │
                   └───────────────────────────────────────────┘
                                        │
                                        ▼
                              Agent runs the work!

THING TYPES:
  proto     Molecule template name (e.g., "feature", "bugfix")
  issue     Beads issue ID (e.g., "gt-abc123")
  epic      Epic ID for batch dispatch

TARGET FORMATS:
  gastown/Toast        → Polecat in rig (auto-starts session)
  gastown/crew/dave    → Crew member (human-managed, no auto-start)
  gastown/witness      → Rig's Witness
  gastown/refinery     → Rig's Refinery
  deacon/              → Global Deacon
  mayor/               → Town Mayor (human-managed)

Examples:
  gt sling feature gastown/Toast           # Spawn feature, sling to polecat
  gt sling gt-abc gastown/Nux -m bugfix    # Issue with workflow
  gt sling patrol deacon/ --wisp           # Patrol wisp to deacon
  gt sling version-bump beads/crew/dave    # Mol to crew member
  gt sling epic-123 mayor/                 # Epic to mayor`,
	Args: cobra.ExactArgs(2),
	RunE: runSling,
}

func init() {
	slingCmd.Flags().BoolVar(&slingWisp, "wisp", false, "Create wisp (burned on complete)")
	slingCmd.Flags().StringVarP(&slingMolecule, "molecule", "m", "", "Molecule proto when slinging an issue")
	slingCmd.Flags().IntVarP(&slingPriority, "priority", "p", -1, "Override priority (0-4)")
	slingCmd.Flags().BoolVar(&slingForce, "force", false, "Re-sling even if hook has work")
	slingCmd.Flags().BoolVar(&slingNoStart, "no-start", false, "Assign work but don't start session")
	slingCmd.Flags().BoolVar(&slingCreate, "create", false, "Create polecat if it doesn't exist")

	rootCmd.AddCommand(slingCmd)
}

// SlingThing represents what's being slung.
type SlingThing struct {
	Kind    string // "proto", "issue", or "epic"
	ID      string // The identifier (proto name or issue ID)
	Proto   string // If Kind=="issue" and --molecule set, the proto name
	IsWisp  bool   // If --wisp flag set
}

// SlingTarget represents who's being slung at.
type SlingTarget struct {
	Kind string // "polecat", "deacon", "witness", "refinery"
	Rig  string // Rig name (empty for town-level agents)
	Name string // Agent name (for polecats)
}

func runSling(cmd *cobra.Command, args []string) error {
	thingArg := args[0]
	targetArg := args[1]

	// Find workspace
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	// Parse target first (needed to determine rig context)
	target, err := parseSlingTarget(targetArg, townRoot)
	if err != nil {
		return fmt.Errorf("invalid target: %w", err)
	}

	// Get rig context
	rigPath := filepath.Join(townRoot, target.Rig)
	beadsPath := rigPath

	// Parse thing (needs beads context for proto lookup)
	thing, err := parseSlingThing(thingArg, beadsPath)
	if err != nil {
		return fmt.Errorf("invalid thing: %w", err)
	}

	// Apply flags to thing
	thing.Proto = slingMolecule
	thing.IsWisp = slingWisp

	fmt.Printf("Slinging %s %s at %s\n",
		thing.Kind, style.Bold.Render(thing.ID),
		style.Bold.Render(targetArg))

	// Route based on target kind
	switch target.Kind {
	case "polecat":
		return slingToPolecat(townRoot, target, thing)
	case "crew":
		return slingToCrew(townRoot, target, thing)
	case "deacon":
		return slingToDeacon(townRoot, target, thing)
	case "witness":
		return slingToWitness(townRoot, target, thing)
	case "refinery":
		return slingToRefinery(townRoot, target, thing)
	case "mayor":
		return slingToMayor(townRoot, target, thing)
	default:
		return fmt.Errorf("unknown target kind: %s", target.Kind)
	}
}

// parseSlingThing parses the <thing> argument.
// Returns the kind (proto, issue, epic) and ID.
func parseSlingThing(arg, beadsPath string) (*SlingThing, error) {
	// Check if it looks like an issue ID (has a prefix like gt-, bd-, hq-)
	if looksLikeIssueID(arg) {
		// Fetch the issue to check its type
		b := beads.New(beadsPath)
		issue, err := b.Show(arg)
		if err != nil {
			return nil, fmt.Errorf("issue not found: %s", arg)
		}

		kind := "issue"
		if issue.Type == "epic" {
			kind = "epic"
		}

		return &SlingThing{
			Kind: kind,
			ID:   arg,
		}, nil
	}

	// Otherwise, assume it's a proto name
	// Validate that the proto exists in the catalog
	catalog, err := loadMoleculeCatalog(beadsPath)
	if err != nil {
		return nil, fmt.Errorf("loading catalog: %w", err)
	}

	// Try both the exact name and with "mol-" prefix
	protoID := arg
	if catalog.Get(protoID) == nil {
		protoID = "mol-" + arg
		if catalog.Get(protoID) == nil {
			return nil, fmt.Errorf("proto not found: %s (tried %s and mol-%s)", arg, arg, arg)
		}
	}

	return &SlingThing{
		Kind: "proto",
		ID:   protoID,
	}, nil
}

// parseSlingTarget parses the <target> argument.
// Format: polecat/name, deacon/, witness/, refinery/
// Or with rig: gastown/polecat/name, gastown/witness
func parseSlingTarget(arg, townRoot string) (*SlingTarget, error) {
	parts := strings.Split(arg, "/")

	// Handle various formats
	switch len(parts) {
	case 1:
		// Single word like "deacon" - need rig context
		rigName, err := inferRigFromCwd(townRoot)
		if err != nil {
			return nil, fmt.Errorf("cannot infer rig: %w", err)
		}
		return parseAgentKind(parts[0], "", rigName)

	case 2:
		// Could be: polecat/name, rig/role, or role/ (trailing slash)
		first, second := parts[0], parts[1]

		// Check for trailing slash (e.g., "deacon/")
		if second == "" {
			rigName, err := inferRigFromCwd(townRoot)
			if err != nil {
				return nil, fmt.Errorf("cannot infer rig: %w", err)
			}
			return parseAgentKind(first, "", rigName)
		}

		// Check if first is a known role
		if isAgentRole(first) {
			// It's role/name (e.g., polecat/alpha)
			rigName, err := inferRigFromCwd(townRoot)
			if err != nil {
				return nil, fmt.Errorf("cannot infer rig: %w", err)
			}
			return parseAgentKind(first, second, rigName)
		}

		// Otherwise it's rig/role (e.g., gastown/deacon)
		return parseAgentKind(second, "", first)

	case 3:
		// rig/role/name (e.g., gastown/polecat/alpha)
		rigName, role, name := parts[0], parts[1], parts[2]
		return parseAgentKind(role, name, rigName)

	default:
		return nil, fmt.Errorf("invalid target format: %s", arg)
	}
}

// parseAgentKind creates a SlingTarget from parsed components.
func parseAgentKind(role, name, rigName string) (*SlingTarget, error) {
	role = strings.ToLower(role)

	switch role {
	case "polecat", "polecats":
		if name == "" {
			return nil, fmt.Errorf("polecat target requires a name (e.g., polecat/alpha)")
		}
		return &SlingTarget{Kind: "polecat", Rig: rigName, Name: name}, nil

	case "crew":
		if name == "" {
			return nil, fmt.Errorf("crew target requires a name (e.g., crew/dave)")
		}
		return &SlingTarget{Kind: "crew", Rig: rigName, Name: name}, nil

	case "deacon":
		return &SlingTarget{Kind: "deacon", Rig: rigName}, nil

	case "witness":
		return &SlingTarget{Kind: "witness", Rig: rigName}, nil

	case "refinery":
		return &SlingTarget{Kind: "refinery", Rig: rigName}, nil

	case "mayor":
		// Mayor is town-level, rig is ignored
		return &SlingTarget{Kind: "mayor", Rig: ""}, nil

	default:
		// Might be a polecat name without "polecat/" prefix
		// Try to detect by checking if it's a valid rig name
		return &SlingTarget{Kind: "polecat", Rig: rigName, Name: role}, nil
	}
}

// isAgentRole returns true if the string is a known agent role.
func isAgentRole(s string) bool {
	switch strings.ToLower(s) {
	case "polecat", "polecats", "deacon", "witness", "refinery", "crew", "mayor":
		return true
	}
	return false
}

// looksLikeIssueID returns true if the string looks like a beads issue ID.
func looksLikeIssueID(s string) bool {
	// Issue IDs have a prefix followed by a dash
	// Common prefixes: gt-, bd-, hq-
	prefixes := []string{"gt-", "bd-", "hq-", "beads-"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// slingToPolecat handles slinging work to a polecat.
func slingToPolecat(townRoot string, target *SlingTarget, thing *SlingThing) error {
	rigsConfigPath := filepath.Join(townRoot, "mayor", "rigs.json")
	rigsConfig, err := config.LoadRigsConfig(rigsConfigPath)
	if err != nil {
		rigsConfig = &config.RigsConfig{Rigs: make(map[string]config.RigEntry)}
	}

	g := git.NewGit(townRoot)
	rigMgr := rig.NewManager(townRoot, rigsConfig, g)
	r, err := rigMgr.GetRig(target.Rig)
	if err != nil {
		return fmt.Errorf("rig '%s' not found", target.Rig)
	}

	// Get polecat manager
	polecatGit := git.NewGit(r.Path)
	polecatMgr := polecat.NewManager(r, polecatGit)

	polecatName := target.Name
	polecatAddress := fmt.Sprintf("%s/%s", target.Rig, polecatName)

	// Router for mail operations
	router := mail.NewRouter(r.Path)

	// Check if polecat exists
	existingPolecat, err := polecatMgr.Get(polecatName)
	polecatExists := err == nil

	if polecatExists {
		// Check for existing work on hook (unless --force)
		if !slingForce {
			if err := checkHookCollision(polecatAddress, r.Path); err != nil {
				return err
			}
		}

		// Check for uncommitted work
		pGit := git.NewGit(existingPolecat.ClonePath)
		workStatus, checkErr := pGit.CheckUncommittedWork()
		if checkErr == nil && !workStatus.Clean() {
			fmt.Printf("\n%s Polecat has uncommitted work:\n", style.Warning.Render("⚠"))
			if workStatus.HasUncommittedChanges {
				fmt.Printf("  • %d uncommitted change(s)\n", len(workStatus.ModifiedFiles)+len(workStatus.UntrackedFiles))
			}
			if workStatus.StashCount > 0 {
				fmt.Printf("  • %d stash(es)\n", workStatus.StashCount)
			}
			if workStatus.UnpushedCommits > 0 {
				fmt.Printf("  • %d unpushed commit(s)\n", workStatus.UnpushedCommits)
			}
			fmt.Println()
			if !slingForce {
				return fmt.Errorf("polecat '%s' has uncommitted work\nUse --force to proceed anyway", polecatName)
			}
			fmt.Printf("%s Proceeding with --force\n", style.Dim.Render("Warning:"))
		}

		// Check for unread mail
		mailbox, mailErr := router.GetMailbox(polecatAddress)
		if mailErr == nil {
			_, unread, _ := mailbox.Count()
			if unread > 0 && !slingForce {
				return fmt.Errorf("polecat '%s' has %d unread message(s)\nUse --force to override", polecatName, unread)
			} else if unread > 0 {
				fmt.Printf("%s Polecat has %d unread message(s), proceeding with --force\n",
					style.Dim.Render("Warning:"), unread)
			}
		}

		// Recreate polecat with fresh worktree
		fmt.Printf("Recreating polecat %s with fresh worktree...\n", polecatName)
		if _, err = polecatMgr.Recreate(polecatName, slingForce); err != nil {
			return fmt.Errorf("recreating polecat: %w", err)
		}
		fmt.Printf("%s Fresh worktree created\n", style.Bold.Render("✓"))
	} else if err == polecat.ErrPolecatNotFound {
		if !slingCreate {
			suggestions := suggest.FindSimilar(polecatName, r.Polecats, 3)
			hint := fmt.Sprintf("Or use --create to create: gt sling %s %s/%s --create",
				thing.ID, target.Rig, polecatName)
			return fmt.Errorf("%s", suggest.FormatSuggestion("Polecat", polecatName, suggestions, hint))
		}
		fmt.Printf("Creating polecat %s...\n", polecatName)
		if _, err = polecatMgr.Add(polecatName); err != nil {
			return fmt.Errorf("creating polecat: %w", err)
		}
	} else {
		return fmt.Errorf("getting polecat: %w", err)
	}

	beadsPath := r.Path

	// Sync beads
	if err := syncBeads(beadsPath, true); err != nil {
		fmt.Printf("%s beads sync: %v\n", style.Dim.Render("Warning:"), err)
	}

	// Process the thing based on its kind
	var issueID string
	var moleculeCtx *MoleculeContext

	switch thing.Kind {
	case "proto":
		// Spawn molecule from proto
		issueID, moleculeCtx, err = spawnMoleculeFromProto(beadsPath, thing, polecatAddress)
		if err != nil {
			return err
		}

	case "issue":
		issueID = thing.ID
		if thing.Proto != "" {
			// Sling issue with molecule workflow
			issueID, moleculeCtx, err = spawnMoleculeOnIssue(beadsPath, thing, polecatAddress)
			if err != nil {
				return err
			}
		}

	case "epic":
		// Epics go to refinery, not polecats
		return fmt.Errorf("epics should be slung at refinery/, not polecat/")
	}

	// Assign issue to polecat
	if err := polecatMgr.AssignIssue(polecatName, issueID); err != nil {
		return fmt.Errorf("assigning issue: %w", err)
	}
	fmt.Printf("%s Assigned %s to %s\n", style.Bold.Render("✓"), issueID, polecatAddress)

	// Pin to hook (update handoff bead with attachment)
	if err := pinToHook(beadsPath, polecatAddress, issueID, moleculeCtx); err != nil {
		fmt.Printf("%s Could not pin to hook: %v\n", style.Dim.Render("Warning:"), err)
	} else {
		fmt.Printf("%s Pinned to hook\n", style.Bold.Render("✓"))
	}

	// Sync beads
	if err := syncBeads(beadsPath, false); err != nil {
		fmt.Printf("%s beads push: %v\n", style.Dim.Render("Warning:"), err)
	}

	if slingNoStart {
		fmt.Printf("\n  %s\n", style.Dim.Render("Use 'gt session start' to start the session"))
		return nil
	}

	// Fetch the issue for mail content
	b := beads.New(beadsPath)
	issue, _ := b.Show(issueID)
	var beadsIssue *BeadsIssue
	if issue != nil {
		beadsIssue = &BeadsIssue{
			ID:          issue.ID,
			Title:       issue.Title,
			Description: issue.Description,
			Priority:    issue.Priority,
			Type:        issue.Type,
			Status:      issue.Status,
		}
	}

	// Send work assignment mail
	workMsg := buildWorkAssignmentMail(beadsIssue, "", polecatAddress, moleculeCtx)
	fmt.Printf("Sending work assignment to %s inbox...\n", polecatAddress)
	if err := router.Send(workMsg); err != nil {
		return fmt.Errorf("sending work assignment: %w", err)
	}
	fmt.Printf("%s Work assignment sent\n", style.Bold.Render("✓"))

	// Start session
	t := tmux.NewTmux()
	sessMgr := session.NewManager(t, r)

	running, _ := sessMgr.IsRunning(polecatName)
	if running {
		fmt.Printf("Session already running, notifying to check inbox...\n")
		time.Sleep(500 * time.Millisecond)
	} else {
		fmt.Printf("Starting session for %s...\n", polecatAddress)
		if err := sessMgr.Start(polecatName, session.StartOptions{}); err != nil {
			return fmt.Errorf("starting session: %w", err)
		}
		time.Sleep(3 * time.Second)
	}

	fmt.Printf("%s Session started. Attach with: %s\n",
		style.Bold.Render("✓"),
		style.Dim.Render(fmt.Sprintf("gt session at %s", polecatAddress)))

	// Nudge polecat
	sessionName := sessMgr.SessionName(polecatName)
	nudgeMsg := fmt.Sprintf("You have a work assignment. Run 'gt mail inbox' to see it, then start working on issue %s.", issueID)
	if err := t.NudgeSession(sessionName, nudgeMsg); err != nil {
		fmt.Printf("  %s\n", style.Dim.Render(fmt.Sprintf("Warning: could not nudge: %v", err)))
	} else {
		fmt.Printf("  %s\n", style.Dim.Render("Polecat nudged to start working"))
	}

	// Notify Witness
	townRouter := mail.NewRouter(townRoot)
	witnessAddr := fmt.Sprintf("%s/witness", target.Rig)
	sender := detectSender()
	spawnNotification := &mail.Message{
		To:      witnessAddr,
		From:    sender,
		Subject: fmt.Sprintf("SLING: %s starting on %s", polecatName, issueID),
		Body:    fmt.Sprintf("Polecat slung.\n\nPolecat: %s\nIssue: %s\nSession: %s\nSlung by: %s", polecatName, issueID, sessionName, sender),
	}
	if err := townRouter.Send(spawnNotification); err != nil {
		fmt.Printf("  %s\n", style.Dim.Render(fmt.Sprintf("Warning: could not notify witness: %v", err)))
	} else {
		fmt.Printf("  %s\n", style.Dim.Render("Witness notified"))
	}

	return nil
}

// slingToDeacon handles slinging work to the deacon.
func slingToDeacon(townRoot string, target *SlingTarget, thing *SlingThing) error {
	if thing.Kind != "proto" {
		return fmt.Errorf("deacon only accepts protos (like 'patrol'), not issues")
	}

	if !thing.IsWisp {
		fmt.Printf("%s Deacon work should be ephemeral. Consider using --wisp\n",
			style.Dim.Render("Note:"))
	}

	// For deacon, we just need to update its hook and send mail
	beadsPath := filepath.Join(townRoot, target.Rig)

	// Sync beads
	if err := syncBeads(beadsPath, true); err != nil {
		fmt.Printf("%s beads sync: %v\n", style.Dim.Render("Warning:"), err)
	}

	// Spawn the molecule from proto
	deaconAddress := fmt.Sprintf("%s/deacon", target.Rig)
	issueID, moleculeCtx, err := spawnMoleculeFromProto(beadsPath, thing, deaconAddress)
	if err != nil {
		return err
	}

	// Pin to deacon's hook
	if err := pinToHook(beadsPath, deaconAddress, issueID, moleculeCtx); err != nil {
		fmt.Printf("%s Could not pin to hook: %v\n", style.Dim.Render("Warning:"), err)
	} else {
		fmt.Printf("%s Pinned to deacon hook\n", style.Bold.Render("✓"))
	}

	// Sync beads
	if err := syncBeads(beadsPath, false); err != nil {
		fmt.Printf("%s beads push: %v\n", style.Dim.Render("Warning:"), err)
	}

	fmt.Printf("%s Deacon will run %s on next patrol\n",
		style.Bold.Render("✓"), thing.ID)

	return nil
}

// slingToCrew handles slinging work to a crew member.
// Crew members are persistent, human-managed workers - no session start.
func slingToCrew(townRoot string, target *SlingTarget, thing *SlingThing) error {
	beadsPath := filepath.Join(townRoot, target.Rig)
	crewAddress := fmt.Sprintf("%s/crew/%s", target.Rig, target.Name)

	// Verify crew member exists
	crewPath := filepath.Join(townRoot, target.Rig, "crew", target.Name)
	if _, err := os.Stat(crewPath); os.IsNotExist(err) {
		return fmt.Errorf("crew member '%s' not found at %s", target.Name, crewPath)
	}

	// Check for existing work on hook (unless --force)
	if !slingForce {
		if err := checkHookCollision(crewAddress, beadsPath); err != nil {
			return err
		}
	}

	// Sync beads
	if err := syncBeads(beadsPath, true); err != nil {
		fmt.Printf("%s beads sync: %v\n", style.Dim.Render("Warning:"), err)
	}

	// Process the thing based on its kind
	var issueID string
	var moleculeCtx *MoleculeContext
	var err error

	switch thing.Kind {
	case "proto":
		issueID, moleculeCtx, err = spawnMoleculeFromProto(beadsPath, thing, crewAddress)
		if err != nil {
			return err
		}
	case "issue":
		issueID = thing.ID
		if thing.Proto != "" {
			issueID, moleculeCtx, err = spawnMoleculeOnIssue(beadsPath, thing, crewAddress)
			if err != nil {
				return err
			}
		}
	case "epic":
		// Epics can be slung to crew for manual processing
		issueID = thing.ID
	}

	// Pin to hook
	if err := pinToHook(beadsPath, crewAddress, issueID, moleculeCtx); err != nil {
		fmt.Printf("%s Could not pin to hook: %v\n", style.Dim.Render("Warning:"), err)
	} else {
		fmt.Printf("%s Pinned to crew hook\n", style.Bold.Render("✓"))
	}

	// Sync beads
	if err := syncBeads(beadsPath, false); err != nil {
		fmt.Printf("%s beads push: %v\n", style.Dim.Render("Warning:"), err)
	}

	// Send work assignment mail (crew will see it on next session start)
	router := mail.NewRouter(townRoot)
	b := beads.New(beadsPath)
	issue, _ := b.Show(issueID)
	var beadsIssue *BeadsIssue
	if issue != nil {
		beadsIssue = &BeadsIssue{
			ID:          issue.ID,
			Title:       issue.Title,
			Description: issue.Description,
			Priority:    issue.Priority,
			Type:        issue.Type,
			Status:      issue.Status,
		}
	}

	workMsg := buildWorkAssignmentMail(beadsIssue, "", crewAddress, moleculeCtx)
	if err := router.Send(workMsg); err != nil {
		fmt.Printf("%s Could not send mail: %v\n", style.Dim.Render("Warning:"), err)
	} else {
		fmt.Printf("%s Work assignment sent to %s\n", style.Bold.Render("✓"), crewAddress)
	}

	fmt.Printf("\n%s Crew member will see work on next session start\n",
		style.Bold.Render("✓"))
	fmt.Printf("  %s\n", style.Dim.Render("(Crew sessions are human-managed, not auto-started)"))

	return nil
}

// slingToWitness handles slinging work to the witness.
func slingToWitness(townRoot string, target *SlingTarget, thing *SlingThing) error {
	beadsPath := filepath.Join(townRoot, target.Rig)
	witnessAddress := fmt.Sprintf("%s/witness", target.Rig)

	if !thing.IsWisp {
		fmt.Printf("%s Witness work should be ephemeral. Consider using --wisp\n",
			style.Dim.Render("Note:"))
	}

	// Check for existing work on hook (unless --force)
	if !slingForce {
		if err := checkHookCollision(witnessAddress, beadsPath); err != nil {
			return err
		}
	}

	// Sync beads
	if err := syncBeads(beadsPath, true); err != nil {
		fmt.Printf("%s beads sync: %v\n", style.Dim.Render("Warning:"), err)
	}

	// Process the thing
	var issueID string
	var moleculeCtx *MoleculeContext
	var err error

	switch thing.Kind {
	case "proto":
		issueID, moleculeCtx, err = spawnMoleculeFromProto(beadsPath, thing, witnessAddress)
		if err != nil {
			return err
		}
	case "issue":
		issueID = thing.ID
		if thing.Proto != "" {
			issueID, moleculeCtx, err = spawnMoleculeOnIssue(beadsPath, thing, witnessAddress)
			if err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("witness accepts protos or issues, not %s", thing.Kind)
	}

	// Pin to witness hook
	if err := pinToHook(beadsPath, witnessAddress, issueID, moleculeCtx); err != nil {
		fmt.Printf("%s Could not pin to hook: %v\n", style.Dim.Render("Warning:"), err)
	} else {
		fmt.Printf("%s Pinned to witness hook\n", style.Bold.Render("✓"))
	}

	// Sync beads
	if err := syncBeads(beadsPath, false); err != nil {
		fmt.Printf("%s beads push: %v\n", style.Dim.Render("Warning:"), err)
	}

	fmt.Printf("%s Witness will run %s on next patrol\n",
		style.Bold.Render("✓"), thing.ID)

	return nil
}

// slingToRefinery handles slinging work to the refinery.
func slingToRefinery(townRoot string, target *SlingTarget, thing *SlingThing) error {
	beadsPath := filepath.Join(townRoot, target.Rig)
	refineryAddress := fmt.Sprintf("%s/refinery", target.Rig)

	// Check for existing work on hook (unless --force)
	if !slingForce {
		if err := checkHookCollision(refineryAddress, beadsPath); err != nil {
			return err
		}
	}

	// Sync beads
	if err := syncBeads(beadsPath, true); err != nil {
		fmt.Printf("%s beads sync: %v\n", style.Dim.Render("Warning:"), err)
	}

	// Process the thing
	var issueID string
	var moleculeCtx *MoleculeContext
	var err error

	switch thing.Kind {
	case "proto":
		issueID, moleculeCtx, err = spawnMoleculeFromProto(beadsPath, thing, refineryAddress)
		if err != nil {
			return err
		}
	case "issue":
		issueID = thing.ID
		if thing.Proto != "" {
			issueID, moleculeCtx, err = spawnMoleculeOnIssue(beadsPath, thing, refineryAddress)
			if err != nil {
				return err
			}
		}
	case "epic":
		// Epics go to refinery for batch dispatch to polecats
		issueID = thing.ID
	}

	// Pin to refinery hook
	if err := pinToHook(beadsPath, refineryAddress, issueID, moleculeCtx); err != nil {
		fmt.Printf("%s Could not pin to hook: %v\n", style.Dim.Render("Warning:"), err)
	} else {
		fmt.Printf("%s Pinned to refinery hook\n", style.Bold.Render("✓"))
	}

	// Sync beads
	if err := syncBeads(beadsPath, false); err != nil {
		fmt.Printf("%s beads push: %v\n", style.Dim.Render("Warning:"), err)
	}

	fmt.Printf("%s Refinery will process %s on next cycle\n",
		style.Bold.Render("✓"), thing.ID)

	return nil
}

// slingToMayor handles slinging work to the mayor.
// Mayor is town-level, human-managed - no session start.
func slingToMayor(townRoot string, target *SlingTarget, thing *SlingThing) error {
	// Mayor uses town-level beads
	beadsPath := townRoot
	mayorAddress := "mayor/"

	// Check for existing work on hook (unless --force)
	if !slingForce {
		if err := checkHookCollision(mayorAddress, beadsPath); err != nil {
			return err
		}
	}

	// Sync beads
	if err := syncBeads(beadsPath, true); err != nil {
		fmt.Printf("%s beads sync: %v\n", style.Dim.Render("Warning:"), err)
	}

	// Process the thing
	var issueID string
	var moleculeCtx *MoleculeContext
	var err error

	switch thing.Kind {
	case "proto":
		issueID, moleculeCtx, err = spawnMoleculeFromProto(beadsPath, thing, mayorAddress)
		if err != nil {
			return err
		}
	case "issue":
		issueID = thing.ID
		if thing.Proto != "" {
			issueID, moleculeCtx, err = spawnMoleculeOnIssue(beadsPath, thing, mayorAddress)
			if err != nil {
				return err
			}
		}
	case "epic":
		// Mayor can work epics directly
		issueID = thing.ID
	}

	// Pin to mayor hook
	if err := pinToHook(beadsPath, mayorAddress, issueID, moleculeCtx); err != nil {
		fmt.Printf("%s Could not pin to hook: %v\n", style.Dim.Render("Warning:"), err)
	} else {
		fmt.Printf("%s Pinned to mayor hook\n", style.Bold.Render("✓"))
	}

	// Sync beads
	if err := syncBeads(beadsPath, false); err != nil {
		fmt.Printf("%s beads push: %v\n", style.Dim.Render("Warning:"), err)
	}

	// Send work assignment mail
	router := mail.NewRouter(townRoot)
	b := beads.New(beadsPath)
	issue, _ := b.Show(issueID)
	var beadsIssue *BeadsIssue
	if issue != nil {
		beadsIssue = &BeadsIssue{
			ID:          issue.ID,
			Title:       issue.Title,
			Description: issue.Description,
			Priority:    issue.Priority,
			Type:        issue.Type,
			Status:      issue.Status,
		}
	}

	workMsg := buildWorkAssignmentMail(beadsIssue, "", mayorAddress, moleculeCtx)
	if err := router.Send(workMsg); err != nil {
		fmt.Printf("%s Could not send mail: %v\n", style.Dim.Render("Warning:"), err)
	} else {
		fmt.Printf("%s Work assignment sent to mayor\n", style.Bold.Render("✓"))
	}

	fmt.Printf("\n%s Mayor will see work on next session start\n",
		style.Bold.Render("✓"))

	return nil
}

// spawnMoleculeFromProto spawns a molecule from a proto template.
func spawnMoleculeFromProto(beadsPath string, thing *SlingThing, assignee string) (string, *MoleculeContext, error) {
	moleculeType := "molecule"
	if thing.IsWisp {
		moleculeType = "wisp"
	}
	fmt.Printf("Spawning %s from proto %s...\n", moleculeType, thing.ID)

	// Use bd mol run to spawn the molecule
	args := []string{"--no-daemon", "mol", "run", thing.ID, "--json"}
	if assignee != "" {
		args = append(args, "--var", "assignee="+assignee)
	}

	// For wisps, use the ephemeral storage location
	workDir := beadsPath
	if thing.IsWisp {
		wispPath := filepath.Join(beadsPath, ".beads-wisp")
		// Check if wisp storage exists
		if _, err := os.Stat(wispPath); err == nil {
			// Use wisp storage - pass --db to point bd at the wisp directory
			args = append([]string{"--db", filepath.Join(wispPath, "beads.db")}, args...)
			fmt.Printf("  Using ephemeral storage: %s\n", style.Dim.Render(".beads-wisp/"))
		} else {
			fmt.Printf("  %s wisp storage not found, using regular storage\n",
				style.Dim.Render("Note:"))
		}
	}

	cmd := exec.Command("bd", args...)
	cmd.Dir = workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return "", nil, fmt.Errorf("running molecule: %s", errMsg)
		}
		return "", nil, fmt.Errorf("running molecule: %w", err)
	}

	// Parse result
	var molResult struct {
		RootID    string            `json:"root_id"`
		IDMapping map[string]string `json:"id_mapping"`
		Created   int               `json:"created"`
		Assignee  string            `json:"assignee"`
		Pinned    bool              `json:"pinned"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &molResult); err != nil {
		return "", nil, fmt.Errorf("parsing molecule result: %w", err)
	}

	fmt.Printf("%s %s spawned: %s (%d steps)\n",
		style.Bold.Render("✓"), moleculeType, molResult.RootID, molResult.Created-1)

	moleculeCtx := &MoleculeContext{
		MoleculeID:  thing.ID,
		RootIssueID: molResult.RootID,
		TotalSteps:  molResult.Created - 1,
		StepNumber:  1,
		IsWisp:      thing.IsWisp,
	}

	return molResult.RootID, moleculeCtx, nil
}

// spawnMoleculeOnIssue spawns a molecule workflow on an existing issue.
func spawnMoleculeOnIssue(beadsPath string, thing *SlingThing, assignee string) (string, *MoleculeContext, error) {
	fmt.Printf("Running molecule %s on issue %s...\n", thing.Proto, thing.ID)

	args := []string{"--no-daemon", "mol", "run", thing.Proto,
		"--var", "issue=" + thing.ID, "--json"}
	if assignee != "" {
		args = append(args, "--var", "assignee="+assignee)
	}

	cmd := exec.Command("bd", args...)
	cmd.Dir = beadsPath

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return "", nil, fmt.Errorf("running molecule: %s", errMsg)
		}
		return "", nil, fmt.Errorf("running molecule: %w", err)
	}

	var molResult struct {
		RootID    string            `json:"root_id"`
		IDMapping map[string]string `json:"id_mapping"`
		Created   int               `json:"created"`
		Assignee  string            `json:"assignee"`
		Pinned    bool              `json:"pinned"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &molResult); err != nil {
		return "", nil, fmt.Errorf("parsing molecule result: %w", err)
	}

	fmt.Printf("%s Molecule %s applied to %s (%d steps)\n",
		style.Bold.Render("✓"), thing.Proto, thing.ID, molResult.Created-1)

	moleculeCtx := &MoleculeContext{
		MoleculeID:  thing.Proto,
		RootIssueID: molResult.RootID,
		TotalSteps:  molResult.Created - 1,
		StepNumber:  1,
		IsWisp:      thing.IsWisp,
	}

	return molResult.RootID, moleculeCtx, nil
}

// checkHookCollision checks if the agent's hook already has work.
func checkHookCollision(agentAddress, beadsPath string) error {
	// Parse agent address to get the role for handoff bead lookup
	parts := strings.Split(agentAddress, "/")
	var role string
	if len(parts) >= 2 {
		role = parts[len(parts)-1] // Last part is the name/role
	} else {
		role = parts[0]
	}

	b := beads.New(beadsPath)
	handoff, err := b.FindHandoffBead(role)
	if err != nil {
		// Can't check, assume OK
		return nil
	}

	if handoff == nil {
		// No handoff bead exists, no collision
		return nil
	}

	// Check if there's an attached molecule
	attachment := beads.ParseAttachmentFields(handoff)
	if attachment != nil && attachment.AttachedMolecule != "" {
		return fmt.Errorf("hook already occupied by %s\nUse --force to re-sling",
			attachment.AttachedMolecule)
	}

	return nil
}

// pinToHook pins work to an agent's hook by updating their handoff bead.
func pinToHook(beadsPath, agentAddress, issueID string, moleculeCtx *MoleculeContext) error {
	// Parse agent address to get the role
	parts := strings.Split(agentAddress, "/")
	var role string
	if len(parts) >= 2 {
		role = parts[len(parts)-1]
	} else {
		role = parts[0]
	}

	b := beads.New(beadsPath)

	// Get or create handoff bead
	handoff, err := b.GetOrCreateHandoffBead(role)
	if err != nil {
		return fmt.Errorf("getting handoff bead: %w", err)
	}

	// Determine what to attach
	attachedMolecule := issueID
	if moleculeCtx != nil && moleculeCtx.RootIssueID != "" {
		attachedMolecule = moleculeCtx.RootIssueID
	}

	// Attach molecule to handoff bead
	_, err = b.AttachMolecule(handoff.ID, attachedMolecule)
	if err != nil {
		return fmt.Errorf("attaching molecule: %w", err)
	}

	return nil
}
