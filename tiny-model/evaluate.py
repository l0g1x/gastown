#!/usr/bin/env python3
"""
Evaluation script for fine-tuned witness models.

Measures:
1. JSON validity rate — does the model produce parseable JSON?
2. Tool accuracy — does it pick the right tool for the scenario?
3. Inference latency — CPU wall-clock time per generation
4. Format compliance — does the output match expected schema?

Usage:
    python evaluate.py --checkpoint ./checkpoints/smollm2-135m_fmtb_full_ep3/final
    python evaluate.py --checkpoint ./checkpoints/smollm2-135m_fmtb_full_ep3/final --eval-set ./dataset/format_b/chunked_2k/eval.jsonl
"""

import argparse
import json
import os
import re
import time
import torch
from pathlib import Path

from transformers import AutoTokenizer, AutoModelForCausalLM


VALID_TOOLS = {
    "gt_polecat_list", "gt_polecat_nuke", "gt_peek", "gt_session_status",
    "gt_nudge", "gt_mail_inbox", "gt_mail_read", "gt_mail_send",
    "gt_patrol_report", "gt_handoff", "gt_escalate",
    "bd_show", "bd_list", "bd_close", "bd_children",
    "check_git_state", "check_tmux_session",
    "bash", "none",
}

# Scenario-based test cases with expected tool categories
SCENARIOS = [
    {
        "name": "idle_patrol",
        "messages": [
            {"role": "user", "content": "Polecats\n\nNo active polecats."},
        ],
        "expected_tools": {"gt_patrol_report", "none"},
        "description": "No polecats active → should report idle or do nothing",
    },
    {
        "name": "healthy_polecat",
        "messages": [
            {"role": "user", "content": "Polecats\n\n  ● gastown/furiosa  working\n    hq-wisp-2v214\n  ○ gastown/nux  done\n    gt-kvo.6"},
        ],
        "expected_tools": {"gt_peek", "gt_session_status", "gt_polecat_list", "gt_patrol_report"},
        "description": "Active polecat → should peek or check status",
    },
    {
        "name": "stuck_polecat",
        "messages": [
            {"role": "user", "content": "Polecats\n\n  ● gastown/furiosa  working\n    hq-wisp-2v214\n\nLast activity: 45 minutes ago. No progress detected."},
        ],
        "expected_tools": {"gt_nudge", "gt_peek", "gt_session_status"},
        "description": "Stuck polecat → should nudge or investigate",
    },
    {
        "name": "completed_polecat",
        "messages": [
            {"role": "user", "content": "Polecats\n\n  ○ gastown/furiosa  done\n    hq-wisp-2v214\n\nAll work pushed. Branch merged."},
        ],
        "expected_tools": {"gt_polecat_nuke", "check_git_state", "gt_mail_send"},
        "description": "Completed polecat with clean git → should nuke or verify git",
    },
    {
        "name": "crash_loop",
        "messages": [
            {"role": "user", "content": "Polecats\n\n  ✗ gastown/furiosa  crashed\n    hq-wisp-2v214\n\nRestart count: 4. Last crash: segfault."},
        ],
        "expected_tools": {"gt_escalate", "gt_polecat_nuke", "gt_mail_send"},
        "description": "Crash-looping polecat → should escalate or force nuke",
    },
    {
        "name": "unpushed_work",
        "messages": [
            {"role": "user", "content": "Polecats\n\n  ○ gastown/furiosa  done\n    hq-wisp-2v214\n\nGit state: 3 unpushed commits on branch feature/fix-auth."},
        ],
        "expected_tools": {"gt_escalate", "gt_mail_send", "check_git_state"},
        "description": "Completed but unpushed work → should escalate, NOT nuke",
    },
    {
        "name": "check_infrastructure",
        "messages": [
            {"role": "user", "content": "Infrastructure check requested.\n\nDeacon: unknown\nRefinery: unknown"},
        ],
        "expected_tools": {"check_tmux_session", "gt_session_status", "bash"},
        "description": "Infrastructure check → should verify deacon/refinery sessions",
    },
    {
        "name": "mail_check",
        "messages": [
            {"role": "user", "content": "Check inbox for new messages."},
        ],
        "expected_tools": {"gt_mail_inbox", "gt_mail_read"},
        "description": "Mail check → should check inbox",
    },
]


def load_model(checkpoint_path: str):
    """Load model and tokenizer from checkpoint."""
    print(f"Loading model from {checkpoint_path}...")
    tokenizer = AutoTokenizer.from_pretrained(checkpoint_path, trust_remote_code=True)
    if tokenizer.pad_token is None:
        tokenizer.pad_token = tokenizer.eos_token

    model = AutoModelForCausalLM.from_pretrained(
        checkpoint_path,
        dtype=torch.float32,
        trust_remote_code=True,
    )
    model.eval()
    n_params = sum(p.numel() for p in model.parameters())
    print(f"Loaded: {n_params/1e6:.1f}M params")
    return model, tokenizer


def generate_response(model, tokenizer, messages: list, system_prompt: str,
                      max_new_tokens: int = 150) -> tuple:
    """Generate a response and return (text, latency_ms)."""
    full_messages = [{"role": "system", "content": system_prompt}] + messages

    prompt = tokenizer.apply_chat_template(
        full_messages, tokenize=False, add_generation_prompt=True
    )
    inputs = tokenizer(prompt, return_tensors="pt")

    start = time.perf_counter()
    with torch.no_grad():
        out = model.generate(
            **inputs,
            max_new_tokens=max_new_tokens,
            do_sample=False,
            pad_token_id=tokenizer.pad_token_id,
        )
    latency = (time.perf_counter() - start) * 1000

    generated = tokenizer.decode(
        out[0][inputs.input_ids.shape[1]:],
        skip_special_tokens=True
    )
    return generated.strip(), latency


def parse_json_output(text: str) -> dict | None:
    """Try to extract a JSON object from model output."""
    # Try direct parse first
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        pass

    # Try to find JSON in the text
    match = re.search(r'\{[^{}]*\}', text)
    if match:
        try:
            return json.loads(match.group())
        except json.JSONDecodeError:
            pass

    # Try to find nested JSON (tool calls with args)
    match = re.search(r'\{.*\}', text, re.DOTALL)
    if match:
        try:
            return json.loads(match.group())
        except json.JSONDecodeError:
            pass

    return None


def evaluate_scenarios(model, tokenizer, system_prompt: str) -> dict:
    """Run scenario-based evaluation."""
    results = []

    print(f"\n{'='*70}")
    print("SCENARIO EVALUATION")
    print(f"{'='*70}")

    for scenario in SCENARIOS:
        output, latency = generate_response(model, tokenizer, scenario["messages"], system_prompt)
        parsed = parse_json_output(output)

        is_valid_json = parsed is not None
        tool_name = parsed.get("tool", "") if parsed else ""
        is_valid_tool = tool_name in VALID_TOOLS
        is_correct_tool = tool_name in scenario["expected_tools"]
        has_args = isinstance(parsed.get("args"), dict) if parsed else False

        result = {
            "scenario": scenario["name"],
            "description": scenario["description"],
            "output": output[:200],
            "parsed": parsed,
            "valid_json": is_valid_json,
            "valid_tool": is_valid_tool,
            "correct_tool": is_correct_tool,
            "has_args": has_args,
            "latency_ms": latency,
        }
        results.append(result)

        status = "OK" if (is_valid_json and is_correct_tool) else "FAIL" if not is_valid_json else "WRONG"
        print(f"\n  [{status:5s}] {scenario['name']}")
        print(f"         Expected: {scenario['expected_tools']}")
        print(f"         Got tool: {tool_name!r}")
        print(f"         Latency:  {latency:.0f}ms")
        print(f"         Output:   {output[:120]}")

    return results


def evaluate_eval_set(model, tokenizer, eval_path: str, system_prompt: str,
                      max_examples: int = 50) -> dict:
    """Evaluate on held-out eval set."""
    print(f"\n{'='*70}")
    print(f"EVAL SET: {eval_path}")
    print(f"{'='*70}")

    with open(eval_path) as f:
        eval_convs = [json.loads(line) for line in f]

    if max_examples:
        eval_convs = eval_convs[:max_examples]

    valid_json_count = 0
    valid_tool_count = 0
    correct_schema_count = 0
    latencies = []
    total_turns = 0

    for i, conv in enumerate(eval_convs):
        # Find user messages (skip system) and get the model to respond
        user_msgs = []
        for m in conv:
            if m["role"] == "system":
                continue
            elif m["role"] == "user":
                user_msgs.append(m)
            elif m["role"] == "assistant":
                # This is a turn we can evaluate
                if not user_msgs:
                    continue

                output, latency = generate_response(
                    model, tokenizer, user_msgs, system_prompt
                )
                latencies.append(latency)

                parsed = parse_json_output(output)
                if parsed is not None:
                    valid_json_count += 1
                    tool = parsed.get("tool", "")
                    if tool in VALID_TOOLS:
                        valid_tool_count += 1
                    if isinstance(parsed.get("args"), dict):
                        correct_schema_count += 1

                total_turns += 1
                # Only evaluate first assistant turn per conversation for speed
                break

        if (i + 1) % 10 == 0:
            print(f"  Processed {i+1}/{len(eval_convs)} conversations...")

    return {
        "total_turns": total_turns,
        "valid_json": valid_json_count,
        "valid_json_rate": valid_json_count / max(total_turns, 1),
        "valid_tool": valid_tool_count,
        "valid_tool_rate": valid_tool_count / max(total_turns, 1),
        "correct_schema": correct_schema_count,
        "correct_schema_rate": correct_schema_count / max(total_turns, 1),
        "mean_latency_ms": sum(latencies) / max(len(latencies), 1),
        "p50_latency_ms": sorted(latencies)[len(latencies) // 2] if latencies else 0,
        "p95_latency_ms": sorted(latencies)[int(len(latencies) * 0.95)] if latencies else 0,
    }


def main():
    parser = argparse.ArgumentParser(description="Evaluate a fine-tuned witness model")
    parser.add_argument("--checkpoint", type=str, required=True,
                        help="Path to model checkpoint")
    parser.add_argument("--eval-set", type=str, default=None,
                        help="Path to eval JSONL (optional, runs eval set evaluation)")
    parser.add_argument("--max-eval", type=int, default=50,
                        help="Max eval examples to process")
    parser.add_argument("--max-tokens", type=int, default=150,
                        help="Max new tokens to generate")
    parser.add_argument("--output", type=str, default=None,
                        help="Output file for results JSON")
    args = parser.parse_args()

    system_prompt = """You are a Witness agent. You respond ONLY with JSON tool calls.

For each turn, output exactly one JSON object:
{"tool": "<tool_name>", "args": {<arguments>}}

If no action is needed, output:
{"tool": "none", "args": {}}

Available tools: gt_polecat_list, gt_polecat_nuke, gt_peek, gt_session_status, gt_nudge, gt_mail_inbox, gt_mail_read, gt_mail_send, gt_patrol_report, gt_handoff, gt_escalate, bd_show, bd_list, bd_close, bd_children, check_git_state, check_tmux_session, bash"""

    model, tokenizer = load_model(args.checkpoint)

    # Scenario evaluation (always run)
    scenario_results = evaluate_scenarios(model, tokenizer, system_prompt)

    # Eval set evaluation (optional)
    eval_set_results = None
    if args.eval_set:
        eval_set_results = evaluate_eval_set(
            model, tokenizer, args.eval_set, system_prompt,
            max_examples=args.max_eval
        )

    # Summary
    print(f"\n{'='*70}")
    print("SUMMARY")
    print(f"{'='*70}")

    # Scenario summary
    n_scenarios = len(scenario_results)
    n_valid = sum(1 for r in scenario_results if r["valid_json"])
    n_correct = sum(1 for r in scenario_results if r["correct_tool"])
    avg_lat = sum(r["latency_ms"] for r in scenario_results) / n_scenarios

    print(f"\nScenarios ({n_scenarios} total):")
    print(f"  JSON valid:    {n_valid}/{n_scenarios} ({n_valid/n_scenarios*100:.0f}%)")
    print(f"  Correct tool:  {n_correct}/{n_scenarios} ({n_correct/n_scenarios*100:.0f}%)")
    print(f"  Avg latency:   {avg_lat:.0f}ms")

    if eval_set_results:
        print(f"\nEval set ({eval_set_results['total_turns']} turns):")
        print(f"  JSON valid:    {eval_set_results['valid_json_rate']*100:.0f}%")
        print(f"  Valid tool:    {eval_set_results['valid_tool_rate']*100:.0f}%")
        print(f"  Schema ok:     {eval_set_results['correct_schema_rate']*100:.0f}%")
        print(f"  Latency p50:   {eval_set_results['p50_latency_ms']:.0f}ms")
        print(f"  Latency p95:   {eval_set_results['p95_latency_ms']:.0f}ms")

    # Save results
    output_path = args.output or os.path.join(
        os.path.dirname(args.checkpoint), "eval_results.json"
    )
    all_results = {
        "checkpoint": args.checkpoint,
        "scenario_summary": {
            "n_scenarios": n_scenarios,
            "valid_json_rate": n_valid / n_scenarios,
            "correct_tool_rate": n_correct / n_scenarios,
            "avg_latency_ms": avg_lat,
        },
        "scenarios": scenario_results,
    }
    if eval_set_results:
        all_results["eval_set"] = eval_set_results

    with open(output_path, "w") as f:
        json.dump(all_results, f, indent=2, default=str)
    print(f"\nResults saved to {output_path}")


if __name__ == "__main__":
    main()
