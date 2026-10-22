# Contributing to GoProBuddy

Thanks for helping make GoProBuddy better! You don't need to write code to contribute: testing it on your camera is one of the most useful things you can do right now.

**Contents**
- [Ways to contribute](#ways-to-contribute)
- [Ground rules](#ground-rules)
- [Reporting a bug](#reporting-a-bug)
- [Testing on your camera](#testing-on-your-camera)
- [Suggesting a feature](#suggesting-a-feature)
- [Setting up for development](#setting-up-for-development)
- [How the code is organised](#how-the-code-is-organised)
- [Making a change](#making-a-change)
- [Coding guidelines](#coding-guidelines)
- [Testing your change](#testing-your-change)
- [Commit messages](#commit-messages)
- [Opening a pull request](#opening-a-pull-request)
- [Review and merging](#review-and-merging)
- [Documentation](#documentation)
- [Releases](#releases)
- [Security issues](#security-issues)
- [License](#license)

## Ways to contribute

| You have… | You can… |
|---|---|
| 5 minutes and a GoPro | [File a camera compatibility report](https://github.com/shariorfarhan07/GoProBuddy/issues/new?template=camera_report.yml) |
| A Mac or a Linux PC | Try the preview build and report what works and what breaks |
| Found a problem | [Report a bug](#reporting-a-bug) with a diagnostic log |
| An idea | [Suggest a feature](#suggesting-a-feature) |
| Go experience | Pick an issue labelled [`good first issue`](https://github.com/shariorfarhan07/GoProBuddy/labels/good%20first%20issue) or [`help wanted`](https://github.com/shariorfarhan07/GoProBuddy/labels/help%20wanted) |
| Writing skills | Improve the README, docs or error messages |

## Ground rules

1. **Be kind and constructive.** Everyone here is a volunteer. See the [Code of Conduct](CODE_OF_CONDUCT.md).
2. **Only public sources for camera protocol work.** Base protocol code and notes on GoPro's public [Open GoPro](https://gopro.github.io/OpenGoPro/) documentation, or on what you observe from your own camera. **Never** contribute code, notes or data taken from GoPro's apps, firmware dumps or any other proprietary software. Pull requests that do will be closed.
3. **Never log or expose secrets.** No Wi-Fi passwords or other credentials in logs, error messages, issues or screenshots. Use `diag.Redact` in logs.
4. **One change per pull request.** Small, focused pull requests get reviewed and merged much faster.
5. **Talk before big changes.** For a new feature or a large refactor, open an issue first so we can agree on the approach before you spend time on it.
6. **Not an official GoPro project.** Don't use GoPro logos, and use the GoPro name only to describe compatibility ("works with HERO9").

## Reporting a bug

1. **Search [existing issues](https://github.com/shariorfarhan07/GoProBuddy/issues)** first. If yours is already there, add a 👍 or a comment with your details.
2. **Record a diagnostic log:** open **Activity** → **Start diagnostic log**, reproduce the problem, then click **Open log folder**.
3. **[Open a bug report](https://github.com/shariorfarhan07/GoProBuddy/issues/new?template=bug_report.yml)** and fill in every field: what happened, steps to reproduce, GoProBuddy version, camera model and firmware, operating system, how you connected, and the log file.

A good bug report says what you expected, what happened instead, and the exact steps that make it happen again.

## Testing on your camera

GoProBuddy has only been tested on a **HERO9 Black** with **Windows 11** so far. Every report on another camera or system helps, even "it all worked".

1. Download the latest build from [Releases](https://github.com/shariorfarhan07/GoProBuddy/releases).
2. Try each feature: connect (Bluetooth, Wi-Fi by IP, USB-C), browse media, download a few files, live preview, webcam, settings.
3. **[File a camera compatibility report](https://github.com/shariorfarhan07/GoProBuddy/issues/new?template=camera_report.yml)** with your camera model, firmware version (*Preferences → About*), operating system and results. Attach a diagnostic log if anything failed.

## Suggesting a feature

[Open a feature request](https://github.com/shariorfarhan07/GoProBuddy/issues/new?template=feature_request.yml). Describe the **problem** you want solved and how you'd use the feature, not only the solution. Ideas that help many GoPro owners and fit a simple, local, privacy-friendly app are the most likely to be built.

## Setting up for development

You need **Go 1.27 or newer** (see `go.mod`) and Git.

```sh
git clone https://github.com/<your-username>/GoProBuddy.git
cd GoProBuddy
go test ./internal/...
```

Then build and run for your system:

| System | Extra requirements | Build |
|---|---|---|
| **Windows** | None (no C compiler needed) | `go build -o GoProBuddy.exe .` |
| **macOS** | Xcode command-line tools: `xcode-select --install` | `go build -o GoProBuddy .` (to use Bluetooth, run it as an app bundle; see the `macos` job in [`.github/workflows/build.yml`](.github/workflows/build.yml)) |
| **Linux** | `sudo apt install gcc pkg-config libwayland-dev libx11-dev libx11-xcb-dev libxkbcommon-x11-dev libgles2-mesa-dev libegl1-mesa-dev libffi-dev libxcursor-dev libvulkan-dev` (Debian/Ubuntu) | `go build -o goprobuddy .` |

For live preview and webcam, install **FFmpeg** (`winget install Gyan.FFmpeg`, `brew install ffmpeg`, or your package manager).

**No camera?** Most of the camera code is covered by unit tests against a fake camera (`internal/gopro/fake_camera_test.go`), so you can work on many issues without hardware. Say in your pull request if you couldn't test on a real camera.

**Debugging Bluetooth:** the small tools in `cmd/` help: `go run ./cmd/blescan` lists nearby Bluetooth devices, and `go run ./cmd/bleconnect` runs the connect steps one by one.

## How the code is organised

```
main.go                     The whole UI (Gio): pages, sidebar, media grid, footer
open_*.go                   Opening folders on each operating system
internal/gopro/             Everything that talks to the camera
  client.go                 HTTP API: status, media list, thumbnails, shutter, turbo
  download.go               Resumable downloads
  preview.go                Live preview (UDP MPEG-TS → ffmpeg → frames)
  webcam.go                 Webcam mode and USB-C detection
  settings.go               Camera settings and their availability filters
  ble.go, pair_*.go         Bluetooth scan, pairing and control
  wifi*.go                  Joining the camera's Wi-Fi on each OS
internal/diag/              The optional diagnostic log
docs/PROTOCOL.md            How the camera protocol works, and where each fact comes from
tools/winres/               Builds the Windows icon resource
packaging/                  macOS and Linux packaging files
```

Platform-specific code uses Go file suffixes (`_windows.go`, `_darwin.go`, `_linux.go`) or build tags. If you add a platform-specific function, provide it for **every** platform, even as a clear "not supported" error, so all builds keep compiling.

## Making a change

The `main` branch is protected: changes reach it only through a pull request that passes the build and gets an approving review.

1. **Fork** the repository and clone your fork.
2. **Create a branch** from `main` with a short descriptive name: `fix/download-resume`, `feat/preset-switching`, `docs/linux-install`.
3. **Make your change**, keeping it focused on one thing.
4. **Run the checks** in [Testing your change](#testing-your-change).
5. **Push** your branch and **open a pull request** against `main`.

Keep your branch up to date with `main` (`git pull --rebase upstream main`). The build must pass on the latest `main` before merging.

## Coding guidelines

- **Format with `gofmt`.** Code that isn't formatted fails review.
- **Match the surrounding code:** plain, standard Go; small functions; names that say what things are.
- **Comments explain *why*,** not what. Keep them short. Cite the source of protocol facts, for example `// HERO9 ignores the port parameter and always sends to 8554 (docs/PROTOCOL.md §6)`.
- **Errors:** wrap with context (`fmt.Errorf("download %s: %w", name, err)`) and return them; don't panic.
- **User-facing messages:** plain language that says what happened and what to do next ("Camera not reachable. Make sure this PC is joined to the camera's Wi-Fi."). No jargon, no blame.
- **Diagnostic logging:** add `diag.Logf("<area>", …)` lines for steps that could fail in the field, using the existing area tags (`http`, `ble`, `wifi`, `download`, `stream`, `settings`…). Never log secrets.
- **UI work:** follow the existing colours (`col*` variables in `main.go`), spacing and components (`panel`, `button`, `ghost`, `pill`). Include before/after screenshots in your pull request.
- **Dependencies:** avoid adding new modules. If one is really needed, explain why in the pull request. It must have a licence compatible with MIT.
- **Privacy:** GoProBuddy has no telemetry and talks only to the camera. Don't add network calls to anything else.

## Testing your change

Run these before opening a pull request. GitHub Actions runs the same checks:

```sh
gofmt -l .               # must print nothing
go vet ./...
go test ./internal/...
```

- **Add or update tests** for camera-protocol changes. Use the fake camera in `internal/gopro/fake_camera_test.go`: `fc.handle("/path", "response body")` serves a canned response.
- **Test on a real camera if you can,** and write the model, firmware and operating system in the pull request.
- **UI changes:** run the app and check the pages you touched, both with and without a camera connected.
- **Check other platforms compile** if you touched platform code: `GOOS=linux go vet ./internal/...` checks the camera code for Linux from any OS. The full macOS and Linux builds run in CI.

## Commit messages

- First line: a short summary in the imperative, under about 70 characters: `Fix resume when the camera ignores Range`.
- Then a blank line, then *why* the change was needed if it isn't obvious.
- Reference issues with `Fixes #12` so they close automatically when merged.

## Opening a pull request

Fill in the pull request template:

- **What** changed and **why** (link the issue).
- **How you tested** it: commands run, camera model and firmware, operating system.
- **Screenshots** for UI changes.
- Tick the checklist.

Draft pull requests are welcome for early feedback: open one and mark it as a draft.

## Review and merging

- A maintainer reviews every pull request, usually within a few days. Feel free to comment on it if it's been a week.
- Merging needs **one approving review** and **all build checks passing**. New commits after an approval need a fresh review.
- Review comments are about the code, not about you. Reply to each one, or push a fix and resolve it.
- All review conversations must be resolved before merging.
- Maintainers may squash commits when merging.

## Documentation

- Update the **README** when you change something users see: a feature, an install step or a limitation.
- Update **`docs/PROTOCOL.md`** when you learn something new about the camera protocol, marked **[OGP]** (from Open GoPro) or **[model name]** (measured on your camera).
- Keep the writing plain and short, like the rest of the docs.

## Releases

Maintainers make releases: update the changelog, tag `vX.Y.Z` on `main`, and push the tag. GitHub Actions then builds Windows, macOS and Linux packages and publishes them on the Releases page. Contributors are credited in the release notes.

## Security issues

**Don't open a public issue for security problems.** Report them privately as described in [SECURITY.md](SECURITY.md).

## License

GoProBuddy is MIT licensed. By contributing, you agree that your contributions are licensed under the [MIT License](LICENSE), and that you have the right to contribute them.

---

Questions about contributing? Open an issue, or ask in your pull request. Thank you for helping!
