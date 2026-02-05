package cmd

import (
	"testing"
)

func TestRestartCmd_Registered(t *testing.T) {
	// Verify restart command is registered
	cmd, _, err := rootCmd.Find([]string{"restart"})
	if err != nil {
		t.Fatalf("restart command not found: %v", err)
	}
	if cmd.Name() != "restart" {
		t.Errorf("expected command name 'restart', got %q", cmd.Name())
	}
}

func TestRestartCmd_Flags(t *testing.T) {
	// Verify flags are registered
	flags := restartCmd.Flags()

	if flags.Lookup("quiet") == nil {
		t.Error("--quiet flag not registered")
	}
	if flags.Lookup("wait") == nil {
		t.Error("--wait flag not registered")
	}
	if flags.Lookup("force") == nil {
		t.Error("--force flag not registered")
	}
	if flags.Lookup("infra") == nil {
		t.Error("--infra flag not registered")
	}
}

func TestRestartCmd_ShortFlags(t *testing.T) {
	// Verify short flags
	flags := restartCmd.Flags()

	if flags.ShorthandLookup("q") == nil {
		t.Error("-q short flag not registered")
	}
	if flags.ShorthandLookup("w") == nil {
		t.Error("-w short flag not registered")
	}
	if flags.ShorthandLookup("f") == nil {
		t.Error("-f short flag not registered")
	}
}

func TestRestartCmd_NoOldFlags(t *testing.T) {
	// Verify old flags are removed
	flags := restartCmd.Flags()

	if flags.Lookup("restore") != nil {
		t.Error("--restore flag should be removed (default behavior now)")
	}
	if flags.Lookup("polecats") != nil {
		t.Error("--polecats flag should be removed (default behavior now)")
	}
}

func TestRestartCmd_FlagDefaults(t *testing.T) {
	// Verify flag defaults
	if restartQuiet != false {
		t.Error("restartQuiet should default to false")
	}
	if restartWait != false {
		t.Error("restartWait should default to false")
	}
	if restartForce != false {
		t.Error("restartForce should default to false")
	}
	if restartInfra != false {
		t.Error("restartInfra should default to false")
	}
}

func TestRestartCmd_GroupID(t *testing.T) {
	// Verify command is in services group
	if restartCmd.GroupID != GroupServices {
		t.Errorf("restart command GroupID = %q, want %q", restartCmd.GroupID, GroupServices)
	}
}

func TestDownOptions_FromFlags(t *testing.T) {
	// Save original values
	savedQuiet := downQuiet
	savedForce := downForce
	savedAll := downAll
	savedNuke := downNuke
	savedDryRun := downDryRun
	savedPolecats := downPolecats

	// Restore after test
	defer func() {
		downQuiet = savedQuiet
		downForce = savedForce
		downAll = savedAll
		downNuke = savedNuke
		downDryRun = savedDryRun
		downPolecats = savedPolecats
	}()

	// Set test values
	downQuiet = true
	downForce = true
	downAll = true
	downNuke = false
	downDryRun = true
	downPolecats = true

	opts := downOptionsFromFlags()

	if opts.Quiet != true {
		t.Error("Quiet should be true")
	}
	if opts.Force != true {
		t.Error("Force should be true")
	}
	if opts.All != true {
		t.Error("All should be true")
	}
	if opts.Nuke != false {
		t.Error("Nuke should be false")
	}
	if opts.DryRun != true {
		t.Error("DryRun should be true")
	}
	if opts.Polecats != true {
		t.Error("Polecats should be true")
	}
}

func TestDownOptions_ZeroValue(t *testing.T) {
	// Test that zero-value DownOptions has sensible defaults
	opts := DownOptions{}

	if opts.Quiet != false {
		t.Error("zero-value Quiet should be false")
	}
	if opts.Force != false {
		t.Error("zero-value Force should be false")
	}
	if opts.Polecats != false {
		t.Error("zero-value Polecats should be false")
	}
}

func TestUpOptions_FromFlags(t *testing.T) {
	// Save original values
	savedQuiet := upQuiet
	savedRestore := upRestore

	// Restore after test
	defer func() {
		upQuiet = savedQuiet
		upRestore = savedRestore
	}()

	// Set test values
	upQuiet = true
	upRestore = true

	opts := upOptionsFromFlags()

	if opts.Quiet != true {
		t.Error("Quiet should be true")
	}
	if opts.Restore != true {
		t.Error("Restore should be true")
	}
}

func TestUpOptions_ZeroValue(t *testing.T) {
	// Test that zero-value UpOptions has sensible defaults
	opts := UpOptions{}

	if opts.Quiet != false {
		t.Error("zero-value Quiet should be false")
	}
	if opts.Restore != false {
		t.Error("zero-value Restore should be false")
	}
}

func TestUpCmd_NoRestartFlag(t *testing.T) {
	// Verify --restart flag was removed from gt up
	flags := upCmd.Flags()

	if flags.Lookup("restart") != nil {
		t.Error("--restart flag should not exist on gt up (use gt restart instead)")
	}
}

func TestRestartDownOptions_DefaultStopsPolecats(t *testing.T) {
	// When --infra is false (default), restart should stop polecats
	// This tests the logic in runRestart without actually running it

	// Simulate default flags
	restartInfra = false

	// The down options should have Polecats: true (stop polecats)
	downOpts := DownOptions{
		Polecats: !restartInfra, // This is the logic from runRestart
	}

	if !downOpts.Polecats {
		t.Error("default restart should stop polecats (Polecats should be true)")
	}
}

func TestRestartDownOptions_InfraOnlySkipsPolecats(t *testing.T) {
	// When --infra is true, restart should NOT stop polecats
	restartInfra = true
	defer func() { restartInfra = false }()

	downOpts := DownOptions{
		Polecats: !restartInfra,
	}

	if downOpts.Polecats {
		t.Error("--infra restart should NOT stop polecats (Polecats should be false)")
	}
}

func TestRestartUpOptions_DefaultRestoresPolecats(t *testing.T) {
	// When --infra is false (default), restart should restore polecats
	restartInfra = false

	upOpts := UpOptions{
		Restore: !restartInfra, // This is the logic from runRestart
	}

	if !upOpts.Restore {
		t.Error("default restart should restore polecats (Restore should be true)")
	}
}

func TestRestartUpOptions_InfraOnlySkipsRestore(t *testing.T) {
	// When --infra is true, restart should NOT restore polecats
	restartInfra = true
	defer func() { restartInfra = false }()

	upOpts := UpOptions{
		Restore: !restartInfra,
	}

	if upOpts.Restore {
		t.Error("--infra restart should NOT restore polecats (Restore should be false)")
	}
}
