#!/usr/bin/env python3
"""
Generate synthetic single-turn training examples for scenario-based decisions.

The model defaults to "none" on short contexts because 62% of single-turn
training examples are "none". This script creates targeted examples where
short inputs map to the correct action tools.

Each scenario template has variations to prevent overfitting to exact phrasing.
"""

import json
import random
import os

SYSTEM_PROMPT = """You are a Witness agent. You respond ONLY with JSON tool calls.

For each turn, output exactly one JSON object:
{"tool": "<tool_name>", "args": {<arguments>}}

If no action is needed, output:
{"tool": "none", "args": {}}

Available tools: gt_polecat_list, gt_polecat_nuke, gt_peek, gt_session_status, gt_nudge, gt_mail_inbox, gt_mail_read, gt_mail_send, gt_patrol_report, gt_handoff, gt_escalate, bd_show, bd_list, bd_close, bd_children, check_git_state, check_tmux_session, bash"""

RIGS = ["gastown", "bcc", "hq", "zfc"]
POLECATS = ["furiosa", "nux", "rust", "guzzle", "nitro", "chrome", "refinery"]
WISPS = ["hq-wisp-2v214", "bcc-wisp-x8k3m", "gt-kvo.6", "hq-wisp-ragxshg", "bcc-wisp-gb0j5u"]
BEADS = ["bcc-8rlwh", "gt-4tp", "hq-3mn2", "zfc-9qw1", "bcc-wisp-2fgi4p"]
BRANCHES = ["feature/fix-auth", "feature/add-tests", "integration/beads-ide", "fix/type-errors", "feature/command-palette"]


def make_idle_patrol():
    """Idle patrol → gt_patrol_report or none"""
    rig = random.choice(RIGS)
    crew = random.choice(["crew/zfc", "crew/bcc", "crew/hq"])
    n_old = random.choice([0, 1, 3, 5])
    templates = [
        "Polecats\n\nNo active polecats.",
        "Polecats\n\nNo polecats running.\n\nAll sessions idle.",
        f"Polecats\n\nNo active polecats.\n\nDeacon: alive\nRefinery: running\n{rig} rig quiet.",
        "Patrol check: no polecats active, infrastructure healthy.",
        f"Polecats\n\n(none active)\n\nInbox: 0 unread\n{rig} rig idle.",
        # Real CLI output formats from gt polecat list + gt mail inbox
        "Polecats\n\nNo polecats found.",
        f"Polecats\n\nNo polecats found.\n\nInbox\n\n📬 Inbox: gastown/{crew} (0 messages, 0 unread)\n  (no messages)",
        f"Polecats\n\nNo polecats found.\n\nInbox\n\n📬 Inbox: gastown/{crew} ({n_old} messages, 0 unread)\n  (no messages)",
        f"Polecats\n\nNo polecats found.\n\nInbox\n\n📬 Inbox: gastown/{crew} (1 messages, 0 unread)\n  (no messages)",
        # Variations with infrastructure healthy
        f"Polecats\n\nNo polecats found.\n\nInbox\n\n📬 Inbox: gastown/{crew} (0 messages, 0 unread)\n  (no messages)\n\nDeacon: alive\nRefinery: running",
    ]
    tools = [
        {"tool": "gt_patrol_report", "args": {"status": "idle", "note": "No active polecats. Rig quiet."}},
        {"tool": "none", "args": {}},
        {"tool": "gt_patrol_report", "args": {"status": "idle"}},
        {"tool": "none", "args": {}},
        {"tool": "none", "args": {}},
    ]
    return random.choice(templates), random.choice(tools)


def make_healthy_polecat():
    """Active healthy polecat → gt_peek or gt_session_status (NOT nudge)"""
    rig = random.choice(RIGS)
    polecat = random.choice(POLECATS)
    wisp = random.choice(WISPS)
    bead = random.choice(BEADS)
    recent_mins = random.choice([1, 2, 3, 5])
    templates = [
        f"Polecats\n\n  ● {rig}/{polecat}  working\n    {wisp}\n\nWorking on {bead}. Last activity: {recent_mins} minutes ago. Making progress.",
        f"Polecats\n\n  ● {rig}/{polecat}  working\n    {wisp}\n  ○ {rig}/{random.choice(POLECATS)}  done\n    {random.choice(WISPS)}\n\nActive output detected. {polecat} is healthy.",
        f"Polecats\n\n  ● {rig}/{polecat}  working\n    {wisp}\n\nLast commit: {recent_mins}min ago. Context usage: 34%. Healthy.",
        f"Polecat status:\n  {rig}/{polecat}: active (working on {bead})\n  Session: {wisp}\n  Last activity: {recent_mins}m ago, producing output normally.",
        f"Polecats\n\n  ● {rig}/{polecat}  working\n    {wisp}\n\n{polecat} is actively working on {bead}. Recent output visible. No issues.",
        f"Polecats\n\n  ● {rig}/{polecat}  working\n    {wisp}\n\nHealthy. Making progress on {bead}. {recent_mins}min since last activity.",
        # Bare status — no negative signal means healthy (DO NOT nudge)
        f"Polecats\n\n  ● {rig}/{polecat}  working\n    {wisp}\n  ○ {rig}/{random.choice(POLECATS)}  done\n    {random.choice(WISPS)}",
        f"Polecats\n\n  ● {rig}/{polecat}  working\n    {wisp}",
    ]
    tools = [
        {"tool": "gt_peek", "args": {"target": f"{rig}/{polecat}", "lines": 30}},
        {"tool": "gt_peek", "args": {"target": f"{rig}/{polecat}"}},
        {"tool": "gt_session_status", "args": {"session": wisp}},
        {"tool": "gt_polecat_list", "args": {"rig": rig}},
        {"tool": "gt_patrol_report", "args": {"status": "active", "note": f"{polecat} healthy and working on {bead}."}},
    ]
    return random.choice(templates), random.choice(tools)


def make_stuck_polecat():
    """Stuck polecat → gt_nudge"""
    rig = random.choice(RIGS)
    polecat = random.choice(POLECATS)
    wisp = random.choice(WISPS)
    idle_mins = random.choice([15, 20, 30, 45, 60])
    templates = [
        f"Polecats\n\n  ● {rig}/{polecat}  working\n    {wisp}\n\nLast activity: {idle_mins} minutes ago. No progress detected.",
        f"Polecats\n\n  ● {rig}/{polecat}  idle\n    {wisp}\n\nNo output for {idle_mins} minutes. May be stuck.",
        f"Polecat {rig}/{polecat} appears stuck.\nSession: {wisp}\nLast activity: {idle_mins}min ago\nNo new output or commits.",
        f"Polecats\n\n  ● {rig}/{polecat}  working\n    {wisp}\n\nStale for {idle_mins}m. Context window may be full.",
    ]
    tools = [
        {"tool": "gt_nudge", "args": {"target": f"{rig}/{polecat}", "message": "Are you still working? No progress detected."}},
        {"tool": "gt_nudge", "args": {"target": f"{rig}/{polecat}"}},
        {"tool": "gt_peek", "args": {"target": f"{rig}/{polecat}", "lines": 50}},
    ]
    # Weighted: nudge is the primary correct action
    weights = [3, 3, 1]
    return random.choice(templates), random.choices(tools, weights=weights, k=1)[0]


def make_completed_polecat():
    """Completed polecat with clean git → gt_polecat_nuke or check_git_state"""
    rig = random.choice(RIGS)
    polecat = random.choice(POLECATS)
    wisp = random.choice(WISPS)
    branch = random.choice(BRANCHES)
    templates = [
        f"Polecats\n\n  ○ {rig}/{polecat}  done\n    {wisp}\n\nAll work pushed. Branch merged.",
        f"Polecats\n\n  ○ {rig}/{polecat}  done\n    {wisp}\n\nBranch {branch} merged to main. Git clean.",
        f"Polecat {rig}/{polecat} completed.\nSession: {wisp}\nGit state: clean, branch merged.\nReady for cleanup.",
        f"Polecats\n\n  ○ {rig}/{polecat}  done\n    {wisp}\n\nPOLECAT_DONE received. Branch {branch} merged. No uncommitted changes.",
    ]
    tools = [
        {"tool": "gt_polecat_nuke", "args": {"target": f"{rig}/{polecat}"}},
        {"tool": "check_git_state", "args": {"session": wisp}},
        {"tool": "gt_polecat_nuke", "args": {"target": f"{rig}/{polecat}", "force": False}},
    ]
    weights = [3, 2, 2]
    return random.choice(templates), random.choices(tools, weights=weights, k=1)[0]


def make_crash_loop():
    """Crash-looping polecat → gt_mail_send (escalate) or gt_polecat_nuke --force"""
    rig = random.choice(RIGS)
    polecat = random.choice(POLECATS)
    wisp = random.choice(WISPS)
    restarts = random.choice([3, 4, 5, 6])
    crash_reason = random.choice(["segfault", "OOM", "context overflow", "API timeout", "module not found"])
    templates = [
        f"Polecats\n\n  ✗ {rig}/{polecat}  crashed\n    {wisp}\n\nRestart count: {restarts}. Last crash: {crash_reason}.",
        f"Polecat {rig}/{polecat} in crash loop.\nSession: {wisp}\nRestarts: {restarts}\nCrash: {crash_reason}\nNot recovering.",
        f"Polecats\n\n  ✗ {rig}/{polecat}  crashed\n    {wisp}\n\n{restarts} restarts in 30 minutes. Crash: {crash_reason}. Needs intervention.",
        f"ALERT: {rig}/{polecat} crash loop detected.\nRestarts: {restarts}\nLast error: {crash_reason}\nSession {wisp} unstable.",
    ]
    tools = [
        {"tool": "gt_mail_send", "args": {"recipient": "mayor/", "subject": f"ESCALATION: {rig}/{polecat} crash loop", "body": f"{polecat} has crashed {restarts} times ({crash_reason}). Needs intervention."}},
        {"tool": "gt_polecat_nuke", "args": {"target": f"{rig}/{polecat}", "force": True}},
        {"tool": "gt_mail_send", "args": {"recipient": "mayor/", "subject": f"ALERT: {rig}/{polecat} crash loop ({restarts} restarts)", "body": f"Crash reason: {crash_reason}. Escalating for intervention."}},
    ]
    weights = [3, 2, 2]
    return random.choice(templates), random.choices(tools, weights=weights, k=1)[0]


def make_unpushed_work():
    """Completed but unpushed work → gt_mail_send (escalate) or check_git_state"""
    rig = random.choice(RIGS)
    polecat = random.choice(POLECATS)
    wisp = random.choice(WISPS)
    branch = random.choice(BRANCHES)
    n_commits = random.choice([1, 2, 3, 5])
    templates = [
        f"Polecats\n\n  ○ {rig}/{polecat}  done\n    {wisp}\n\nGit state: {n_commits} unpushed commits on branch {branch}.",
        f"Polecat {rig}/{polecat} completed but has unpushed work.\nSession: {wisp}\nBranch: {branch}\n{n_commits} local commits not pushed.",
        f"Polecats\n\n  ○ {rig}/{polecat}  done\n    {wisp}\n\nWARNING: {n_commits} unpushed commits on {branch}. DO NOT nuke.",
        f"ALERT: {rig}/{polecat} done but unpushed.\n{n_commits} commits on {branch} not pushed to remote.\nSession: {wisp}\nRecovery needed before cleanup.",
    ]
    tools = [
        {"tool": "gt_mail_send", "args": {"recipient": "mayor/", "subject": f"RECOVERY_NEEDED: {rig}/{polecat} has unpushed work", "body": f"{n_commits} unpushed commits on {branch}. DO NOT nuke."}},
        {"tool": "check_git_state", "args": {"session": wisp}},
        {"tool": "gt_mail_send", "args": {"recipient": "mayor/", "subject": f"ESCALATION: unpushed work on {rig}/{polecat}", "body": f"Branch {branch} has {n_commits} unpushed commits. Needs recovery."}},
    ]
    weights = [3, 2, 2]
    return random.choice(templates), random.choices(tools, weights=weights, k=1)[0]


def make_check_infrastructure():
    """Infrastructure check → check_tmux_session or gt_session_status"""
    rig = random.choice(RIGS)
    templates = [
        f"Infrastructure check requested.\n\nDeacon: unknown\nRefinery: unknown",
        f"Patrol: need to verify {rig} infrastructure.\nDeacon status: unknown\nRefinery status: unknown",
        f"Check deacon and refinery health for {rig}.",
        f"Infrastructure status unknown.\nDeacon: ?\nRefinery: ?\nNeed to verify sessions are alive.",
    ]
    tools = [
        {"tool": "check_tmux_session", "args": {"session": "deacon"}},
        {"tool": "gt_session_status", "args": {}},
        {"tool": "check_tmux_session", "args": {"session": "refinery"}},
        {"tool": "bash", "args": {"command": "tmux list-sessions 2>/dev/null"}},
    ]
    return random.choice(templates), random.choice(tools)


def make_mail_check():
    """Mail check → gt_mail_inbox"""
    n_unread = random.choice([1, 3, 5, 12, 27])
    templates = [
        "Check inbox for new messages.",
        f"📬 Inbox: {n_unread} unread messages.",
        "Patrol step: check mail for handoffs or escalations.",
        f"Mail check. {n_unread} unread in witness inbox.",
        "Check inbox before next patrol cycle.",
    ]
    tools = [
        {"tool": "gt_mail_inbox", "args": {}},
        {"tool": "gt_mail_read", "args": {"mail_id": "1"}},
    ]
    weights = [4, 1]
    return random.choice(templates), random.choices(tools, weights=weights, k=1)[0]


SCENARIO_GENERATORS = [
    (make_idle_patrol, 3.0),          # upweight — must learn "quiet rig → none"
    (make_healthy_polecat, 3.0),    # upweight — must distinguish from stuck
    (make_stuck_polecat, 2.0),      # upweight rare but important
    (make_completed_polecat, 2.0),   # upweight
    (make_crash_loop, 2.5),          # upweight — hardest scenario
    (make_unpushed_work, 2.5),       # upweight — hardest scenario
    (make_check_infrastructure, 1.5),
    (make_mail_check, 1.0),
]


def generate_examples(n: int = 200, seed: int = 42) -> list:
    """Generate n synthetic scenario examples."""
    rng = random.Random(seed)
    random.seed(seed)

    # Weighted selection of scenario types
    generators, weights = zip(*SCENARIO_GENERATORS)
    total_weight = sum(weights)
    probs = [w / total_weight for w in weights]

    examples = []
    for _ in range(n):
        gen = rng.choices(generators, weights=probs, k=1)[0]
        user_msg, tool_call = gen()

        example = [
            {"role": "system", "content": SYSTEM_PROMPT},
            {"role": "user", "content": user_msg},
            {"role": "assistant", "content": json.dumps(tool_call)},
        ]
        examples.append(example)

    return examples


def main():
    import argparse
    parser = argparse.ArgumentParser(description="Generate synthetic scenario training data")
    parser.add_argument("--n", type=int, default=200, help="Number of examples to generate")
    parser.add_argument("--output", default="./dataset/format_b_decisions/format_b/synthetic_scenarios.jsonl")
    parser.add_argument("--seed", type=int, default=42)
    parser.add_argument("--merge", action="store_true",
                        help="Merge with existing train.jsonl and write combined output")
    args = parser.parse_args()

    examples = generate_examples(args.n, args.seed)

    # Stats
    from collections import Counter
    tool_counts = Counter()
    for ex in examples:
        tool = json.loads(ex[-1]["content"]).get("tool", "")
        tool_counts[tool] += 1

    print(f"Generated {len(examples)} synthetic examples")
    print(f"\nTool distribution:")
    for tool, count in tool_counts.most_common():
        print(f"  {tool:30s} {count:5d} ({count/len(examples)*100:.1f}%)")

    os.makedirs(os.path.dirname(args.output), exist_ok=True)

    if args.merge:
        # Read existing train data and merge
        train_path = os.path.join(os.path.dirname(args.output), "train.jsonl")
        with open(train_path) as f:
            existing = [json.loads(line) for line in f]
        print(f"\nMerging with {len(existing)} existing examples")

        combined = existing + examples
        random.seed(args.seed)
        random.shuffle(combined)

        merged_path = os.path.join(os.path.dirname(args.output), "train_with_synthetic.jsonl")
        with open(merged_path, "w") as f:
            for ex in combined:
                f.write(json.dumps(ex) + "\n")
        print(f"Written {len(combined)} merged examples to {merged_path}")
    else:
        with open(args.output, "w") as f:
            for ex in examples:
                f.write(json.dumps(ex) + "\n")
        print(f"\nWritten to {args.output}")

    # Show sample
    print(f"\nSample examples:")
    for ex in examples[:3]:
        user = ex[1]["content"][:100].replace("\n", " ")
        tool = json.loads(ex[-1]["content"])
        print(f"  User: {user}")
        print(f"  Tool: {tool['tool']}, Args: {json.dumps(tool.get('args',{}))[:80]}")
        print()


if __name__ == "__main__":
    main()
