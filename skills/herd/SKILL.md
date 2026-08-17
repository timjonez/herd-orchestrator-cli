---
name: herd
description: Consume the herd pending-decision queue for a Herdr session. Use when the user mentions herd pending, herd watch, or asks what other agents need from them.
---

# herd

`herd` is a session watcher for Herdr. It is a daemon plus a queue, not an orchestration skill.

The existing `herdr` skill still owns layout, `agent start`, and 1:1 `prompt --wait`. This skill only teaches how to read and close the queue `herd` writes.

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
