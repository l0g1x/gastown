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
	if flags.Lookup("now") == nil {
		t.Error("--now flag not registered")
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
	if flags.ShorthandLookup("n") == nil {
		t.Error("-n short flag not registered")
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
	if restartNow != false {
		t.Error("restartNow should default to false")
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

func TestRestartOptions_FromFlags(t *testing.T) {
	// Save original values
	savedQuiet := restartQuiet
	savedWait := restartWait
	savedNow := restartNow
	savedInfra := restartInfra

	// Restore after test
	defer func() {
		restartQuiet = savedQuiet
		restartWait = savedWait
		restartNow = savedNow
		restartInfra = savedInfra
	}()

	// Set test values
	restartQuiet = true
	restartWait = true
	restartNow = false
	restartInfra = true

	opts := restartOptionsFromFlags()

	if opts.Quiet != true {
		t.Error("Quiet should be true")
	}
	if opts.Wait != true {
		t.Error("Wait should be true")
	}
	if opts.Now != false {
		t.Error("Now should be false")
	}
	if opts.Infra != true {
		t.Error("Infra should be true")
	}
}

func TestRestartOptions_ZeroValue(t *testing.T) {
	// Test that zero-value RestartOptions has sensible defaults
	opts := RestartOptions{}

	if opts.Quiet != false {
		t.Error("zero-value Quiet should be false")
	}
	if opts.Wait != false {
		t.Error("zero-value Wait should be false")
	}
	if opts.Now != false {
		t.Error("zero-value Now should be false")
	}
	if opts.Infra != false {
		t.Error("zero-value Infra should be false")
	}
}

func TestRestartOptions_DefaultStopsPolecats(t *testing.T) {
	// Default restart (Infra=false) should stop polecats
	opts := RestartOptions{Infra: false}

	// Verify this translates to DownOptions with Polecats=true
	shouldStopPolecats := !opts.Infra
	if !shouldStopPolecats {
		t.Error("default restart should stop polecats")
	}
}

func TestRestartOptions_InfraSkipsPolecats(t *testing.T) {
	// --infra restart should NOT stop polecats
	opts := RestartOptions{Infra: true}

	shouldStopPolecats := !opts.Infra
	if shouldStopPolecats {
		t.Error("--infra restart should NOT stop polecats")
	}
}

func TestRestartOptions_DefaultRestoresPolecats(t *testing.T) {
	// Default restart (Infra=false) should restore polecats
	opts := RestartOptions{Infra: false}

	shouldRestore := !opts.Infra
	if !shouldRestore {
		t.Error("default restart should restore polecats")
	}
}

func TestRestartOptions_InfraSkipsRestore(t *testing.T) {
	// --infra restart should NOT restore polecats
	opts := RestartOptions{Infra: true}

	shouldRestore := !opts.Infra
	if shouldRestore {
		t.Error("--infra restart should NOT restore polecats")
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
