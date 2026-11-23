# Deploy OpenAgentInbox from a harness

Use the release installer on the computer where the agent runs. Pick `local` for a background process owned by the current user. Pick `ssh` to install a Linux system service on an authorized server. A new deployment requires the human owner to claim the workspace once.

## Local

```sh
installer=./openagentinbox-install-0.7.0.sh
bash "$installer" agent plan --target local
bash "$installer" agent apply --target local
bash "$installer" agent status --target local
```

Apply exits after the running daemon answers a database-backed health check. Repeating it for the same running version, data directory and port returns `changed: false`. Set `--port 4311` on each command to use a different port. For a separate installation, use dedicated absolute paths in `INBOX_DATA`, `INBOX_INSTALL_DIR` and `INBOX_BIN_DIR`. The first successful apply includes the daemon PID and log path. The process survives closing the terminal. Run apply after a reboot. For upgrades, stop the existing process after backing up its data, then apply the new installer.

## Remote server over SSH

Use an SSH config alias or `user@host`. Configure the host key and noninteractive SSH login on the controller. A non-root remote user needs noninteractive sudo for installation. Use an actual DNS name pointing to the VPS and make ports 80 and 443 reachable for managed HTTPS.

```sh
installer=./openagentinbox-install-0.7.0.sh
bash "$installer" agent plan --target ssh --host inbox-prod --domain inbox.example.org
bash "$installer" agent apply --target ssh --host inbox-prod --domain inbox.example.org
bash "$installer" agent status --target ssh --host inbox-prod --domain inbox.example.org
```

Plan describes requirements without contacting the server. Apply transfers the installer, verifies its checksum and invokes the native systemd installation on the target. The package selects the target architecture. The system service starts at boot. Add `--existing-proxy` to manage HTTPS yourself and forward requests to 127.0.0.1:4310. To install from a shell already on the server, use `sudo bash "$installer" agent apply --target vps --domain inbox.example.org`. SSH status uses the installed daemon and needs no sudo or installer upload.

## What the harness does with the result

Read the JSON object on stdout. Installation logs go to stderr. The schema is `openagentinbox.deploy/v1`. Run `bash "$installer" agent help` for the machine-readable command list.

| Exit | Status | Action |
|---|---|---|
| 0 | planned | Execute apply when authorized. |
| 0 | ready | Use the origin, discovery URL and MCP URL. |
| 2 | not_ready | Follow the next field, then run status again. |
| 1 | error | Inspect the error and stderr. Correct the cause before retrying. |

For SSH, ready means the target service is active and the HTTPS URL from both the host and controller reaches the same running instance. A functioning loopback service with pending DNS or certificates returns not_ready. Retry status while routing settles.

If `next` is `owner_setup`, give the owner the URL and the path in `setup_code_file` on the target machine. The owner reads the code there and creates the account. Keep the code out of transcripts. When status reports `enroll_agent`, follow the [agent enrollment guide](../web/public/agent-guide.md). Deployment alone grants no mailbox credential.
