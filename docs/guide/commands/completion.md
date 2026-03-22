# Completion

Generate shell completion scripts for bash and zsh.

```bash
sandal completion -shell bash
sandal completion -shell zsh
```

## Flags

### `-shell string`

:   shell type: `bash` or `zsh`. If omitted, auto-detected from `$SHELL`.

### `-help bool`

:   show help message

## Setup

### Bash

Add to your `~/.bashrc`:

```bash
eval "$(sandal completion -shell bash)"
```

### Zsh

Add to your `~/.zshrc`:

```bash
eval "$(sandal completion -shell zsh)"
```

!!! note
    Use `eval "$(...)"` instead of `source <(...)` if your system does not have `/dev/fd` available (common in minimal containers).

## Features

- Tab-completes all sandal subcommands and their flags
- Completes container names for `stop`, `kill`, `exec`, `attach`, `rm`, `rerun`, `inspect`, `cmd`, `snapshot`, and `export`
- Completes file paths and directories where applicable
- Completes VM subcommands and their flags
