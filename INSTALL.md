# Installing Weloury

Weloury is distributed through GitHub Releases. Download an installer only from the official [Weloury Releases page](https://github.com/mh3nj/Weloury/releases).

## Windows

Weloury has Windows installer builds in both MSI and NSIS formats. When a release includes installer assets, choose the format that suits your setup and follow the Windows installer prompts.

The installer files are uploaded to releases separately by the project maintainer. If you do not see an MSI or NSIS asset attached to the release, that installer has not been published there yet.

Before installing:

1. Confirm that the release belongs to `mh3nj/Weloury`.
2. Download the installer asset from that release, not from a third-party mirror.
3. If the release includes checksums, verify the downloaded file.
4. Review any release-specific notes before running the installer.

Windows may display a security warning if an installer is not signed with a certificate trusted by your system. Do not bypass a warning unless you have independently confirmed the file's origin and integrity.

## Other platforms

The source project includes cross-platform build configuration, but that does not mean that a tested installer is available for every operating system. Check the release assets and release notes for the exact platforms and packages published for each version.

## Building from source

If you want to build Weloury yourself, start with [DEVELOPMENT.md](DEVELOPMENT.md). The project uses Go, Node.js, Rust, and Tauri. The desktop build scripts expect the native application project to exist at `apps/desktop`.

A successful backend or frontend build alone is not a complete desktop application build.

## Application data

Weloury stores its local application data on the machine. The exact data-directory name and location are determined by the application version and operating system. Before removing application data manually, consult the version's documentation and make a backup if you want to preserve settings, schedules, automation rules, or history.

## Getting help

For implementation details and known limitations, see [README.md](README.md), [DEVELOPMENT.md](DEVELOPMENT.md), and [SECURITY.md](SECURITY.md). Bugs and project questions can be raised through the repository's [GitHub Issues](https://github.com/mh3nj/Weloury/issues).
