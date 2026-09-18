---
name: herd
description: Consume the herd pending-decision queue for a Herdr session, or launch a workspace with herd new. Use when the user mentions herd pending, herd watch, herd new, or asks what other agents need from them.
---

# herd

`herd` is a session watcher for Herdr, plus one launch recipe. It is not a general orchestration skill.

The existing `herdr` skill still owns ad-hoc layout, splits, and 1:1 `prompt --wait`. Use `herd new` only when the user wants that launch recipe. This skill also teaches how to read and close the queue `herd` writes.

## Launch a workspace

```bash
herd new --label fix-login "fix the login redirect"
herd ls
herd down fix-login
```

`herd new` creates a Herdr workspace, starts Claude with `--permission-mode auto`, submits the prompt, and returns. It does not wait. Pass `--manual` to skip auto mode. `ls` lists every live Herdr workspace. `down` closes any of those.

## Do not implement the watch loop

- Do not `sleep` and poll `herdr agent list`.
- Do not start `herd watch` unless the user asked.
- Do not send “continue” or other input to keep an agent moving.

## Inspect the queue

```bash
herd pending --json
herd show <id>
```

`--json` is the machine-readable form. Human output is for the user.

Each item has `kind` (`needs_decision`, `settled`, `unknown_idle`), Herdr `herdr_status`, pane/agent identifiers, `state_change_seq`, and an `excerpt` of the screen.

## Treat `blocked` as a hint

Grok and Claude Code often present questions as `idle`. Read the excerpt (and `herd show`) before answering the user. If the excerpt is empty or unclear, say so; do not invent the question.

## Close work you handled

```bash
herd ack <id>        # you handled the decision
herd dismiss <id>    # it was noise
```

Do not ack an item you did not actually resolve.

## Connection

`herd` uses the same socket resolution as Herdr (`HERDR_SOCKET_PATH`, `HERDR_SESSION`, `--session`, `--socket`). Queue files live under `$XDG_DATA_HOME/herd/<session>/` unless `HERD_STATE_DIR` / `--state-dir` is set.
