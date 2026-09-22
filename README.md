# herd

Thin session watcher for [Herdr](https://herdr.dev). It watches every live agent except itself, classifies attention events without an LLM, keeps a durable pending queue, and pages you when a human decision is needed. `herd new` is the one launch recipe: create a workspace, start Claude, submit a prompt, and return.

This is not a replacement for `herdr`, and it is not an agent babysitter. Waiting is not reasoning.

## Install

```bash
go install github.com/timjonez/herd-orchestrator-cli/cmd/herd@latest
# or from a checkout:
make install
```

Requires Go 1.25+ and a running Herdr server (0.8 / protocol 19).

## Commands

```text
herd watch [--session NAME] [--socket PATH] [--ignore TARGET]...
           [--notify] [--notify-settled] [--json]
herd status
herd pending [--all] [--json]
herd show <id>
herd ack <id>
herd dismiss <id>
herd notify --title TEXT [--body TEXT] [--sound request|done|none]
herd new [prompt...] [--label TEXT] [--cwd PATH] [--name AGENT]
         [--kind claude] [--manual] [--focus]
herd ls
herd down <workspace|name> [--force]
herd version
```

`new` creates a Herdr workspace, starts an agent in the root pane, and submits the prompt if given. It does not wait. Claude defaults to `--permission-mode auto`. `--manual` starts Claude with `--permission-mode manual`, because omitting the flag leaves Claude Code on its built-in auto default. `ls` and `down` only see workspaces `herd new` created. `down` refuses if that workspace still has open queue items unless `--force` is set.

`watch` is a blocking daemon. Status goes to stderr. `--json` writes one JSONL object per new or updated queue item on stdout.

`--notify` (default on) toasts `needs_decision` items through `herdr notification.show`. `--notify-settled` is opt-in.

## How it connects

Resolution order for the Herdr socket:

1. `--socket` / `HERDR_SOCKET_PATH`
2. `--session` / `HERDR_SESSION` → `~/.config/herdr/sessions/<name>/herdr.sock`
3. `~/.config/herdr/herdr.sock`

`HERDR_ENV=1` is not required. The watcher can run inside a pane or as a user process.

When `HERDR_PANE_ID` is set, that pane is ignored (the watcher does not queue itself). `--ignore` accepts a pane id or a live agent name.

## Queue

```
$XDG_DATA_HOME/herd/<session>/queue.json
$XDG_DATA_HOME/herd/<session>/spaces.json
# or ~/.local/share/herd/<session>/
```

Override with `--state-dir` or `HERD_STATE_DIR`. Session defaults to `HERDR_SESSION` or `default`.

Open items are not acked and not dismissed. The same pane and `state_change_seq` is not recorded twice. A later seq updates the open item and may notify again.

## Classification

Herdr `blocked` is a hint, not truth. Grok and Claude Code are screen-manifest agents: unusual prompts often show as `idle`. `idle` vs `done` is about whether the tab has been seen; CLI reads do not mark it seen.

| Kind | When | Notify by default |
|---|---|---|
| `needs_decision` | Herdr `blocked`, or the screen looks like a question | yes |
| `settled` | `done`, or idle after work with no question markers | no |
| `unknown_idle` | `unknown` or unreadable | no |

`watch` never sends input to an agent. `new` submits one prompt and returns.

## Typical use

```bash
# in a spare pane or a user systemd unit
herd watch --json

# launch a working Claude and leave
herd new --label fix-login "fix the login redirect"
herd ls
herd down fix-login

# from another pane, or from an agent skill
herd pending --json
herd show 1
herd ack 1
```

## Development

```bash
make test
make build   # ./bin/herd
```
