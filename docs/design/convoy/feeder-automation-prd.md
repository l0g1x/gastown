[PRD]
# PRD: Convoy Feeder Redesign & Stage/Launch Commands

## Overview

The convoy feeder has structural gaps that prevent reliable automated dispatch of
convoy-tracked work. Phase 1 fixes the feeder to properly iterate ready issues,
respect blocking dependencies, and honor rig capacity. Phase 2 introduces
`gt convoy stage` and `gt convoy launch` commands that analyze a pre-built bead
DAG, compute a wave-based dispatch plan, and automate convoy lifecycle from
staging through completion — including epic status management and integration
branch awareness.

A pluggable `FeederStrategy` interface is introduced in Phase 2 to allow future
dispatch strategies (e.g., AI-coordinated dispatch via a coordinator polecat
formula). Phase 1 fixes operate on the existing feeder code without the strategy
abstraction.

## Goals

- Fix the feeder so it dispatches work reliably: iterate all ready issues, skip
  parked/unslingable/blocked beads, and try the next eligible issue instead of
  giving up after the first failure.
- Enforce `blocks` dependency ordering in both feed paths (event-driven and
  stranded scan).
- Respect `max_polecats` rig capacity to avoid overloading rigs.
- Provide `gt convoy stage` for pre-launch DAG analysis with a route plan
  showing the dispatch order as "waves."
- Provide `gt convoy launch` as a separate opt-in command to activate a
  previously staged convoy.
- Manage epic/sub-epic status transitions automatically (open -> in_progress ->
  closed) based on child task lifecycle.
- Enforce max epic nesting depth of 2 (root epic + sub-epics) with actionable
  restructuring suggestions on violation.
- Integrate with the existing integration branch infrastructure — warn when an
  epic lacks an integration branch, but defer branch creation/landing to existing
  mechanisms.
- Define a `FeederStrategy` interface for future extensibility (coordinator
  polecat strategy, etc.).

## Quality Gates

These commands must pass for every user story:

- `SKIP_UPDATE_CHECK=1 make install` — Build
- `go test ./...` — Full test suite including integration tests

## Phases

| Phase | Scope | PR |
|-------|-------|----|
| **Phase 1** | Feeder iteration fix, blocks enforcement, capacity limits, slingability filtering | Current PR (`feat/convoy-manager-rewrite`) |
| **Phase 2** | `gt convoy stage`, `gt convoy launch`, wave computation, route plan, epic status management, integration branch awareness, FeederStrategy interface | Next PR |
| **Phase 3** (future) | Auto-formula-detection in `gt sling` (epic -> coordinator formula), AI-coordinated dispatch strategy | Future |

---

## Phase 1: Feeder Redesign

### US-001: Feeder iterates all ready issues

**Description:** As a convoy manager, I want the feeder to try all ready issues
in a convoy instead of giving up after the first one fails or is skipped, so
that convoys progress even when some issues target parked or unavailable rigs.

**Acceptance Criteria:**

- [ ] `feedFirstReady` (stranded scan path) iterates through ALL `ReadyIssues`
  instead of only trying `ReadyIssues[0]`. When the first issue's rig is parked,
  unresolvable, or sling fails, it continues to the next issue.
- [ ] `feedNextReadyIssue` (event-driven path) already iterates but returns
  after feeding one — verify it correctly skips all non-dispatchable issues
  (parked rig, no rig resolution) and continues to the next.
- [ ] Logging: each skip reason is logged (parked, no rig, sling failure) so
  the operator can see why issues were skipped.
- [ ] Unit tests: convoy with 3 ready issues where issue 1 targets a parked rig,
  issue 2 has no rig resolution, and issue 3 is dispatchable — verify issue 3
  gets slung.

**Key files:**
- `internal/daemon/convoy_manager.go` — `feedFirstReady`
- `internal/convoy/operations.go` — `feedNextReadyIssue`

---

### US-002: Feeder respects blocks dependencies

**Description:** As a convoy manager, I want the feeder to check blocking
dependencies before dispatching an issue, so that issues are only slung when
their blockers have been resolved.

**Acceptance Criteria:**

- [ ] Event-driven path (`feedNextReadyIssue`): before dispatching, query the
  issue's dependencies via `store.GetDependenciesWithMetadata` and check for
  `blocks` or `conditional-blocks` type deps where the blocking issue is not
  closed. Skip the issue if blocked.
- [ ] Stranded scan path (`feedFirstReady`): the upstream `isReadyIssue`
  already checks `t.Blocked` — verify this field is populated correctly from
  cross-rig dependency data. If it uses stale hq metadata (known issue from
  Discovery #11), document the limitation.
- [ ] `parent-child` dependencies are NOT treated as blocking for dispatch
  purposes (consistent with molecule step behavior). A child task is
  dispatchable even if its parent epic is still open.
- [ ] Unit tests: task A `blocks` task B — verify B is not dispatched while A is
  open, and B becomes dispatchable after A closes.
- [ ] Unit tests: task with `parent-child` dep on an open epic — verify the task
  IS dispatchable.

**Key files:**
- `internal/convoy/operations.go` — `feedNextReadyIssue` (add blocks check)
- `internal/convoy/operations_test.go`

---

### US-003: Feeder respects max_polecats rig capacity

**Description:** As a convoy manager, I want the feeder to check how many
polecats are active on a rig before dispatching more work, so that rigs are not
overloaded beyond their configured capacity.

**Acceptance Criteria:**

- [ ] Before dispatching to a rig, read `max_polecats` from the rig property
  layer (default: 10). Count active polecats on that rig (via tmux session
  enumeration or polecat manager API). If active >= max, skip that issue and
  try the next.
- [ ] The capacity check applies to both feed paths (event-driven and stranded
  scan).
- [ ] If ALL ready issues target rigs at capacity, the feeder logs this and
  waits for the next cycle rather than force-dispatching.
- [ ] Unit test: rig at capacity (3/3 polecats) — verify feeder skips issues
  targeting that rig and dispatches to a different rig with capacity.

**Key files:**
- `internal/daemon/convoy_manager.go` — capacity check before dispatch
- `internal/rig/config.go` — `max_polecats` property layer access

**Note:** `max_polecats` is defined in the rig property layer (system default 10)
but is not currently enforced anywhere in the spawn path. This story adds the
first enforcement point. The capacity check reads from the same property layer
used by `gt rig config show`.

---

### US-004: Feeder filters non-slingable bead types

**Description:** As a convoy manager, I want the feeder to skip beads that
should not be slung to polecats (epics, convoys, town-level beads), so that
only actionable task beads are dispatched.

**Acceptance Criteria:**

- [ ] Both feed paths filter out beads where `isSlingableBead` returns false.
  The existing `isSlingableBead` already filters town-level beads (hq-* with
  `path="."`).
- [ ] Extend `isSlingableBead` (or add a parallel check) to filter by
  `issue_type`: beads of type `epic`, `convoy`, `gate`, `event`, `agent`,
  `role`, `rig`, `message`, `slot` are not slingable.
- [ ] Slingable types: `task`, `bug`, `feature`, `chore`, `molecule`,
  `merge-request` (and any unknown/custom type — default to slingable).
- [ ] The event-driven path (`feedNextReadyIssue`) needs to fetch issue type
  from the store to perform this check. Add `IssueType` to the `trackedIssue`
  struct.
- [ ] Unit tests: convoy tracking a mix of task + epic + convoy beads — verify
  only tasks are dispatched.

**Key files:**
- `internal/cmd/convoy.go` — `isSlingableBead` (extend)
- `internal/convoy/operations.go` — `feedNextReadyIssue`, `trackedIssue` struct
- `internal/cmd/convoy_stranded_test.go` — existing `TestIsSlingableBead`

---

### US-005: Register custom convoy statuses

**Description:** As a convoy system, I want the custom statuses `staged:ready`
and `staged:warnings` registered in the beads configuration, so that Phase 2
commands can use them.

**Acceptance Criteria:**

- [ ] The hq beads store has `staged:ready` and `staged:warnings` configured as
  valid custom statuses (via `bd config set status.custom` or equivalent).
- [ ] Document the status semantics:
  - `staged:ready` — DAG analysis passed, no warnings, convoy ready to launch
  - `staged:warnings` — DAG analysis passed with warnings, convoy launchable
    after user review
  - These statuses cannot be set after a convoy transitions to `open` (enforced
    by `gt convoy launch` and documented as a constraint)
- [ ] The stranded scan and event poll correctly ignore convoys in `staged:*`
  statuses (the stranded scan already filters `--status=open`; verify the
  event-driven path also skips non-open convoys).
- [ ] Unit test: convoy in `staged:ready` status is NOT picked up by the feeder.

**Key files:**
- `internal/daemon/convoy_manager.go` — verify `staged:*` convoys are ignored
- `internal/cmd/convoy.go` — add `staged:ready`, `staged:warnings` to
  `ensureKnownConvoyStatus`

---

## Phase 2: Stage & Launch Commands

### US-006: `gt convoy stage` — basic command

**Description:** As a user, I want to run `gt convoy stage <bead-id>` to create
a staged convoy from a pre-existing bead DAG, so that I can review the dispatch
plan before committing to execution.

**Acceptance Criteria:**

- [ ] `gt convoy stage <bead-id>` accepts a root bead ID (the root of the DAG).
- [ ] Creates a convoy bead (`--type=convoy`) in the hq store with status
  `staged:ready` or `staged:warnings` depending on the analysis outcome.
- [ ] Adds `tracks` dependencies from the convoy to ALL beads in the DAG
  (root epic, sub-epics, and leaf tasks).
- [ ] The root bead must exist. If it doesn't, exit with an error.
- [ ] If the root bead is already tracked by an existing open/staged convoy,
  exit with an error ("bead already tracked by convoy X").
- [ ] Convoy title defaults to the root bead's title, prefixed with
  "Convoy: " (e.g., "Convoy: Auth overhaul").
- [ ] Prints the route plan (tree view + wave view) to stdout (see US-010,
  US-011).

**Key files:**
- `internal/cmd/convoy.go` — new `stage` subcommand

---

### US-007: DAG walking and validation

**Description:** As a user, I want `gt convoy stage` to walk the bead DAG and
validate its structure, so that invalid configurations are caught before
dispatch.

**Acceptance Criteria:**

- [ ] Walk `parent-child` dependencies from the root bead to discover all
  beads in the DAG. Use SDK `store.GetDependenciesWithMetadata` (not CLI
  shelling) for performance.
- [ ] Classify each bead by type: epic (depth 1 = root, depth 2 = sub-epic)
  vs task/bug/feature/chore (leaf work items).
- [ ] **Max epic depth enforcement**: If any epic is nested deeper than 2
  levels, reject with an error and print restructuring suggestions (see US-008).
- [ ] **Cycle detection**: If the `blocks` dependency graph contains cycles,
  reject with an error listing the cycle.
- [ ] **Rig resolution**: For each leaf task, resolve the target rig via
  `beads.ExtractPrefix` -> `beads.GetRigNameForPrefix`. Tasks with no
  resolvable rig are flagged as errors.
- [ ] All validation runs before the convoy bead is created. If any error-level
  condition is found, no convoy is created.

**Key files:**
- `internal/convoy/` — new DAG analysis module

---

### US-008: Depth violation restructuring suggestions

**Description:** As a user, when my bead DAG violates the max epic depth of 2,
I want actionable suggestions for how to restructure it, so that I can fix the
DAG and re-run stage.

**Acceptance Criteria:**

- [ ] When a sub-epic at depth 3+ is detected, print:
  1. The violation: which bead, at what depth, under which parent
  2. The suggested fix: bump the violating sub-epic to depth 2 (sibling of its
     current parent) and add a `blocks` dependency to preserve ordering
  3. The exact `bd` commands to apply the fix
- [ ] Example output:
  ```
  Error: Epic depth exceeds maximum of 2

    gt-epic-abc (depth 1, root)
    +-- gt-sub-def (depth 2)
        +-- gt-sub-ghi (depth 3) <-- VIOLATION

  Suggested restructuring:
    Bump gt-sub-ghi to depth 2 (sibling of gt-sub-def)
    Add blocks dep: gt-sub-ghi must complete before gt-sub-def

  Commands:
    bd dep remove gt-sub-ghi gt-sub-def --type=parent-child
    bd dep add gt-epic-abc gt-sub-ghi --type=parent-child
    bd dep add gt-sub-def gt-sub-ghi --type=blocks
  ```
- [ ] If multiple violations exist, all are listed with individual fix commands.

---

### US-009: Wave computation

**Description:** As a convoy system, I want to compute dispatch waves from the
`blocks` dependency graph, so that the route plan shows the correct execution
order and the feeder dispatches tasks in the right sequence.

**Acceptance Criteria:**

- [ ] Build a directed graph of `blocks` dependencies among leaf tasks
  (task -> task edges). Epic-level `blocks` deps are resolved to their leaf
  descendants (e.g., if Sub-Epic A blocks Sub-Epic B, all of B's tasks are
  blocked by all of A's tasks).
- [ ] Compute waves via topological sort with level assignment:
  - Wave 1: tasks with no incoming `blocks` edges (ready immediately)
  - Wave N: tasks whose blockers are all in waves < N
- [ ] Handle `conditional-blocks` (runs only if blocker FAILS): these tasks
  are placed in the same wave as the blocker's dependents but only dispatched
  if the blocker closes with a failure keyword.
- [ ] Store the wave plan in the convoy bead's metadata (JSON) so that the
  feeder and launch command can read it without recomputing.
- [ ] Unit tests: DAG with linear chain (A->B->C) produces 3 waves. DAG with
  parallel tasks produces 1 wave. Mixed DAG produces correct wave grouping.

**Key files:**
- `internal/convoy/` — wave computation module

---

### US-010: Route plan display — tree view

**Description:** As a user, I want `gt convoy stage` to display a tree view of
the bead DAG, so that I can see the hierarchy and verify the structure is
correct.

**Acceptance Criteria:**

- [ ] Render the DAG as an indented tree using box-drawing characters:
  ```
  gt-epic-abc   epic    "Auth overhaul"
  |-- gt-task-1  task    -> shippercrm  "Fix auth timeout"
  |-- gt-sub-def epic    "Validation suite"
  |   |-- gt-task-2  task -> shippercrm  "Add input validation"
  |   +-- gt-task-3  task -> shippercrm  "Add error messages"  [blocks: gt-task-2]
  +-- gt-task-4  task    -> gastown     "Integration tests"    [blocks: gt-task-1]
  ```
- [ ] Each line shows: bead ID, type, target rig (for tasks), title, and
  blocking deps (if any).
- [ ] Epics show type but no target rig (they aren't slung).

---

### US-011: Route plan display — wave view

**Description:** As a user, I want `gt convoy stage` to display a wave view
showing the dispatch order, so that I can understand the execution timeline.

**Acceptance Criteria:**

- [ ] Render waves in order:
  ```
  Wave 1 (immediate):
    gt-task-1   -> shippercrm  "Fix auth timeout"
    gt-task-2   -> shippercrm  "Add input validation"

  Wave 2 (after Wave 1):
    gt-task-3   -> shippercrm  "Add error messages"     [blocked by: gt-task-2]
    gt-task-4   -> gastown     "Integration tests"      [blocked by: gt-task-1]

  Epics (managed by convoy):
    gt-epic-abc  "Auth overhaul"          [closes when all children complete]
    gt-sub-def   "Validation suite"       [closes when gt-task-2, gt-task-3 complete]
  ```
- [ ] Both tree view and wave view are printed by `gt convoy stage` (tree
  first, then waves).
- [ ] Warnings section appended after waves (see US-012).

---

### US-012: Staged analysis engine

**Description:** As a convoy system, I want the stage command to analyze the
DAG and classify the result as `staged:ready`, `staged:warnings`, or error, so
that the user knows whether the convoy is safe to launch.

**Acceptance Criteria:**

- [ ] **Error conditions** (convoy not created, exit with error):
  - Epic depth > 2
  - Cycle in `blocks` dependency graph
  - No leaf tasks found (empty DAG or all beads are epics)
  - Root bead does not exist
  - ALL leaf tasks have unresolvable rigs (no task can be dispatched)
- [ ] **Warning conditions** (`staged:warnings` status, launch requires
  confirmation):
  - One or more leaf tasks have unresolvable rigs (but not all)
  - Root epic has no integration branch
  - A leaf task targets a currently parked rig
  - A task has a `blocks` dependency on a bead outside the DAG (external
    blocker that the convoy cannot control)
  - Two beads of type `task` have a `parent-child` relationship (unusual
    structure — task should not be parent of another task)
- [ ] **Clean** (`staged:ready` status): no warnings, no errors.
- [ ] Warnings are printed in a `Warnings:` section of the route plan output.
- [ ] Warnings are stored in convoy bead metadata so `gt convoy launch` can
  re-display them.

---

### US-013: `gt convoy launch` command

**Description:** As a user, I want to run `gt convoy launch <convoy-id>` to
activate a previously staged convoy, so that I have a double opt-in before work
is dispatched.

**Acceptance Criteria:**

- [ ] Accepts a convoy bead ID. Verifies the convoy exists and is in
  `staged:ready` or `staged:warnings` status.
- [ ] If `staged:warnings`: re-display the warnings and prompt for confirmation
  ("Launch convoy with warnings? [y/N]").
- [ ] If convoy is already `open`: error ("convoy already launched").
- [ ] If convoy is `closed`: error ("convoy is closed, cannot launch").
- [ ] On confirmation:
  1. Transition convoy status from `staged:*` to `open`
  2. Dispatch Wave 1 tasks (immediate ready leaf tasks) via `gt sling`
  3. Print what was dispatched:
     ```
     Launched convoy hq-cv-xxxxx
     Dispatched Wave 1:
       gt-task-1 -> shippercrm
       gt-task-2 -> shippercrm
     ```
- [ ] After launch, the daemon's convoy feeder picks up subsequent waves as
  tasks close (event-driven + stranded scan).
- [ ] Setting a convoy back to `staged:*` status after launch is rejected by
  the convoy command layer (not a beads-level constraint — enforced in
  `gt convoy` commands).

**Key files:**
- `internal/cmd/convoy.go` — new `launch` subcommand

---

### US-014: Epic status management

**Description:** As a convoy manager, I want epic and sub-epic statuses to be
managed automatically based on child task lifecycle, so that the bead DAG
accurately reflects progress.

**Acceptance Criteria:**

- [ ] **open -> in_progress**: When the first child task of an epic is
  successfully slung (confirmed hooked to a polecat), transition the parent
  epic from `open` to `in_progress`.
- [ ] **in_progress -> closed**: When ALL direct children of an epic are
  `closed`, transition the epic to `closed`.
- [ ] **Recursive**: Sub-epic closure follows the same rule. The root epic
  closes only when all its direct children (sub-epics + standalone tasks) are
  closed.
- [ ] **Convoy completion**: When the root epic closes, the convoy's standard
  completion check triggers (all tracked beads closed -> convoy closes).
- [ ] Status transitions are performed by the convoy manager (daemon), not
  by individual polecats.
- [ ] Status transitions are logged: "Convoy hq-cv-xxx: epic gt-epic-abc
  -> in_progress (first child slung)" / "-> closed (all children complete)".
- [ ] Unit tests: epic with 3 children — verify status transitions at each
  step (first sling -> in_progress, last close -> closed).

**Key files:**
- `internal/convoy/operations.go` — epic status transition logic
- `internal/daemon/convoy_manager.go` — wire into event poll / post-dispatch

---

### US-015: Integration branch awareness

**Description:** As a user staging a convoy, I want to be warned if the root
epic lacks an integration branch, so that I can set one up before launching.

**Acceptance Criteria:**

- [ ] During DAG analysis, check if the root epic has an integration branch
  (read `integration_branch` from epic bead metadata via
  `beads.DetectIntegrationBranch` or equivalent).
- [ ] If no integration branch exists: add a warning to the staged analysis
  ("Root epic gt-epic-abc has no integration branch. Polecat work will merge
  directly to the default branch. Run `gt mq integration create gt-epic-abc`
  to set one up.").
- [ ] `gt convoy stage` does NOT auto-create the integration branch. That is
  the user's responsibility (or future strategy work).
- [ ] Integration branch landing remains the refinery's responsibility.
  The `integration_branch_auto_land` rig config controls whether landing
  happens automatically when the epic closes. The convoy does not trigger
  landing directly.
- [ ] When `auto_land` is disabled, a closed sub-epic's integration branch
  remains unmerged. This is expected behavior — the user or refinery
  handles landing separately. Document this tradeoff rather than trying
  to solve it. If `auto_land` is false and the convoy has sub-epics with
  integration branches, include a note in the route plan warnings.

**Key files:**
- `internal/beads/integration.go` — `DetectIntegrationBranch`
- `internal/convoy/` — DAG analysis (integration branch check)

---

### US-016: FeederStrategy interface

**Description:** As a system architect, I want a pluggable feeder strategy
interface, so that future dispatch strategies (e.g., AI coordinator) can be
added without modifying core convoy manager code.

**Acceptance Criteria:**

- [ ] Define `FeederStrategy` interface in `internal/convoy/`:
  ```go
  type FeederStrategy interface {
      // Feed dispatches the next round of work for a convoy.
      // Returns the IDs of issues that were dispatched.
      Feed(ctx context.Context, state *ConvoyState) ([]string, error)
      // Name returns the strategy identifier for logging/config.
      Name() string
  }
  ```
- [ ] `ConvoyState` contains: convoy ID, tracked beads with fresh status,
  wave plan (from metadata), rig capacity info, parked rig info.
- [ ] Implement `WaveStrategy` as the default strategy:
  - Reads wave plan from convoy metadata
  - Finds the current wave (highest wave where all prior waves are complete)
  - Dispatches ready leaf tasks in the current wave
  - Respects `max_polecats`, parked rigs, blocks deps
  - The feeder walks the hierarchy to find leaf tasks — it never slings
    epics or sub-epics
- [ ] The `ConvoyManager` uses the strategy for staged+launched convoys.
  Non-staged convoys (legacy, auto-created by sling) continue to use the
  existing direct dispatch path.
- [ ] The strategy is stored in convoy metadata (set at stage time, default
  "wave").
- [ ] Unit tests: mock strategy verifying the interface contract.

**Key files:**
- `internal/convoy/strategy.go` — interface + WaveStrategy
- `internal/daemon/convoy_manager.go` — strategy dispatch path

---

## Functional Requirements

- **FR-1:** The feeder MUST check `blocks` and `conditional-blocks` dependencies
  before dispatching any issue. A task whose blocker is not closed MUST NOT be
  slung.
- **FR-2:** The feeder MUST NOT treat `parent-child` dependencies as blocking.
  A child task is dispatchable even if its parent epic is open.
- **FR-3:** The feeder MUST NOT sling beads of type `epic`, `convoy`, `gate`,
  `event`, `agent`, `role`, `rig`, `message`, or `slot`. Only task-like beads
  (`task`, `bug`, `feature`, `chore`, `molecule`, `merge-request`) are slung.
- **FR-4:** The feeder MUST respect `max_polecats` per rig. If a rig is at
  capacity, tasks targeting that rig are skipped until capacity is available.
- **FR-5:** `gt convoy stage` MUST validate the DAG before creating the convoy.
  Error conditions prevent convoy creation entirely.
- **FR-6:** `gt convoy stage` MUST enforce max epic nesting depth of 2 (root
  epic at depth 1, sub-epics at depth 2, tasks as leaves). Deeper nesting is
  a hard error with restructuring suggestions.
- **FR-7:** `gt convoy launch` MUST be a separate command from `gt convoy stage`.
  Users must double opt-in: stage first, then launch.
- **FR-8:** `staged:ready` and `staged:warnings` statuses MUST NOT be settable
  after a convoy transitions to `open`. Enforced at the `gt convoy` command
  layer.
- **FR-9:** Epic status transitions (open -> in_progress -> closed) MUST be
  managed by the convoy manager, not by polecats.
- **FR-10:** The convoy MUST track ALL beads in the DAG via `tracks`
  dependencies (epics, sub-epics, and tasks) for status reporting purposes.
  The feeder only dispatches leaf tasks.
- **FR-11:** Wave computation MUST use topological sorting of `blocks`
  dependencies among leaf tasks. Epic-level `blocks` deps resolve to their
  leaf descendants.
- **FR-12:** `gt convoy stage` MUST print both a tree view and a wave view of
  the route plan.

## Non-Goals (Out of Scope)

- **Auto-formula-detection in `gt sling`**: Making `gt sling` auto-select
  formulas based on bead type (epic -> coordinator formula) is Phase 3.
- **Coordinator polecat strategy**: The AI-driven dispatch strategy where an
  epic is slung to a coordinator polecat is Phase 3.
- **Convoy-managed integration branch creation**: `gt convoy stage` does not
  auto-create integration branches. Users run `gt mq integration create`
  separately.
- **Convoy-managed integration branch landing**: Landing is the refinery's
  responsibility via `integration_branch_auto_land` config.
- **Sub-epic nesting beyond depth 2**: Enforced as a hard limit with
  restructuring guidance.
- **Fixing `gt convoy stranded` stale metadata** (Discovery #11): The stranded
  scan reads hq store dependency metadata which may be stale for cross-rig
  issues. This pre-existing upstream issue is not addressed.
- **Dynamic DAG decomposition**: Having Claude analyze an underspecified epic
  and create child tasks at runtime is Phase 3.

## Technical Considerations

### Beads Dependency Semantics

- **`tracks`**: convoy -> issue. Non-blocking. Used for convoy membership.
  Direction: `bd dep add <convoyID> <issueID> --type=tracks`.
- **`blocks`**: A -> B means A depends on B (B blocks A). B must close before A
  is dispatchable. Affects `bd ready`.
- **`parent-child`**: child -> parent. Used for hierarchy. Listed as a workflow
  type in beads schema but explicitly NOT treated as blocking for dispatch
  (consistent with molecule step behavior).
- **`conditional-blocks`**: Like `blocks`, but the dependent only runs if the
  blocker closes with a failure keyword.

### Integration Branch Architecture

Integration branches are fully implemented and wired into the epic -> task flow:
- `gt mq integration create <epic-id>` creates `integration/{title}` branch
- Polecats auto-source worktrees from the integration branch
  (`DetectIntegrationBranch` walks parent chain)
- `gt done` auto-targets MRs to the integration branch
- `gt mq integration land <epic-id>` merges to main when all children close
- `integration_branch_auto_land` enables automatic landing

Convoys and integration branches are currently independent. Phase 2 adds
awareness (warnings) but does not couple them. Future strategy implementations
may create/manage integration branches as part of their dispatch logic.

### Max Polecats Enforcement

`max_polecats` is defined in the rig property layer (`internal/rig/config.go`,
default 10) but is not enforced anywhere in the spawn path today. Phase 1 adds
the first enforcement point in the convoy feeder. The property is read via
`rig.GetIntConfig("max_polecats")` with 4-layer resolution (wisp -> bead ->
town -> system default).

Active polecat count per rig can be determined by enumerating tmux sessions
matching the rig's polecat naming pattern, or via the polecat manager's
tracking data.

### FeederStrategy Interface

The strategy interface decouples dispatch logic from the convoy manager:

```go
// internal/convoy/strategy.go

type FeederStrategy interface {
    Feed(ctx context.Context, state *ConvoyState) ([]string, error)
    Name() string
}

type ConvoyState struct {
    ConvoyID     string
    TrackedBeads []TrackedBead
    WavePlan     *WavePlan       // nil for non-staged convoys
    IsRigParked  func(string) bool
    RigCapacity  func(string) (current, max int)
}
```

Phase 2 implements `WaveStrategy`. Legacy (non-staged) convoys continue to use
the existing direct dispatch path until migrated.

### Custom Status Registration

`staged:ready` and `staged:warnings` are custom beads statuses. They must be
registered via `bd config set status.custom "staged:ready,staged:warnings"` in
the hq store. The `ensureKnownConvoyStatus` function in `convoy.go` must be
updated to accept these statuses.

## Success Metrics

- **Phase 1**: Convoys with mixed-rig tracked issues (some parked, some
  operational) progress without manual intervention. Blocked tasks are never
  dispatched prematurely.
- **Phase 2**: A user can `gt convoy stage` a 10-task DAG with 2 sub-epics,
  review the wave plan, `gt convoy launch` it, and watch the convoy progress
  through waves automatically — with epic statuses updating as children
  complete — until the convoy lands.

## Open Questions

1. **`bd ready` vs manual blocks check**: Does `bd ready` filter out beads
   whose parent epic is open (via `parent-child` workflow type)? If so, the
   feeder cannot rely on `bd ready` and must implement its own readiness check
   that ignores `parent-child` blocking. Needs testing.

2. **Non-auto-landed sub-epic ramifications**: When `auto_land` is false and
   Sub-Epic A closes before Sub-Epic B starts, B's polecats won't see A's code
   (different integration branches). This is standard git branching behavior
   (resolved at merge time), but may cause conflicts. Document this tradeoff
   rather than trying to solve it.

3. **Wave plan storage format**: The wave plan is stored in convoy bead
   metadata (JSON). The exact schema needs definition during implementation.
   Candidate: `{"waves": [["gt-task-1", "gt-task-2"], ["gt-task-3"]], "strategy": "wave"}`.

4. **Stale cross-rig blocking data**: The event-driven feeder uses the hq
   store for dependency lookups, but cross-rig blocking deps may reference
   issues whose status is stale in the hq snapshot. The stranded scan has the
   same issue (Discovery #11). For Phase 1, document the limitation. For
   Phase 2, the wave plan pre-computes ordering, reducing reliance on
   runtime blocking checks.
[/PRD]
