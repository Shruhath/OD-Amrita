# Security policy

## Reporting a vulnerability

Please **do not open a public issue** for a security problem.

Report it privately through GitHub: open the repository's **Security** tab and choose
**Report a vulnerability**. Include what you found, how to reproduce it, and the impact
you expect. We aim to acknowledge reports within 7 days.

Please do not test against the hosted production deployment, access data that is not
yours, or degrade the service for others. Run the stack locally instead — see
[HOWTO.md](HOWTO.md).

## Scope

In scope: the code in this repository (`api/`, `web/`, `mobile/`, `contracts/`) and its
default configuration.

Out of scope: third-party services the project relies on (Supabase, Microsoft Entra ID,
hosting providers), and findings that need a compromised administrator account or
physical access to a device.

## Supported versions

Only the latest commit on the default branch receives security fixes.
