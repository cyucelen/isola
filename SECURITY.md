# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 0.x.x   | :white_check_mark: |

## Reporting a Vulnerability

If you discover a security vulnerability in isola, please report it responsibly:

1. **Do NOT open a public GitHub issue**
2. Send an email to the maintainers (create a private security advisory on GitHub)
3. Include:
   - Description of the vulnerability
   - Steps to reproduce
   - Potential impact
   - Suggested fix (if any)

We will respond within 48 hours and work with you to understand and address the issue.

## Security Considerations

isola manages processes and network ports on your local machine. Please be aware:

- **Process Management**: isola spawns child processes based on commands in `.isola.toml`. Only run commands you trust.
- **Port Binding**: Services bind to `127.0.0.1` (localhost) by default. They are not exposed to external networks unless explicitly configured.
- **State Files**: Runtime state is stored in `~/.isola/` with user-only permissions (0600).
- **No Remote Code Execution**: isola does not fetch or execute remote code.

## Best Practices

- Review `.isola.toml` before running `isola up` in untrusted repositories
- Keep isola updated to the latest version
- Do not commit sensitive credentials in `.isola.toml` - use environment variables instead
