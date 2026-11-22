<p align="center">
  <img src="docs/logo.svg" alt="Weloury logo" width="112">
</p>

<h1 align="center">Weloury</h1>

<p align="center">
  A local desktop workspace for understanding and managing your own machine.
</p>

<p align="center">
  <a href="https://mh3nj.github.io/Weloury/">Project website</a>
  ·
  <a href="https://github.com/mh3nj/Weloury">Source code</a>
  ·
  <a href="https://github.com/mh3nj/Weloury/releases">Releases</a>
</p>

---

## About

Weloury brings everyday system tools into one desktop application. Instead of jumping between process viewers, service managers, container tools, terminal commands, and log windows, you can work with them from one place.

The application is local-first. Its core runs on your machine, stores application data in SQLite, and communicates with the desktop interface over a loopback connection. The local features do not require an account or a hosted service.

Weloury began under the name **FORGE**. It was later renamed and given its own identity.

## What it includes

- **Overview:** machine identity, uptime, CPU and memory information.
- **Processes:** a searchable process list, process details, and confirmed termination actions.
- **Services:** service inspection and supported start, stop, and restart actions, subject to platform support and user permissions.
- **Storage:** mounted filesystems and their usage.
- **Health checks:** checks for processes, services, ports, HTTP endpoints, commands, and resource thresholds.
- **Logs and events:** application logs and recorded system and application activity.
- **Schedules:** one-time and recurring tasks.
- **Automation:** event or schedule triggers, conditions, and ordered actions.
- **Network:** network interfaces, listening ports, and active connections.
- **Containers:** Docker container discovery, basic lifecycle actions, and recent logs.
- **Diagnostics:** an exportable snapshot for troubleshooting.

Available capabilities vary by operating system. Some inspection features are platform-specific. Docker features require a Docker daemon to be installed and running.

## Windows installers

Windows installer builds are available in MSI and NSIS formats. The installer files are distributed as assets on the GitHub Releases page when the maintainer attaches them to a release. If no installer asset is listed there yet, it has not been published as a download.

Before running an installer, check that it came from the official [Weloury Releases](https://github.com/mh3nj/Weloury/releases) page. If checksums are provided with a release, verify the downloaded file against them.

## Technical overview

The original application is built from three main parts:

- **Go:** system operations, application logic, persistence, health checks, scheduling, automation, and the local API.
- **React and TypeScript:** the desktop interface.
- **Rust and Tauri:** the native desktop shell and lifecycle management for the Go backend.

The Go backend uses SQLite for local persistence. The desktop interface communicates with it through a local HTTP interface bound to loopback. It is not intended to expose the API to the network.

For implementation details, see [ARCHITECTURE.md](ARCHITECTURE.md).

## Project status

Weloury is no longer being developed as a standalone application.

Its work is being carried forward together with Stellarie into a much larger commercial application. That successor is a substantial rewrite in Rust, not a continuation of this codebase. This repository remains available as a record of the original project and its implementation. No further standalone feature development is planned here.

## Building from source

The original project uses Go, Rust, Node.js, and the Tauri CLI. Toolchain requirements and build steps are documented in [DEVELOPMENT.md](DEVELOPMENT.md).

```bash
./scripts/dev.sh
```

The desktop development and release scripts require the Tauri project at `apps/desktop`. Make sure that directory is present in your checkout before running them. Backend or frontend checks on their own do not verify a complete desktop build.

## Documentation

- [Architecture](ARCHITECTURE.md)
- [Development](DEVELOPMENT.md)
- [Installation](INSTALL.md)
- [Security](SECURITY.md)
- [Change history](CHANGELOG.md)
- [Contributing](CONTRIBUTING.md)

## License

Weloury is released under the [MIT License](LICENSE).
