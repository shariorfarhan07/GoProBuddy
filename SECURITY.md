# Security policy

## Reporting a vulnerability

Please **don't open a public issue** for security problems.

Report it privately through GitHub instead: go to the repository's **Security** tab and click **Report a vulnerability** ([direct link](https://github.com/shariorfarhan07/GoProBuddy/security/advisories/new)). Include what you found, how to reproduce it, and the impact you think it has.

You can expect a first reply within **7 days**. We'll keep you updated, agree on a fix and a disclosure date with you, and credit you in the release notes unless you'd rather stay anonymous.

## Supported versions

Security fixes go into the latest release. GoProBuddy is in preview, so please update to the newest version from [Releases](https://github.com/shariorfarhan07/GoProBuddy/releases).

## Scope

GoProBuddy talks to your camera over its own Wi-Fi hotspot, USB-C or Bluetooth, and writes files to the folder you choose. It has no accounts, no cloud and no telemetry. Things we'd like to hear about include:

- Secrets (such as the camera's Wi-Fi password) appearing in logs, files or the UI where they shouldn't
- The app connecting to anything other than the camera
- Files written outside the chosen save folder
- Problems in the downloaded build packages

The camera's own HTTP API has no authentication; that's GoPro's design on its private hotspot, not a GoProBuddy issue.
