#!/usr/bin/env python3
"""
Witness patrol serving shim — loads a fine-tuned SmolLM2-135M model and runs
patrol cycles: gather context → inference → execute tool call → backoff.

Replaces the Claude Code witness agent for patrol decisions.

Usage:
    python serve.py --checkpoint ./checkpoints/smollm2-135m_fmtb_full_ep3_800hardened_v2/
    python serve.py --checkpoint ./checkpoints/... --shadow        # observe only
    python serve.py --checkpoint ./checkpoints/... --once          # single cycle
    python serve.py --checkpoint ./checkpoints/... --interval 60   # fixed interval
"""

import argparse
import json
import logging
import re
import shlex
import subprocess
import sys
import time

import torch
from transformers import AutoTokenizer, AutoModelForCausalLM

SYSTEM_PROMPT = """You are a Witness agent. You respond ONLY with JSON tool calls.

For each turn, output exactly one JSON object:
{"tool": "<tool_name>", "args": {<arguments>}}

If no action is needed, output:
{"tool": "none", "args": {}}

Available tools: gt_polecat_list, gt_polecat_nuke, gt_peek, gt_session_status, gt_nudge, gt_mail_inbox, gt_mail_read, gt_mail_send, gt_patrol_report, gt_handoff, gt_escalate, bd_show, bd_list, bd_close, bd_children, check_git_state, check_tmux_session, bash"""

BACKOFF_MIN = 30
BACKOFF_MAX = 300

log = logging.getLogger("witness-shim")


# ---------------------------------------------------------------------------
# Model loading & inference (from evaluate.py)
# ---------------------------------------------------------------------------

def load_model(checkpoint_path: str):
    """Load model and tokenizer from checkpoint."""
    log.info("Loading model from %s", checkpoint_path)
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
    log.info("Loaded: %.1fM params", n_params / 1e6)
    return model, tokenizer


def model_decide(model, tokenizer, context: str) -> dict:
    """Run inference on patrol context, return parsed tool call dict."""
    messages = [
        {"role": "system", "content": SYSTEM_PROMPT},
        {"role": "user", "content": context},
    ]

    prompt = tokenizer.apply_chat_template(
        messages, tokenize=False, add_generation_prompt=True
    )
    inputs = tokenizer(prompt, return_tensors="pt")

    start = time.perf_counter()
    with torch.no_grad():
        out = model.generate(
            **inputs,
            max_new_tokens=100,
            do_sample=False,
            pad_token_id=tokenizer.pad_token_id,
        )
    latency_ms = (time.perf_counter() - start) * 1000

    generated = tokenizer.decode(
        out[0][inputs.input_ids.shape[1]:],
        skip_special_tokens=True,
    ).strip()

    log.debug("Raw output (%dms): %s", latency_ms, generated)

    parsed = parse_json_output(generated)
    if parsed is None:
        log.warning("Failed to parse JSON from model output: %s", generated)
        return {"tool": "none", "args": {}, "_raw": generated, "_latency_ms": latency_ms}

    parsed["_raw"] = generated
    parsed["_latency_ms"] = latency_ms
    return parsed


def parse_json_output(text: str) -> dict | None:
    """Try to extract a JSON object from model output."""
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        pass

    match = re.search(r'\{[^{}]*\}', text)
    if match:
        try:
            return json.loads(match.group())
        except json.JSONDecodeError:
            pass

    match = re.search(r'\{.*\}', text, re.DOTALL)
    if match:
        try:
            return json.loads(match.group())
        except json.JSONDecodeError:
            pass

    return None


# ---------------------------------------------------------------------------
# Patrol context gathering
# ---------------------------------------------------------------------------

def run_cmd(cmd: str, timeout: int = 15) -> str:
    """Run a shell command and return stdout (or error string)."""
    try:
        r = subprocess.run(
            cmd, shell=True, capture_output=True, text=True, timeout=timeout,
        )
        return r.stdout.strip() if r.returncode == 0 else f"[error] {r.stderr.strip()}"
    except subprocess.TimeoutExpired:
        return "[error] command timed out"


def gather_patrol_context() -> str:
    """Gather patrol context via gt CLI commands."""
    polecats = run_cmd("gt polecat list --all")
    inbox = run_cmd("gt mail inbox --unread")

    parts = ["Polecats", "", polecats or "No active polecats."]
    if inbox and inbox != "[error] command timed out":
        parts += ["", "Inbox", "", inbox]
    return "\n".join(parts)


# ---------------------------------------------------------------------------
# Tool execution
# ---------------------------------------------------------------------------

def execute_tool(decision: dict, shadow: bool = False) -> str:
    """Map a tool-call dict to a gt CLI command and execute it."""
    tool = decision.get("tool", "none")
    args = decision.get("args", {})

    cmd = _build_command(tool, args)
    if cmd is None:
        log.info("No-op: tool=%s", tool)
        return ""

    if shadow:
        log.info("[SHADOW] Would run: %s", cmd)
        return f"[shadow] {cmd}"

    log.info("Executing: %s", cmd)
    result = run_cmd(cmd, timeout=30)
    log.info("Result: %s", result[:200])
    return result


def _build_command(tool: str, args: dict) -> str | None:
    """Build a shell command string from tool name and args. Returns None for no-op."""
    if tool == "none":
        return None

    if tool == "gt_nudge":
        target = args.get("target", "")
        message = args.get("message", "")
        if not target:
            return None
        cmd = f"gt nudge {shlex.quote(target)}"
        if message:
            cmd += f" -m {shlex.quote(message)}"
        return cmd

    if tool == "gt_polecat_nuke":
        target = args.get("target", "")
        if not target:
            return None
        cmd = f"gt polecat nuke {shlex.quote(target)}"
        if args.get("force"):
            cmd += " --force"
        return cmd

    if tool == "gt_peek":
        target = args.get("target", "")
        if not target:
            return None
        cmd = f"gt peek {shlex.quote(target)}"
        if args.get("lines"):
            cmd += f" --lines {int(args['lines'])}"
        return cmd

    if tool == "gt_mail_inbox":
        return "gt mail inbox"

    if tool == "gt_mail_read":
        mail_id = args.get("mail_id", "")
        if not mail_id:
            return "gt mail inbox"
        return f"gt mail read {shlex.quote(str(mail_id))}"

    if tool == "gt_mail_send":
        recipient = args.get("recipient", "")
        subject = args.get("subject", "")
        body = args.get("body", "")
        if not recipient:
            return None
        cmd = f"gt mail send {shlex.quote(recipient)}"
        if subject:
            cmd += f" -s {shlex.quote(subject)}"
        if body:
            cmd += f" -m {shlex.quote(body)}"
        return cmd

    if tool == "gt_patrol_report":
        status = args.get("status", "ok")
        note = args.get("note", "")
        cmd = f"gt patrol report --summary {shlex.quote(status)}"
        if note:
            cmd += f" --note {shlex.quote(note)}"
        return cmd

    if tool == "check_tmux_session":
        session = args.get("session", "")
        if not session:
            return None
        return f"tmux has-session -t {shlex.quote(session)}"

    if tool == "gt_session_status":
        return "gt status --fast"

    if tool == "gt_polecat_list":
        return "gt polecat list"

    if tool == "gt_escalate":
        severity = args.get("severity", "HIGH")
        message = args.get("message", "")
        cmd = f"gt escalate -s {shlex.quote(severity)}"
        if message:
            cmd += f" {shlex.quote(message)}"
        return cmd

    if tool == "gt_handoff":
        target = args.get("target", "")
        if not target:
            return None
        return f"gt handoff {shlex.quote(target)}"

    if tool == "check_git_state":
        session = args.get("session", "")
        if session:
            return f"tmux send-keys -t {shlex.quote(session)} 'git status' Enter"
        return "git status"

    if tool == "bash":
        command = args.get("command", "")
        if not command:
            return None
        return command

    log.warning("Unknown tool: %s", tool)
    return None


# ---------------------------------------------------------------------------
# Main patrol loop
# ---------------------------------------------------------------------------

def patrol_loop(model, tokenizer, *, shadow: bool = False,
                once: bool = False, fixed_interval: int | None = None):
    """Run the patrol loop with exponential backoff."""
    interval = fixed_interval or BACKOFF_MIN
    cycle = 0

    log.info("Starting patrol loop (shadow=%s, once=%s, interval=%s)",
             shadow, once, fixed_interval or "adaptive")

    try:
        while True:
            cycle += 1
            ts = time.strftime("%H:%M:%S")

            # 1. Gather context
            context = gather_patrol_context()
            ctx_summary = context[:120].replace("\n", " ")
            log.info("[%s] cycle=%d context=%s", ts, cycle, ctx_summary)

            # 2. Inference
            decision = model_decide(model, tokenizer, context)
            tool = decision.get("tool", "none")
            latency = decision.get("_latency_ms", 0)
            log.info("[%s] decision: tool=%s latency=%.0fms", ts, tool, latency)

            # 3. Execute
            result = execute_tool(decision, shadow=shadow)
            if result:
                log.info("[%s] result: %s", ts, result[:200])

            # 4. Backoff
            if once:
                log.info("Single cycle complete, exiting.")
                break

            if fixed_interval:
                interval = fixed_interval
            elif tool != "none" and not shadow:
                interval = BACKOFF_MIN  # reset on real action only
            else:
                interval = min(interval * 2, BACKOFF_MAX)  # exponential backoff

            log.info("[%s] sleeping %ds", ts, interval)
            time.sleep(interval)

    except KeyboardInterrupt:
        log.info("Interrupted, shutting down.")


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def main():
    parser = argparse.ArgumentParser(
        description="Witness patrol shim — tiny model serving loop"
    )
    parser.add_argument("--checkpoint", required=True,
                        help="Path to fine-tuned model checkpoint")
    parser.add_argument("--shadow", action="store_true",
                        help="Shadow mode: log decisions but do not execute")
    parser.add_argument("--once", action="store_true",
                        help="Run a single patrol cycle then exit")
    parser.add_argument("--interval", type=int, default=None,
                        help="Fixed sleep interval in seconds (disables adaptive backoff)")
    parser.add_argument("--verbose", "-v", action="store_true",
                        help="Enable debug logging")
    args = parser.parse_args()

    logging.basicConfig(
        level=logging.DEBUG if args.verbose else logging.INFO,
        format="%(asctime)s %(name)s %(levelname)s %(message)s",
        datefmt="%Y-%m-%d %H:%M:%S",
    )

    model, tokenizer = load_model(args.checkpoint)
    patrol_loop(model, tokenizer, shadow=args.shadow,
                once=args.once, fixed_interval=args.interval)


if __name__ == "__main__":
    main()
