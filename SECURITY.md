# Security Policy

## Supported Versions

Security fixes are developed on the `main` branch. After this fork begins publishing
releases, only the latest release and `main` are supported. Older tags and artifacts
from the upstream `palemoky` project are not releases of this fork.

## Reporting a Vulnerability

Do not include exploit details, credentials, private data, or an unpatched proof of
concept in a public issue or pull request.

1. Check the repository Security page for a **Report a vulnerability** button and use
   it when available.
2. If private vulnerability reporting is not enabled, open a public issue containing
   no sensitive details and ask the maintainer for a private contact channel.
3. Include affected versions, impact, prerequisites, reproduction steps, and a
   minimal proof of concept only in the resulting private channel.

Maintainers should enable GitHub private vulnerability reporting before the first
public production release. Until that setting is enabled, the repository does not
claim to provide a confidential GitHub reporting form.

## Scope

Reports about authentication or reconnect credentials, WebSocket or HTTP request
handling, game-state integrity, Redis isolation, container images, release artifacts,
GitHub Actions, and dependency compromise are in scope.

Availability limits caused solely by documented single-instance ownership, missing
Redis high availability, or intentionally unsupported old clients are known
limitations unless they enable a separate security impact.

## Disclosure

Please allow maintainers to validate and remediate a report before public disclosure.
The project will credit reporters who request credit and will publish an advisory when
that can be done without increasing risk to users who have not yet upgraded.
