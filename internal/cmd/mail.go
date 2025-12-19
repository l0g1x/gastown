package cmd

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/mail"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/workspace"
)

// Mail command flags
var (
	mailSubject     string
	mailBody        string
	mailPriority    string
	mailType        string
	mailReplyTo     string
	mailNotify      bool
	mailInboxJSON   bool
	mailReadJSON    bool
	mailInboxUnread bool
	mailCheckInject bool
	mailCheckJSON   bool
	mailThreadJSON  bool
)

var mailCmd = &cobra.Command{
	Use:   "mail",
	Short: "Agent messaging system",
	Long: `Send and receive messages between agents.

The mail system allows Mayor, polecats, and the Refinery to communicate.
Messages are stored in beads as issues with type=message.`,
}

var mailSendCmd = &cobra.Command{
	Use:   "send <address>",
	Short: "Send a message",
	Long: `Send a message to an agent.

Addresses:
  mayor/           - Send to Mayor
  <rig>/refinery   - Send to a rig's Refinery
  <rig>/<polecat>  - Send to a specific polecat
  <rig>/           - Broadcast to a rig

Message types:
  task          - Required processing
  scavenge      - Optional first-come work
  notification  - Informational (default)
  reply         - Response to message

Priority levels:
  low, normal (default), high, urgent

Examples:
  gt mail send gastown/Toast -s "Status check" -m "How's that bug fix going?"
  gt mail send mayor/ -s "Work complete" -m "Finished gt-abc"
  gt mail send gastown/ -s "All hands" -m "Swarm starting" --notify
  gt mail send gastown/Toast -s "Task" -m "Fix bug" --type task --priority high
  gt mail send mayor/ -s "Re: Status" -m "Done" --reply-to msg-abc123`,
	Args: cobra.ExactArgs(1),
	RunE: runMailSend,
}

var mailInboxCmd = &cobra.Command{
	Use:   "inbox [address]",
	Short: "Check inbox",
	Long: `Check messages in an inbox.

If no address is specified, shows the current context's inbox.

Examples:
  gt mail inbox             # Current context
  gt mail inbox mayor/      # Mayor's inbox
  gt mail inbox gastown/Toast  # Polecat's inbox`,
	Args: cobra.MaximumNArgs(1),
	RunE: runMailInbox,
}

var mailReadCmd = &cobra.Command{
	Use:   "read <message-id>",
	Short: "Read a message",
	Long: `Read a specific message and mark it as read.

The message ID can be found from 'gt mail inbox'.`,
	Args: cobra.ExactArgs(1),
	RunE: runMailRead,
}

var mailDeleteCmd = &cobra.Command{
	Use:   "delete <message-id>",
	Short: "Delete a message",
	Long: `Delete (acknowledge) a message.

This closes the message in beads.`,
	Args: cobra.ExactArgs(1),
	RunE: runMailDelete,
}

var mailCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Check for new mail (for hooks)",
	Long: `Check for new mail - useful for Claude Code hooks.

Exit codes (normal mode):
  0 - New mail available
  1 - No new mail

Exit codes (--inject mode):
  0 - Always (hooks should never block)
  Output: system-reminder if mail exists, silent if no mail

Examples:
  gt mail check             # Simple check
  gt mail check --inject    # For hooks`,
	RunE: runMailCheck,
}

var mailThreadCmd = &cobra.Command{
	Use:   "thread <thread-id>",
	Short: "View a message thread",
	Long: `View all messages in a conversation thread.

Shows messages in chronological order (oldest first).

Examples:
  gt mail thread thread-abc123`,
	Args: cobra.ExactArgs(1),
	RunE: runMailThread,
}

func init() {
	// Send flags
	mailSendCmd.Flags().StringVarP(&mailSubject, "subject", "s", "", "Message subject (required)")
	mailSendCmd.Flags().StringVarP(&mailBody, "message", "m", "", "Message body")
	mailSendCmd.Flags().StringVar(&mailPriority, "priority", "normal", "Message priority (low, normal, high, urgent)")
	mailSendCmd.Flags().StringVar(&mailType, "type", "notification", "Message type (task, scavenge, notification, reply)")
	mailSendCmd.Flags().StringVar(&mailReplyTo, "reply-to", "", "Message ID this is replying to")
	mailSendCmd.Flags().BoolVarP(&mailNotify, "notify", "n", false, "Send tmux notification to recipient")
	mailSendCmd.MarkFlagRequired("subject")

	// Inbox flags
	mailInboxCmd.Flags().BoolVar(&mailInboxJSON, "json", false, "Output as JSON")
	mailInboxCmd.Flags().BoolVarP(&mailInboxUnread, "unread", "u", false, "Show only unread messages")

	// Read flags
	mailReadCmd.Flags().BoolVar(&mailReadJSON, "json", false, "Output as JSON")

	// Check flags
	mailCheckCmd.Flags().BoolVar(&mailCheckInject, "inject", false, "Output format for Claude Code hooks")
	mailCheckCmd.Flags().BoolVar(&mailCheckJSON, "json", false, "Output as JSON")

	// Thread flags
	mailThreadCmd.Flags().BoolVar(&mailThreadJSON, "json", false, "Output as JSON")

	// Add subcommands
	mailCmd.AddCommand(mailSendCmd)
	mailCmd.AddCommand(mailInboxCmd)
	mailCmd.AddCommand(mailReadCmd)
	mailCmd.AddCommand(mailDeleteCmd)
	mailCmd.AddCommand(mailCheckCmd)
	mailCmd.AddCommand(mailThreadCmd)

	rootCmd.AddCommand(mailCmd)
}

func runMailSend(cmd *cobra.Command, args []string) error {
	to := args[0]

	// Find workspace - we need a directory with .beads
	workDir, err := findBeadsWorkDir()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	// Determine sender
	from := detectSender()

	// Create message
	msg := &mail.Message{
		From:    from,
		To:      to,
		Subject: mailSubject,
		Body:    mailBody,
	}

	// Set priority
	msg.Priority = mail.ParsePriority(mailPriority)
	if mailNotify && msg.Priority == mail.PriorityNormal {
		msg.Priority = mail.PriorityHigh
	}

	// Set message type
	msg.Type = mail.ParseMessageType(mailType)

	// Handle reply-to: auto-set type to reply and look up thread
	if mailReplyTo != "" {
		msg.ReplyTo = mailReplyTo
		if msg.Type == mail.TypeNotification {
			msg.Type = mail.TypeReply
		}

		// Look up original message to get thread ID
		router := mail.NewRouter(workDir)
		mailbox, err := router.GetMailbox(from)
		if err == nil {
			if original, err := mailbox.Get(mailReplyTo); err == nil {
				msg.ThreadID = original.ThreadID
			}
		}
	}

	// Generate thread ID for new threads
	if msg.ThreadID == "" {
		msg.ThreadID = generateThreadID()
	}

	// Send via router
	router := mail.NewRouter(workDir)
	if err := router.Send(msg); err != nil {
		return fmt.Errorf("sending message: %w", err)
	}

	fmt.Printf("%s Message sent to %s\n", style.Bold.Render("✓"), to)
	fmt.Printf("  Subject: %s\n", mailSubject)
	if msg.Type != mail.TypeNotification {
		fmt.Printf("  Type: %s\n", msg.Type)
	}

	return nil
}

func runMailInbox(cmd *cobra.Command, args []string) error {
	// Find workspace
	workDir, err := findBeadsWorkDir()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	// Determine which inbox to check
	address := ""
	if len(args) > 0 {
		address = args[0]
	} else {
		address = detectSender()
	}

	// Get mailbox
	router := mail.NewRouter(workDir)
	mailbox, err := router.GetMailbox(address)
	if err != nil {
		return fmt.Errorf("getting mailbox: %w", err)
	}

	// Get messages
	var messages []*mail.Message
	if mailInboxUnread {
		messages, err = mailbox.ListUnread()
	} else {
		messages, err = mailbox.List()
	}
	if err != nil {
		return fmt.Errorf("listing messages: %w", err)
	}

	// JSON output
	if mailInboxJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(messages)
	}

	// Human-readable output
	total, unread, _ := mailbox.Count()
	fmt.Printf("%s Inbox: %s (%d messages, %d unread)\n\n",
		style.Bold.Render("📬"), address, total, unread)

	if len(messages) == 0 {
		fmt.Printf("  %s\n", style.Dim.Render("(no messages)"))
		return nil
	}

	for _, msg := range messages {
		readMarker := "●"
		if msg.Read {
			readMarker = "○"
		}
		typeMarker := ""
		if msg.Type != "" && msg.Type != mail.TypeNotification {
			typeMarker = fmt.Sprintf(" [%s]", msg.Type)
		}
		priorityMarker := ""
		if msg.Priority == mail.PriorityHigh || msg.Priority == mail.PriorityUrgent {
			priorityMarker = " " + style.Bold.Render("!")
		}

		fmt.Printf("  %s %s%s%s\n", readMarker, msg.Subject, typeMarker, priorityMarker)
		fmt.Printf("    %s from %s\n",
			style.Dim.Render(msg.ID),
			msg.From)
		fmt.Printf("    %s\n",
			style.Dim.Render(msg.Timestamp.Format("2006-01-02 15:04")))
	}

	return nil
}

func runMailRead(cmd *cobra.Command, args []string) error {
	msgID := args[0]

	// Find workspace
	workDir, err := findBeadsWorkDir()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	// Determine which inbox
	address := detectSender()

	// Get mailbox and message
	router := mail.NewRouter(workDir)
	mailbox, err := router.GetMailbox(address)
	if err != nil {
		return fmt.Errorf("getting mailbox: %w", err)
	}

	msg, err := mailbox.Get(msgID)
	if err != nil {
		return fmt.Errorf("getting message: %w", err)
	}

	// Mark as read
	mailbox.MarkRead(msgID)

	// JSON output
	if mailReadJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(msg)
	}

	// Human-readable output
	priorityStr := ""
	if msg.Priority == mail.PriorityUrgent {
		priorityStr = " " + style.Bold.Render("[URGENT]")
	} else if msg.Priority == mail.PriorityHigh {
		priorityStr = " " + style.Bold.Render("[HIGH PRIORITY]")
	}

	typeStr := ""
	if msg.Type != "" && msg.Type != mail.TypeNotification {
		typeStr = fmt.Sprintf(" [%s]", msg.Type)
	}

	fmt.Printf("%s %s%s%s\n\n", style.Bold.Render("Subject:"), msg.Subject, typeStr, priorityStr)
	fmt.Printf("From: %s\n", msg.From)
	fmt.Printf("To: %s\n", msg.To)
	fmt.Printf("Date: %s\n", msg.Timestamp.Format("2006-01-02 15:04:05"))
	fmt.Printf("ID: %s\n", style.Dim.Render(msg.ID))

	if msg.ThreadID != "" {
		fmt.Printf("Thread: %s\n", style.Dim.Render(msg.ThreadID))
	}
	if msg.ReplyTo != "" {
		fmt.Printf("Reply-To: %s\n", style.Dim.Render(msg.ReplyTo))
	}

	if msg.Body != "" {
		fmt.Printf("\n%s\n", msg.Body)
	}

	return nil
}

func runMailDelete(cmd *cobra.Command, args []string) error {
	msgID := args[0]

	// Find workspace
	workDir, err := findBeadsWorkDir()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	// Determine which inbox
	address := detectSender()

	// Get mailbox
	router := mail.NewRouter(workDir)
	mailbox, err := router.GetMailbox(address)
	if err != nil {
		return fmt.Errorf("getting mailbox: %w", err)
	}

	if err := mailbox.Delete(msgID); err != nil {
		return fmt.Errorf("deleting message: %w", err)
	}

	fmt.Printf("%s Message deleted\n", style.Bold.Render("✓"))
	return nil
}

// findBeadsWorkDir finds a directory with a .beads database.
// Walks up from CWD looking for .beads/ directory.
func findBeadsWorkDir() (string, error) {
	// First try workspace root
	townRoot, err := workspace.FindFromCwdOrError()
	if err == nil {
		// Check if town root has .beads
		if _, err := os.Stat(filepath.Join(townRoot, ".beads")); err == nil {
			return townRoot, nil
		}
	}

	// Walk up from CWD looking for .beads
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	path := cwd
	for {
		if _, err := os.Stat(filepath.Join(path, ".beads")); err == nil {
			return path, nil
		}

		parent := filepath.Dir(path)
		if parent == path {
			break // Reached root
		}
		path = parent
	}

	return "", fmt.Errorf("no .beads directory found")
}

// detectSender determines the current context's address.
func detectSender() string {
	// Check environment variables (set by session start)
	rig := os.Getenv("GT_RIG")
	polecat := os.Getenv("GT_POLECAT")

	if rig != "" && polecat != "" {
		return fmt.Sprintf("%s/%s", rig, polecat)
	}

	// Check current directory
	cwd, err := os.Getwd()
	if err != nil {
		return "mayor/"
	}

	// If in a rig's polecats directory, extract address
	if strings.Contains(cwd, "/polecats/") {
		parts := strings.Split(cwd, "/polecats/")
		if len(parts) >= 2 {
			rigPath := parts[0]
			polecatPath := strings.Split(parts[1], "/")[0]
			rigName := filepath.Base(rigPath)
			return fmt.Sprintf("%s/%s", rigName, polecatPath)
		}
	}

	// Default to mayor
	return "mayor/"
}

func runMailCheck(cmd *cobra.Command, args []string) error {
	// Find workspace
	workDir, err := findBeadsWorkDir()
	if err != nil {
		if mailCheckInject {
			// Inject mode: always exit 0, silent on error
			return nil
		}
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	// Determine which inbox
	address := detectSender()

	// Get mailbox
	router := mail.NewRouter(workDir)
	mailbox, err := router.GetMailbox(address)
	if err != nil {
		if mailCheckInject {
			return nil
		}
		return fmt.Errorf("getting mailbox: %w", err)
	}

	// Count unread
	_, unread, err := mailbox.Count()
	if err != nil {
		if mailCheckInject {
			return nil
		}
		return fmt.Errorf("counting messages: %w", err)
	}

	// JSON output
	if mailCheckJSON {
		result := map[string]interface{}{
			"address": address,
			"unread":  unread,
			"has_new": unread > 0,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	// Inject mode: output system-reminder if mail exists
	if mailCheckInject {
		if unread > 0 {
			// Get subjects for context
			messages, _ := mailbox.ListUnread()
			var subjects []string
			for _, msg := range messages {
				subjects = append(subjects, fmt.Sprintf("- From %s: %s", msg.From, msg.Subject))
			}

			fmt.Println("<system-reminder>")
			fmt.Printf("You have %d unread message(s) in your inbox.\n\n", unread)
			for _, s := range subjects {
				fmt.Println(s)
			}
			fmt.Println()
			fmt.Println("Run 'gt mail inbox' to see your messages, or 'gt mail read <id>' for a specific message.")
			fmt.Println("</system-reminder>")
		}
		return nil
	}

	// Normal mode
	if unread > 0 {
		fmt.Printf("%s %d unread message(s)\n", style.Bold.Render("📬"), unread)
		os.Exit(0)
	} else {
		fmt.Println("No new mail")
		os.Exit(1)
	}
	return nil
}

func runMailThread(cmd *cobra.Command, args []string) error {
	threadID := args[0]

	// Find workspace
	workDir, err := findBeadsWorkDir()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	// Determine which inbox
	address := detectSender()

	// Get mailbox and thread messages
	router := mail.NewRouter(workDir)
	mailbox, err := router.GetMailbox(address)
	if err != nil {
		return fmt.Errorf("getting mailbox: %w", err)
	}

	messages, err := mailbox.ListByThread(threadID)
	if err != nil {
		return fmt.Errorf("getting thread: %w", err)
	}

	// JSON output
	if mailThreadJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(messages)
	}

	// Human-readable output
	fmt.Printf("%s Thread: %s (%d messages)\n\n",
		style.Bold.Render("🧵"), threadID, len(messages))

	if len(messages) == 0 {
		fmt.Printf("  %s\n", style.Dim.Render("(no messages in thread)"))
		return nil
	}

	for i, msg := range messages {
		typeMarker := ""
		if msg.Type != "" && msg.Type != mail.TypeNotification {
			typeMarker = fmt.Sprintf(" [%s]", msg.Type)
		}
		priorityMarker := ""
		if msg.Priority == mail.PriorityHigh || msg.Priority == mail.PriorityUrgent {
			priorityMarker = " " + style.Bold.Render("!")
		}

		if i > 0 {
			fmt.Printf("  %s\n", style.Dim.Render("│"))
		}
		fmt.Printf("  %s %s%s%s\n", style.Bold.Render("●"), msg.Subject, typeMarker, priorityMarker)
		fmt.Printf("    %s from %s to %s\n",
			style.Dim.Render(msg.ID),
			msg.From, msg.To)
		fmt.Printf("    %s\n",
			style.Dim.Render(msg.Timestamp.Format("2006-01-02 15:04")))

		if msg.Body != "" {
			fmt.Printf("    %s\n", msg.Body)
		}
	}

	return nil
}

// generateThreadID creates a random thread ID for new message threads.
func generateThreadID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return "thread-" + hex.EncodeToString(b)
}
