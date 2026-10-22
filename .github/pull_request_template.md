## What and why
<!-- What does this change, and why is it needed? Link the issue: "Fixes #12". -->

## How I tested it
<!-- Commands you ran, and what you saw. -->
- Camera model and firmware: <!-- e.g. HERO9 Black, HD9.01.01.72 — or "no camera (unit tests only)" -->
- Operating system: <!-- e.g. Windows 11 23H2 -->

## Screenshots
<!-- For UI changes: before and after. Delete this section otherwise. -->

## Checklist
- [ ] `gofmt -l .` prints nothing, and `go vet ./...` and `go test ./internal/...` pass
- [ ] Tests added or updated for camera-protocol changes
- [ ] README / `docs/PROTOCOL.md` updated if users or the protocol are affected
- [ ] No secrets (Wi-Fi passwords, credentials) in logs, messages or screenshots
- [ ] Protocol work is based only on Open GoPro or my own camera, not on GoPro's apps or other proprietary software
- [ ] Platform-specific code compiles on every platform (Windows, macOS, Linux)
