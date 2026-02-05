# Beads Load Analysis

Performance analysis of beads under high concurrency in Gas Town multi-agent environments.

**Date**: 2026-02-05
**Environment**: Gas Town with Dolt backend, openedi rig (2.9MB JSONL, ~200+ issues)

---

## Executive Summary

Under high concurrency (20-40 parallel writes), **JSONL auto-flush is the primary bottleneck**, causing up to 16x slowdown. The periodic daemon export (every 30s) creates a 7.5-second window where writes are 8.8x slower.

### Key Findings

| Bottleneck | Impact | Mitigation |
|------------|--------|------------|
| Per-mutation JSONL flush | 16x slower at 20 concurrent writes | `no-auto-flush: true` |
| Periodic daemon export | 8.8x slower writes during 7.5s window | Increase `remote-sync-interval` |
| No-daemon mode | 18x slower than daemon mode | Always use daemon |

---

## Stress Test Results

### Test Environment
- Rig: openedi (dolt-native mode)
- JSONL size: 2.9MB
- Dolt server on port 3307
- Semaphore: MaxConcurrentBd=3

### Baseline Operations (Single)

| Operation | Duration |
|-----------|----------|
| `bd list` | 0.41s |
| `bd show` | 0.22s |
| `bd create` | 0.20s |

### Concurrent Reads (bd list)

| Concurrency | Duration | Scaling |
|-------------|----------|---------|
| 5x parallel | 0.53s | 1.3x baseline |
| 10x parallel | 0.82s | 2.0x baseline |
| 20x parallel | 1.57s | 3.8x baseline |

Reads scale well under concurrency.

### Concurrent Writes (bd create) - Default Config

| Concurrency | Duration | Per-op | Notes |
|-------------|----------|--------|-------|
| 5x parallel | 0.68s | 0.14s | Good |
| 10x parallel | 0.97s | 0.10s | Good |
| **20x parallel** | **34.8s** | 1.74s | Severe degradation |
| **40x parallel** | **46.4s** | 1.16s | Severe degradation |

### Concurrent Writes - With `--no-auto-flush`

| Concurrency | Default | No-auto-flush | Speedup |
|-------------|---------|---------------|---------|
| 20x parallel | 34.8s | **2.1s** | **16x faster** |
| 40x parallel | 46.4s | **8.6s** | **5x faster** |

### Daemon vs No-Daemon Mode

| Mode | 20x Parallel Writes |
|------|---------------------|
| Daemon + no-auto-flush | 2.1s |
| No-daemon + no-auto-flush | 37.9s |

**The daemon provides critical lock coordination.** Without it, each process contends for Dolt access.

---

## Periodic Export Impact

### JSONL Export Timing

| Metric | Value |
|--------|-------|
| Export duration (2.9MB) | **7.5 seconds** |
| Default sync interval | 30 seconds |
| Time spent exporting | **25% of runtime** |

### Concurrency During Export

| Operation | No Export | During Export | Impact |
|-----------|-----------|---------------|--------|
| 5x reads | 0.42s | 0.53s | 1.3x (minimal) |
| 10x writes | 0.97s | **8.5s** | **8.8x slower** |

**Every 30 seconds, there's a 7.5-second window where write performance degrades 8.8x.**

---

## Configuration Options

### Sync Modes

| Mode | Description | JSONL Behavior |
|------|-------------|----------------|
| `git-portable` | Default, JSONL primary | Export on push, import on pull |
| `realtime` | Immediate persistence | Export on every mutation |
| `dolt-native` | Dolt primary | Export-only (backup), no import |
| `belt-and-suspenders` | Maximum redundancy | Both Dolt and JSONL active |

### Key Settings

```yaml
# Per-mutation flush (biggest impact)
no-auto-flush: true          # 16x speedup at high concurrency

# Periodic daemon export interval
remote-sync-interval: 120s   # Reduce export frequency (default: 30s)
remote-sync-interval: 0      # Disable periodic sync entirely

# Auto-import from JSONL
no-auto-import: true         # Skip JSONL→DB import checks

# Flush debounce (minor impact)
flush-debounce: 5s           # Coalesce writes within window (default)
```

### Environment Variables

```bash
BD_NO_AUTO_FLUSH=1           # Disable per-mutation flush
BD_FLUSH_DEBOUNCE=30s        # Longer debounce window
BD_REMOTE_SYNC_INTERVAL=0    # Disable periodic sync
```

---

## Recommended Configurations

### For Polecats (High-Throughput Workers)

```yaml
# In rig config.yaml
sync.mode: "dolt-native"
no-auto-flush: true
no-auto-import: true
remote-sync-interval: 0      # Or 120s if remote pull needed
```

Or via gastown's AgentEnvConfig:
```go
BeadsNoAutoFlush: true       // Set BD_NO_AUTO_FLUSH=1 for polecats
```

### For Town HQ (Mayor/Human Coordination)

```yaml
# Keep visibility for humans
sync.mode: "belt-and-suspenders"
no-auto-flush: false         # Immediate JSONL for git visibility
flush-debounce: 5s
remote-sync-interval: 30s    # Regular sync for collaboration
```

### For Batch Operations

```bash
# Run with no-auto-flush, then flush at end
BD_NO_AUTO_FLUSH=1 bd create ...
BD_NO_AUTO_FLUSH=1 bd create ...
bd sync --flush-only         # Single export at end
```

---

## Current Rig Configurations

| Rig | sync.mode | no-auto-flush | no-auto-import | remote-sync-interval |
|-----|-----------|---------------|----------------|----------------------|
| Town HQ | belt-and-suspenders | false | false | 30s (default) |
| openedi | dolt-native | false | false | 30s (default) |
| shippercrm | belt-and-suspenders | false | **true** | 30s (default) |
| gastown | (no config) | false | false | 30s (default) |
| beads | (no config) | false | false | 30s (default) |

---

## Architecture Notes

### Daemon Semaphore

The daemon uses `MaxConcurrentBd=3` to limit concurrent database access:
- Located in: `gastown/internal/beads/semaphore.go`
- Prevents lock thrashing under high concurrency
- Sufficient for typical multi-agent workloads

### JSONL Export Path

Even with `no-auto-flush: true`, JSONL export still happens:
1. Per-mutation flush: Disabled by `no-auto-flush`
2. Periodic daemon sync: Still runs every `remote-sync-interval`
3. Manual: `bd sync --flush-only`

To completely eliminate JSONL operations, beads would need a new `no-jsonl-export` flag.

### Why JSONL Exists

- Git-visible audit trail
- Portable backup (works without Dolt)
- Human-readable issue history
- Cross-clone synchronization (non-Dolt workflows)

In `dolt-native` mode, JSONL serves only as backup - Dolt handles all sync.

---

## Potential Improvements

### Short-term (Configuration)

1. Set `no-auto-flush: true` for all polecat sessions
2. Increase `remote-sync-interval` to 120s or disable (0)
3. Add `BD_NO_AUTO_FLUSH=1` to gastown's polecat env config

### Medium-term (Beads Changes)

1. Add `no-jsonl-export: true` config option
2. Add `dolt-only` sync mode that completely skips JSONL
3. Make periodic export respect `no-auto-flush` setting

### Long-term (Architecture)

1. Async JSONL export (non-blocking background thread)
2. Incremental JSONL export (only changed issues)
3. Separate read/write paths to avoid export blocking reads

---

## Test Commands

```bash
# Time JSONL export
time bd sync --flush-only

# Concurrent write stress test
for i in $(seq 1 20); do
  bd create --rig openedi "stress-test-$i" --type=task &
done
wait

# Test with no-auto-flush
for i in $(seq 1 20); do
  bd create --rig openedi "stress-test-$i" --type=task --no-auto-flush &
done
wait
bd sync --flush-only

# Check JSONL size
ls -lh .beads/issues.jsonl
```

---

## References

- Beads config: `~/beads/internal/config/config.go`
- Daemon sync: `~/beads/cmd/bd/daemon_sync.go`
- Flush manager: `~/beads/cmd/bd/flush_manager.go`
- Sync modes: `~/beads/cmd/bd/sync_mode.go`
- Gastown env config: `~/gastown/internal/config/env.go`
- Semaphore: `~/gastown/internal/beads/semaphore.go`
