# Security Policy

Jaga handles camera credentials and video of people's homes and workplaces. We take security reports seriously.

## Reporting a vulnerability

**Please do not open a public issue.** Use GitHub's private vulnerability reporting: go to the repository's **Security** tab and click **Report a vulnerability**.

Include:

- affected version (`jaga version`) and how Jaga is deployed (binary or Docker)
- steps to reproduce or a proof of concept
- the impact you expect (for example "unauthenticated access to live view")

What to expect:

- acknowledgement within 7 days
- a fix or mitigation plan for confirmed critical issues within 30 days. Jaga is maintained by volunteers, so this is best effort.
- credit in the advisory and release notes, unless you prefer to stay anonymous

## Supported versions

Until 1.0, only the latest release receives security fixes.

## Scope

In scope: the `jaga` binary, its web UI and API, the official Docker image, and how Jaga configures the go2rtc and FFmpeg processes it starts.

Out of scope: vulnerabilities in camera firmware, and in upstream FFmpeg or go2rtc themselves. Please report those upstream; we will still ship updated versions.

## Hardening guidance for users

- Do not port-forward Jaga to the internet. Use a VPN (Tailscale, WireGuard) or a reverse proxy with HTTPS.
- Keep `JAGA_SECRET_KEY` secret and backed up.
- Put cameras on a separate VLAN with no internet access when possible.
