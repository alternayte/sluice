# Security policy

## Report a vulnerability

Do not open a public issue for a vulnerability.

1. Open the repository [alternayte/sluice](https://github.com/alternayte/sluice) on GitHub.
2. Open the **Security** tab.
3. Click **Report a vulnerability**.
4. Give the affected version or commit, the steps to reproduce and the effect.

GitHub keeps the report private between you and the maintainers. The maintainers publish a security advisory when a fix is available.

## Supported versions

Sluice has no release yet. Only the latest commit on `main` gets security fixes.

| Version | Supported |
|---|---|
| Latest commit on `main` | Yes |
| Older commits | No |

## Security design

The SDD §8 security invariants define the main controls:

- Secret values are never stored or sent in plaintext outside the task process. Builtin secrets are encrypted with AES-256-GCM. Logs, outputs and AI requests mask secret values.
- Passwords use argon2id. Session IDs, API tokens, run tokens and webhook keys are stored only as SHA-256 hashes.
- Every route declares its access. The server does not start when a route has no access.
- A run token is valid for one task run only. It expires, and the server revokes it at the end of the task.
- Webhook keys and git webhook signatures are compared in constant time.
- Cookie-authenticated unsafe requests must come from the same origin.
- File APIs, git sync and bundle extraction reject absolute paths, `..` segments and symlinks.
- Login failures are rate limited per email and per IP address.
- The assistant runs a tool call that changes data only after the user confirms it. The audit log records all AI actions that change data.
- Responses set a strict `Content-Security-Policy` and other security headers.

For details, read [docs/operations/security.md](docs/operations/security.md) and SDD §8 in [docs/sluice-sdd.md](docs/sluice-sdd.md).
