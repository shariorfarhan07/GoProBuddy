# Contributing to GoProBuddy

Thanks for helping! Bug reports, camera test results, fixes and features are all welcome.

## Reporting bugs and testing cameras
- Open an [issue](https://github.com/shariorfarhan07/GoProBuddy/issues/new/choose) and choose **Bug report**. Please attach a diagnostic log: see [Debugging](README.md#debugging).
- Tried GoProBuddy on a camera or operating system the README lists as untested? Please tell us, whether it worked or not. That's one of the most useful contributions right now.

## Changing the code
1. Fork the repo and create a branch from `main`.
2. Make your change. Keep each pull request to one fix or feature.
3. Check it:
   ```sh
   gofmt -l .            # should print nothing
   go vet ./...
   go test ./internal/...
   ```
4. If you can, try it with a real camera, and say which model and firmware in the pull request.
5. Open a pull request that says what changed and why. GitHub Actions builds it for Windows, macOS and Linux.

See [Building from source](README.md#building-from-source) for setup, [Project layout](README.md#project-layout) for where things live, and [docs/PROTOCOL.md](docs/PROTOCOL.md) for how the camera protocol works.

## Ground rules
- **Only public sources.** Base camera-protocol work on GoPro's public [Open GoPro](https://gopro.github.io/OpenGoPro/) documentation or on what you observe from your own camera. Don't contribute code or notes taken from GoPro's apps or other proprietary software.
- **No secrets in logs.** Never log Wi-Fi passwords or other credentials. Use `diag.Redact`.
- **Match the style.** Follow the surrounding code: plain Go, short comments that explain *why*, and user-facing messages in plain language.
- Be kind and constructive in issues and reviews.

By contributing, you agree that your contributions are licensed under the [MIT License](LICENSE).
