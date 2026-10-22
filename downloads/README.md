# GoProBuddy downloads (v0.1.0)

Ready-to-run builds of GoProBuddy v0.1.0, identical to the files on the [v0.1.0 release](https://github.com/shariorfarhan07/GoProBuddy/releases/tag/v0.1.0). For newer versions, check the [Releases page](https://github.com/shariorfarhan07/GoProBuddy/releases).

| System | File | Size |
|---|---|---|
| Windows 10/11 (most PCs) | [GoProBuddy-windows-amd64.zip](GoProBuddy-windows-amd64.zip) | 4.8 MB |
| Windows on ARM (Snapdragon) | [GoProBuddy-windows-arm64.zip](GoProBuddy-windows-arm64.zip) | 4.3 MB |
| macOS 11+ (Intel and Apple Silicon) | [GoProBuddy-macos.zip](GoProBuddy-macos.zip) | 9.8 MB |
| Linux x86-64 | [GoProBuddy-linux-amd64.tar.gz](GoProBuddy-linux-amd64.tar.gz) | 4.9 MB |
| Linux ARM64 | [GoProBuddy-linux-arm64.tar.gz](GoProBuddy-linux-arm64.tar.gz) | 4.4 MB |

Click a file, then **Download raw file** (the download icon at the top right).

Install steps: see [Download](../README.md#download) in the main README. Camera setup: see [Connecting your camera](../README.md#connecting-your-camera).

## Check a download (optional)
`SHA256SUMS.txt` lists each file's SHA-256 checksum.

- Windows (PowerShell): `Get-FileHash GoProBuddy-windows-amd64.zip`
- macOS: `shasum -a 256 GoProBuddy-macos.zip`
- Linux: `sha256sum -c SHA256SUMS.txt`

The hash should match the line for that file.

> Windows is tested with a real camera. The macOS and Linux builds haven't been tried on real hardware yet. Please report how they work.
