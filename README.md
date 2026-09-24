# SSH MCP Server

[![License](https://img.shields.io/github/license/tufantunc/ssh-mcp)](./LICENSE)
[![GitHub Stars](https://img.shields.io/github/stars/tufantunc/ssh-mcp?style=social)](https://github.com/tufantunc/ssh-mcp/stargazers)
[![GitHub Forks](https://img.shields.io/github/forks/tufantunc/ssh-mcp?style=social)](https://github.com/tufantunc/ssh-mcp/forks)
[![Build Status](https://github.com/tufantunc/ssh-mcp/actions/workflows/publish.yml/badge.svg)](https://github.com/tufantunc/ssh-mcp/actions)
[![GitHub issues](https://img.shields.io/github/issues/tufantunc/ssh-mcp)](https://github.com/tufantunc/ssh-mcp/issues)

[![Trust Score](https://archestra.ai/mcp-catalog/api/badge/quality/tufantunc/ssh-mcp)](https://archestra.ai/mcp-catalog/tufantunc__ssh-mcp)

**SSH MCP Server** is a local Model Context Protocol (MCP) server that exposes SSH control for Linux and Windows systems, enabling LLMs and other MCP clients to execute shell commands securely via SSH.

## Contents

- [Quick Start](#quick-start)
- [Features](#features)
- [Install](#install)
- [MCP Usage](#mcp-usage-single-host-unchanged-flags)
- [Multi-Server + Web Config](#multi-server--web-config)
- [Client Setup](#client-setup)
- [Disclaimer](#disclaimer)
- [Support](#support)

## Quick Start

- [Install](#install) SSH MCP Server
- [Configure](#client-setup) your MCP Client (e.g. Claude Desktop, Cursor, etc)
- Execute remote shell commands on your Linux or Windows server via natural language

## Features

- MCP-compliant server exposing SSH capabilities
- Execute shell commands on remote Linux and Windows systems
- Secure authentication via password or SSH key
- Single Go binary, no runtime dependencies
- Multi-server support with a local web config UI, secrets encrypted at rest
- **Configurable timeout protection** with automatic process abortion
- **Graceful timeout handling** - attempts to kill hanging processes before closing connections

### Tools

The parameters and flags below are for `--host` mode. Through a saved connection via the hub — the default when `--host` is omitted — `exec`/`sudo-exec` take a `timeoutSec` instead (1–600s, default 60), `server` has no default (pass the exact name), `description` is shown to the approver and audited but never executed, and `list-servers` only lists servers marked "Visible to AI". See [Using saved servers from an AI client](#using-saved-servers-from-an-ai-client).

Both `exec` and `sudo-exec` return their result as `exit code: N`, followed by a `stdout:` section and a `stderr:` section (each section omitted if empty).

- `exec`: Execute a shell command on the remote server
  - **Parameters:**
    - `server` (optional): Name of a saved connection (see [Multi-Server + Web Config](#multi-server--web-config)); empty uses the default server
    - `command` (required): Shell command to execute on the remote SSH server
    - `description` (optional): Optional description of what this command will do (appended as a comment)
  - **Timeout Configuration:**

- `sudo-exec`: Execute a shell command with sudo elevation
  - **Parameters:**
    - `server` (optional): Name of a saved connection; empty uses the default server
    - `command` (required): Shell command to execute as root using sudo
    - `description` (optional): Optional description of what this command will do (appended as a comment)
  - **Notes:**
    - Requires `--sudoPassword` to be set for password-protected sudo
    - Can be disabled by passing the `--disableSudo` flag at startup if sudo access is not needed or not available
    - For persistent root access, consider using `--suPassword` instead which establishes a root shell
    - Tool will not be available at all if server is started with `--disableSudo`
  - **Timeout Configuration:**
    - Timeout is configured via command line argument `--timeout` (in milliseconds)
    - Default timeout: 60000ms (1 minute)
    - When a command times out, the server automatically attempts to abort the running process before closing the connection
  - **Max Command Length Configuration:**
    - Max command characters are configured via `--maxChars`
    - Default: `1000`
    - No-limit mode: set `--maxChars=none` or any `<= 0` value (e.g. `--maxChars=0`)

- `list-servers`: List configured SSH connection names (no secrets). Useful for discovering which `server` values are available when running in multi-server mode.

## Install

    go install github.com/lang315/ssh-mcp/cmd/ssh-mcp@latest

This installs the `ssh-mcp` binary to `$(go env GOPATH)/bin` (make sure that directory is on your `PATH`).

## MCP usage (single host, unchanged flags)

    ssh-mcp --host=1.2.3.4 --user=root --password=secret

**Required Parameters:**
- `host`: Hostname or IP of the Linux or Windows server
- `user`: SSH username

**Optional Parameters:**
- `port`: SSH port (default: 22)
- `password`: SSH password (or use `key` for key-based auth)
- `key`: Path to private SSH key
- `sudoPassword`: Password for sudo elevation (when executing commands with sudo)
- `suPassword`: Password for su elevation (when you need a persistent root shell)
- `timeout`: Command execution timeout in milliseconds (default: 60000ms = 1 minute)
- `maxChars`: Maximum allowed characters for the `command` input (default: 1000). Use `none` or `0` to disable the limit.
- `disableSudo`: Flag to disable the `sudo-exec` tool completely. Useful when sudo access is not needed or not available.
- `insecureIgnoreHostKey`: Flag to skip SSH host key verification. Not recommended outside of trusted/throwaway environments.

## Multi-server + web config

    ssh-mcp web        # opens config UI on http://127.0.0.1:8422
    # then reference a saved connection by name via the `server` tool argument

The web UI lets you add, edit, import, and export SSH connections without passing `--host`/`--password` on every launch. Saved connections (and their secrets) are stored at `~/.config/ssh-mcp/servers.json`, **encrypted at rest**.

Saved connections are only reachable through a separate `ssh-mcp hub` process, which gates every AI-issued command behind your approval; `--host` mode never touches this store at all.

### Using saved servers from an AI client

Start the hub and keep it running:

    ssh-mcp hub --cli          # terminal approver; a desktop app replaces this later
    # Commands: a [id]=allow  d [id] [reason]=deny  D [reason]=deny all
    #           s [id]=send to tab (prints the command for you to paste)
    #           u=unlock  p=list pending  q=quit
    # (the id is only needed when 2+ requests are pending)

Then register the bridge with your MCP client, with no flags:

    claude mcp add --transport stdio ssh-mcp -- ssh-mcp

With the hub closed, every tool call fails with "Open the app to approve commands". There is no headless mode for the vault.

The AI only sees servers with "Visible to AI" checked in `ssh-mcp web` (off by default), and only once a host key is pinned for them — the hub refuses an AI-visible server that has no pin rather than learning one on the fly. Nothing in this release drives that first connection for you from the CLI (that lands with terminal tabs in the desktop app); paste the fingerprint yourself into the "Host key fingerprint" field in `ssh-mcp web` instead.

**Paste only the `SHA256:...` token** — nothing else. The pin is compared by exact string equality against `ssh.FingerprintSHA256(key)`, so it must be exactly `SHA256:<base64>`: no leading key-size number, no trailing hostname or `(ED25519)` key-type suffix, no extra whitespace. `ssh-keygen -lf -` prints a whole line like `256 SHA256:xxxx host (ED25519)`; pasting that whole line causes a permanent host key mismatch. Print just the token, for the key type the server actually presents (OpenSSH clients prefer ED25519; if unsure, connect once with a plain `ssh` client and read the fingerprint it prints):

    ssh-keyscan -p PORT -t ed25519 HOST 2>/dev/null | ssh-keygen -lf - | awk '{print $2}'

Through the hub, `exec`/`sudo-exec` take a `timeoutSec` (1–600, default 60) instead of `--timeout`, `server` has no default (pass the exact name from `list-servers`), and `list-servers` lists only AI-visible servers, marking a locked one `[locked: unlock the app]`.

## Client Setup

You can configure your IDE or LLM like Cursor, Windsurf, Claude Desktop to use this MCP Server.

```commandline
{
    "mcpServers": {
        "ssh-mcp": {
            "command": "ssh-mcp",
            "args": [
                "--host=1.2.3.4",
                "--port=22",
                "--user=root",
                "--password=pass",
                "--key=path/to/key",
                "--timeout=30000",
                "--maxChars=none"
            ]
        }
    }
}
```

### Claude Code

You can add this MCP server to Claude Code using the `claude mcp add` command. This is the recommended method for Claude Code.

**Basic Installation:**

```bash
claude mcp add --transport stdio ssh-mcp -- ssh-mcp --host=YOUR_HOST --user=YOUR_USER --password=YOUR_PASSWORD
```

**Installation Examples:**

**With Password Authentication:**
```bash
claude mcp add --transport stdio ssh-mcp -- ssh-mcp --host=192.168.1.100 --port=22 --user=admin --password=your_password
```

**With SSH Key Authentication:**
```bash
claude mcp add --transport stdio ssh-mcp -- ssh-mcp --host=example.com --user=root --key=/path/to/private/key
```

**With Custom Timeout and No Character Limit:**
```bash
claude mcp add --transport stdio ssh-mcp -- ssh-mcp --host=192.168.1.100 --user=admin --password=your_password --timeout=120000 --maxChars=none
```

**With Sudo and Su Support:**
```bash
claude mcp add --transport stdio ssh-mcp -- ssh-mcp --host=192.168.1.100 --user=admin --password=your_password --sudoPassword=sudo_pass --suPassword=root_pass
```

**With Saved Servers (through the hub):**
```bash
claude mcp add --transport stdio ssh-mcp -- ssh-mcp
```
(run `ssh-mcp hub --cli` separately to approve commands, and enable "Visible to AI" on the servers you want reachable — see [Using saved servers from an AI client](#using-saved-servers-from-an-ai-client))

**Installation Scopes:**

You can specify the scope when adding the server:

- **Local scope** (default): For personal use in the current project
  ```bash
  claude mcp add --transport stdio ssh-mcp --scope local -- ssh-mcp --host=YOUR_HOST --user=YOUR_USER --password=YOUR_PASSWORD
  ```

- **Project scope**: Share with your team via `.mcp.json` file
  ```bash
  claude mcp add --transport stdio ssh-mcp --scope project -- ssh-mcp --host=YOUR_HOST --user=YOUR_USER --password=YOUR_PASSWORD
  ```

- **User scope**: Available across all your projects
  ```bash
  claude mcp add --transport stdio ssh-mcp --scope user -- ssh-mcp --host=YOUR_HOST --user=YOUR_USER --password=YOUR_PASSWORD
  ```


**Verify Installation:**

After adding the server, restart Claude Code and ask Claude to execute a command:
```
"Can you run 'ls -la' on the remote server?"
```

For more information about MCP in Claude Code, see the [official documentation](https://docs.claude.com/en/docs/claude-code/mcp).

## Disclaimer

SSH MCP Server is provided under the [MIT License](./LICENSE). Use at your own risk. This project is not affiliated with or endorsed by any SSH or MCP provider.

## Contributing

We welcome contributions! Please see our [Contributing Guidelines](./CONTRIBUTING.md) for more information.

## Code of Conduct

This project follows a [Code of Conduct](./CODE_OF_CONDUCT.md) to ensure a welcoming environment for everyone.

## Support

If you find SSH MCP Server helpful, consider starring the repository or contributing! Pull requests and feedback are welcome. 