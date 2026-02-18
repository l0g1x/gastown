package convoy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	beadsdk "github.com/steveyegge/beads"
)

// setupTestStore opens a real beads database in a temp dir for integration tests.
// Skips the test if the store cannot be opened (e.g. no CGO, no Dolt).
// Caller must run the returned cleanup when done.
func setupTestStore(t *testing.T) (beadsdk.Storage, func()) {
	t.Helper()

	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	doltPath := filepath.Join(beadsDir, "dolt")
	if err := os.MkdirAll(doltPath, 0755); err != nil {
		t.Skipf("cannot create test dir: %v", err)
	}

	ctx := context.Background()
	store, err := beadsdk.Open(ctx, doltPath)
	if err != nil {
		t.Skipf("beads store unavailable (CGO/Dolt required): %v", err)
	}

	if err := store.SetConfig(ctx, "issue_prefix", "test"); err != nil {
		_ = store.Close()
		t.Skipf("SetConfig issue_prefix: %v", err)
	}

	cleanup := func() {
		_ = store.Close()
	}
	return store, cleanup
}

func TestSetupTestStore_OpensStore(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	if store == nil {
		t.Fatal("setupTestStore returned nil store")
	}
}

func TestGetTrackingConvoys_FiltersByTracksType(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	// Create convoy and tracked issue
	convoyIssue := &beadsdk.Issue{
		ID:        "hq-cv-test1",
		Title:     "Test Convoy",
		Status:    beadsdk.StatusOpen,
		Priority:  2,
		IssueType: beadsdk.TypeTask,
		CreatedAt: now,
		UpdatedAt: now,
	}
	trackedIssue := &beadsdk.Issue{
		ID:        "gt-tracked1",
		Title:     "Tracked",
		Status:    beadsdk.StatusOpen,
		Priority:  2,
		IssueType: beadsdk.TypeTask,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := store.CreateIssue(ctx, convoyIssue, "test"); err != nil {
		t.Fatalf("CreateIssue convoy: %v", err)
	}
	if err := store.CreateIssue(ctx, trackedIssue, "test"); err != nil {
		t.Fatalf("CreateIssue tracked: %v", err)
	}

	// Add tracks dependency: convoy tracks issue (convoy depends on issue with type tracks)
	dep := &beadsdk.Dependency{
		IssueID:     convoyIssue.ID,
		DependsOnID: trackedIssue.ID,
		Type:        beadsdk.DependencyType("tracks"),
		CreatedAt:   now,
		CreatedBy:   "test",
	}
	if err := store.AddDependency(ctx, dep, "test"); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}

	// Add blocks dependency (should be filtered out)
	otherIssue := &beadsdk.Issue{
		ID:        "gt-other",
		Title:     "Other",
		Status:    beadsdk.StatusOpen,
		Priority:  2,
		IssueType: beadsdk.TypeTask,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateIssue(ctx, otherIssue, "test"); err != nil {
		t.Fatalf("CreateIssue other: %v", err)
	}
	blocksDep := &beadsdk.Dependency{
		IssueID:     "hq-cv-other",
		DependsOnID: trackedIssue.ID,
		Type:        beadsdk.DepBlocks,
		CreatedAt:   now,
		CreatedBy:   "test",
	}
	if err := store.CreateIssue(ctx, &beadsdk.Issue{
		ID:        "hq-cv-other",
		Title:     "Other Convoy",
		Status:    beadsdk.StatusOpen,
		Priority:  2,
		IssueType: beadsdk.TypeTask,
		CreatedAt: now,
		UpdatedAt: now,
	}, "test"); err != nil {
		t.Fatalf("CreateIssue other convoy: %v", err)
	}
	if err := store.AddDependency(ctx, blocksDep, "test"); err != nil {
		t.Fatalf("AddDependency blocks: %v", err)
	}

	// getTrackingConvoys(trackedIssue.ID) should return only hq-cv-test1 (tracks), not hq-cv-other (blocks)
	convoyIDs := getTrackingConvoys(ctx, store, trackedIssue.ID)
	if len(convoyIDs) != 1 || convoyIDs[0] != convoyIssue.ID {
		t.Errorf("getTrackingConvoys = %v, want [%s]", convoyIDs, convoyIssue.ID)
	}
}

func TestIsIssueBlocked_BlocksDepOpen(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	blocker := &beadsdk.Issue{
		ID: "test-blocker", Title: "Blocker", Status: beadsdk.StatusOpen,
		Priority: 2, IssueType: beadsdk.TypeTask, CreatedAt: now, UpdatedAt: now,
	}
	blocked := &beadsdk.Issue{
		ID: "test-blocked", Title: "Blocked", Status: beadsdk.StatusOpen,
		Priority: 2, IssueType: beadsdk.TypeTask, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateIssue(ctx, blocker, "test"); err != nil {
		t.Fatalf("CreateIssue blocker: %v", err)
	}
	if err := store.CreateIssue(ctx, blocked, "test"); err != nil {
		t.Fatalf("CreateIssue blocked: %v", err)
	}

	// blocked depends on blocker with type "blocks" → blocker blocks blocked
	dep := &beadsdk.Dependency{
		IssueID: blocked.ID, DependsOnID: blocker.ID,
		Type: beadsdk.DepBlocks, CreatedAt: now, CreatedBy: "test",
	}
	if err := store.AddDependency(ctx, dep, "test"); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}

	if !isIssueBlocked(ctx, store, blocked.ID) {
		t.Error("isIssueBlocked = false, want true (blocker is open)")
	}
}

func TestIsIssueBlocked_BlocksDepClosed(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	blocker := &beadsdk.Issue{
		ID: "test-blocker2", Title: "Blocker", Status: beadsdk.StatusOpen,
		Priority: 2, IssueType: beadsdk.TypeTask, CreatedAt: now, UpdatedAt: now,
	}
	blocked := &beadsdk.Issue{
		ID: "test-blocked2", Title: "Blocked", Status: beadsdk.StatusOpen,
		Priority: 2, IssueType: beadsdk.TypeTask, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateIssue(ctx, blocker, "test"); err != nil {
		t.Fatalf("CreateIssue blocker: %v", err)
	}
	if err := store.CreateIssue(ctx, blocked, "test"); err != nil {
		t.Fatalf("CreateIssue blocked: %v", err)
	}

	dep := &beadsdk.Dependency{
		IssueID: blocked.ID, DependsOnID: blocker.ID,
		Type: beadsdk.DepBlocks, CreatedAt: now, CreatedBy: "test",
	}
	if err := store.AddDependency(ctx, dep, "test"); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}

	// Close the blocker
	if err := store.CloseIssue(ctx, blocker.ID, "done", "test", ""); err != nil {
		t.Fatalf("CloseIssue: %v", err)
	}

	if isIssueBlocked(ctx, store, blocked.ID) {
		t.Error("isIssueBlocked = true, want false (blocker is closed)")
	}
}

func TestIsIssueBlocked_ParentChildNotBlocking(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	epic := &beadsdk.Issue{
		ID: "test-epic", Title: "Epic", Status: beadsdk.StatusOpen,
		Priority: 2, IssueType: beadsdk.TypeTask, CreatedAt: now, UpdatedAt: now,
	}
	child := &beadsdk.Issue{
		ID: "test-child", Title: "Child Task", Status: beadsdk.StatusOpen,
		Priority: 2, IssueType: beadsdk.TypeTask, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateIssue(ctx, epic, "test"); err != nil {
		t.Fatalf("CreateIssue epic: %v", err)
	}
	if err := store.CreateIssue(ctx, child, "test"); err != nil {
		t.Fatalf("CreateIssue child: %v", err)
	}

	// child depends on epic with type "parent-child" (child → parent)
	dep := &beadsdk.Dependency{
		IssueID: child.ID, DependsOnID: epic.ID,
		Type: beadsdk.DepParentChild, CreatedAt: now, CreatedBy: "test",
	}
	if err := store.AddDependency(ctx, dep, "test"); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}

	// parent-child should NOT be treated as blocking
	if isIssueBlocked(ctx, store, child.ID) {
		t.Error("isIssueBlocked = true, want false (parent-child is not blocking)")
	}
}

func TestIsIssueBlocked_NoDeps(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	issue := &beadsdk.Issue{
		ID: "test-nodeps", Title: "No Deps", Status: beadsdk.StatusOpen,
		Priority: 2, IssueType: beadsdk.TypeTask, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateIssue(ctx, issue, "test"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	if isIssueBlocked(ctx, store, issue.ID) {
		t.Error("isIssueBlocked = true, want false (no dependencies)")
	}
}

// --- US-004 / P0: IssueType population and type filtering ---

func TestGetConvoyTrackedIssues_PopulatesIssueType(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	// Create convoy
	convoy := &beadsdk.Issue{
		ID: "hq-cv-type1", Title: "Type Test Convoy", Status: beadsdk.StatusOpen,
		Priority: 2, IssueType: beadsdk.IssueType("convoy"), CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateIssue(ctx, convoy, "test"); err != nil {
		t.Fatalf("CreateIssue convoy: %v", err)
	}

	// Create an epic and a task, both tracked by the convoy
	epic := &beadsdk.Issue{
		ID: "test-epic1", Title: "Parent Epic", Status: beadsdk.StatusOpen,
		Priority: 1, IssueType: beadsdk.TypeEpic, CreatedAt: now, UpdatedAt: now,
	}
	task := &beadsdk.Issue{
		ID: "test-task1", Title: "Leaf Task", Status: beadsdk.StatusOpen,
		Priority: 2, IssueType: beadsdk.TypeTask, CreatedAt: now, UpdatedAt: now,
	}
	for _, iss := range []*beadsdk.Issue{epic, task} {
		if err := store.CreateIssue(ctx, iss, "test"); err != nil {
			t.Fatalf("CreateIssue %s: %v", iss.ID, err)
		}
	}

	// convoy tracks both issues
	for _, targetID := range []string{epic.ID, task.ID} {
		dep := &beadsdk.Dependency{
			IssueID: convoy.ID, DependsOnID: targetID,
			Type: beadsdk.DependencyType("tracks"), CreatedAt: now, CreatedBy: "test",
		}
		if err := store.AddDependency(ctx, dep, "test"); err != nil {
			t.Fatalf("AddDependency %s: %v", targetID, err)
		}
	}

	tracked := getConvoyTrackedIssues(ctx, store, convoy.ID)
	if len(tracked) != 2 {
		t.Fatalf("expected 2 tracked issues, got %d: %+v", len(tracked), tracked)
	}

	byID := map[string]trackedIssue{}
	for _, ti := range tracked {
		byID[ti.ID] = ti
	}

	// Verify IssueType is populated from the store
	epicTracked, ok := byID[epic.ID]
	if !ok {
		t.Fatal("epic not found in tracked issues")
	}
	if epicTracked.IssueType != "epic" {
		t.Errorf("epic IssueType = %q, want %q", epicTracked.IssueType, "epic")
	}

	taskTracked, ok := byID[task.ID]
	if !ok {
		t.Fatal("task not found in tracked issues")
	}
	if taskTracked.IssueType != "task" {
		t.Errorf("task IssueType = %q, want %q", taskTracked.IssueType, "task")
	}

	// Verify IsSlingableType correctly filters: epic excluded, task included
	if IsSlingableType(epicTracked.IssueType) {
		t.Error("epic should NOT be slingable")
	}
	if !IsSlingableType(taskTracked.IssueType) {
		t.Error("task should be slingable")
	}
}

func TestGetConvoyTrackedIssues_EpicSkippedByFeedFilter(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	// Create convoy tracking one epic and one task
	convoy := &beadsdk.Issue{
		ID: "hq-cv-feed1", Title: "Feed Filter Convoy", Status: beadsdk.StatusOpen,
		Priority: 2, IssueType: beadsdk.IssueType("convoy"), CreatedAt: now, UpdatedAt: now,
	}
	epic := &beadsdk.Issue{
		ID: "test-epic2", Title: "Non-Slingable Epic", Status: beadsdk.StatusOpen,
		Priority: 1, IssueType: beadsdk.TypeEpic, CreatedAt: now, UpdatedAt: now,
	}
	task := &beadsdk.Issue{
		ID: "test-task2", Title: "Slingable Task", Status: beadsdk.StatusOpen,
		Priority: 2, IssueType: beadsdk.TypeTask, CreatedAt: now, UpdatedAt: now,
	}
	for _, iss := range []*beadsdk.Issue{convoy, epic, task} {
		if err := store.CreateIssue(ctx, iss, "test"); err != nil {
			t.Fatalf("CreateIssue %s: %v", iss.ID, err)
		}
	}
	for _, targetID := range []string{epic.ID, task.ID} {
		dep := &beadsdk.Dependency{
			IssueID: convoy.ID, DependsOnID: targetID,
			Type: beadsdk.DependencyType("tracks"), CreatedAt: now, CreatedBy: "test",
		}
		if err := store.AddDependency(ctx, dep, "test"); err != nil {
			t.Fatalf("AddDependency %s: %v", targetID, err)
		}
	}

	// Simulate what feedNextReadyIssue does: get tracked issues, then iterate
	// with the same filter chain. We can't call feedNextReadyIssue directly
	// (it shells out to gt sling), but we can verify the filter logic.
	tracked := getConvoyTrackedIssues(ctx, store, convoy.ID)

	var slingable []trackedIssue
	for _, issue := range tracked {
		if issue.Status != "open" || issue.Assignee != "" {
			continue
		}
		if !IsSlingableType(issue.IssueType) {
			continue // This is the filter under test
		}
		if isIssueBlocked(ctx, store, issue.ID) {
			continue
		}
		slingable = append(slingable, issue)
	}

	if len(slingable) != 1 {
		t.Fatalf("expected 1 slingable issue, got %d: %+v", len(slingable), slingable)
	}
	if slingable[0].ID != task.ID {
		t.Errorf("slingable issue = %q, want %q", slingable[0].ID, task.ID)
	}
}

func TestIsConvoyClosed_ReturnsCorrectStatus(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	openIssue := &beadsdk.Issue{
		ID:        "hq-cv-open",
		Title:     "Open",
		Status:    beadsdk.StatusOpen,
		Priority:  2,
		IssueType: beadsdk.TypeTask,
		CreatedAt: now,
		UpdatedAt: now,
	}
	closedIssue := &beadsdk.Issue{
		ID:        "hq-cv-closed",
		Title:     "Closed",
		Status:    beadsdk.StatusClosed,
		Priority:  2,
		IssueType: beadsdk.TypeTask,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := store.CreateIssue(ctx, openIssue, "test"); err != nil {
		t.Fatalf("CreateIssue open: %v", err)
	}
	if err := store.CreateIssue(ctx, closedIssue, "test"); err != nil {
		t.Fatalf("CreateIssue closed: %v", err)
	}

	if isConvoyClosed(ctx, store, openIssue.ID) {
		t.Error("isConvoyClosed(open) = true, want false")
	}
	if !isConvoyClosed(ctx, store, closedIssue.ID) {
		t.Error("isConvoyClosed(closed) = false, want true")
	}
}
