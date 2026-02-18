package cmd

import (
	"strings"
	"testing"
)

func TestEnsureKnownConvoyStatus(t *testing.T) {
	t.Parallel()

	if err := ensureKnownConvoyStatus("open"); err != nil {
		t.Fatalf("expected open to be accepted: %v", err)
	}
	if err := ensureKnownConvoyStatus(" closed "); err != nil {
		t.Fatalf("expected closed to be accepted: %v", err)
	}
	if err := ensureKnownConvoyStatus("staged:ready"); err != nil {
		t.Fatalf("expected staged:ready to be accepted: %v", err)
	}
	if err := ensureKnownConvoyStatus("staged:warnings"); err != nil {
		t.Fatalf("expected staged:warnings to be accepted: %v", err)
	}
	if err := ensureKnownConvoyStatus("in_progress"); err == nil {
		t.Fatal("expected unknown status to be rejected")
	}
}

func TestValidateConvoyStatusTransition(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		current string
		target  string
		wantErr bool
	}{
		// Original transitions
		{name: "open to closed", current: "open", target: "closed", wantErr: false},
		{name: "closed to open", current: "closed", target: "open", wantErr: false},
		{name: "same open", current: "open", target: "open", wantErr: false},
		{name: "same closed", current: "closed", target: "closed", wantErr: false},
		{name: "unknown current", current: "in_progress", target: "closed", wantErr: true},
		{name: "unknown target", current: "open", target: "archived", wantErr: true},

		// Staged transitions (US-005)
		{name: "staged:ready to open (launch)", current: "staged:ready", target: "open", wantErr: false},
		{name: "staged:warnings to open (launch --force)", current: "staged:warnings", target: "open", wantErr: false},
		{name: "staged:ready to staged:warnings", current: "staged:ready", target: "staged:warnings", wantErr: false},
		{name: "staged:warnings to staged:ready", current: "staged:warnings", target: "staged:ready", wantErr: false},
		{name: "staged:ready to closed (abandon)", current: "staged:ready", target: "closed", wantErr: false},
		{name: "staged:warnings to closed (abandon)", current: "staged:warnings", target: "closed", wantErr: false},
		{name: "same staged:ready", current: "staged:ready", target: "staged:ready", wantErr: false},
		{name: "same staged:warnings", current: "staged:warnings", target: "staged:warnings", wantErr: false},

		// Invalid: cannot stage after launch/close
		{name: "open to staged:ready (invalid)", current: "open", target: "staged:ready", wantErr: true},
		{name: "open to staged:warnings (invalid)", current: "open", target: "staged:warnings", wantErr: true},
		{name: "closed to staged:ready (invalid)", current: "closed", target: "staged:ready", wantErr: true},
		{name: "closed to staged:warnings (invalid)", current: "closed", target: "staged:warnings", wantErr: true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateConvoyStatusTransition(tc.current, tc.target)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for transition %q -> %q", tc.current, tc.target)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected transition %q -> %q to pass, got %v", tc.current, tc.target, err)
			}
		})
	}
}

// TestEnsureKnownConvoyStatus_StagedPassesGuardPaths verifies that staged
// statuses pass the ensureKnownConvoyStatus guard used in convoy commands
// (close, add, status, check). This exercises the C2 concern: if a convoy
// has status "staged:ready" or "staged:warnings", command paths that call
// ensureKnownConvoyStatus must not reject it.
func TestEnsureKnownConvoyStatus_StagedPassesGuardPaths(t *testing.T) {
	t.Parallel()

	for _, status := range []string{"staged:ready", "staged:warnings"} {
		if err := ensureKnownConvoyStatus(status); err != nil {
			t.Errorf("ensureKnownConvoyStatus(%q) = %v, want nil (must pass command guards)", status, err)
		}
	}
}

// TestEnsureKnownConvoyStatus_ErrorMessageIsHelpful verifies that the error
// message from ensureKnownConvoyStatus lists all known statuses so the user
// knows what values are valid.
func TestEnsureKnownConvoyStatus_ErrorMessageIsHelpful(t *testing.T) {
	t.Parallel()

	err := ensureKnownConvoyStatus("bogus")
	if err == nil {
		t.Fatal("expected error for unknown status")
	}
	msg := err.Error()
	for _, expected := range []string{"open", "closed", "staged:ready", "staged:warnings"} {
		if !strings.Contains(msg, expected) {
			t.Errorf("error message should mention %q, got: %s", expected, msg)
		}
	}
}
