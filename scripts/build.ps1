#Requires -Version 5.1
<#
    Builds a Windows installer for Weloury: the Go core, optionally the
    bundled smartctl SMART reader, the frontend, and the Tauri MSI/NSIS
    packages.

    Prerequisites: Go, Rust (MSVC toolchain), and Node. The Tauri CLI comes
    from frontend/node_modules, so npm install runs as part of this script.

    smartmontools (smartctl.exe) is OPTIONAL and the build never stops for it.
    When present it is bundled into the installer so the Storage tab reads real
    per-drive SMART health on any machine, with no install step. The script
    looks for it in this order:
      1. apps/desktop/smartctl.exe, if already present.
      2. The path in the WELOURY_SMARTCTL_SRC environment variable.
      3. A standard smartmontools install (C:\Program Files\smartmontools\bin,
         Chocolatey's bin) or smartctl.exe on PATH.
      4. Otherwise it tries winget, then an elevated choco, and retries.
    If none of those yield a smartctl.exe, the installer is still built without
    it and the script prints how to get real health later. This is not a
    downgrade of the app: even unbundled, Weloury finds a system smartctl.exe on
    PATH at runtime and reads real SMART, and shows an honest fallback when no
    SMART tool is available at all. (Note: smartmontools ships Windows binaries
    only as an admin NSIS installer, so bundling a fresh copy is what needs the
    one-time elevated install; once smartctl.exe is on the machine this script
    just finds and embeds it.)
#>

[CmdletBinding()]
param(
    [switch]$SkipInstaller
)

$ErrorActionPreference = "Stop"

$Root = Split-Path -Parent $PSScriptRoot
$Desktop = Join-Path $Root "apps/desktop"

function Step($msg) { Write-Host "==> $msg" -ForegroundColor Cyan }

Step "Building Weloury core"
Push-Location (Join-Path $Root "backend")
try {
    go build -o (Join-Path $Desktop "weloury-core.exe") ./cmd/weloury-core
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
}
finally { Pop-Location }

Step "Locating smartctl.exe to bundle"
$SmartctlDst = Join-Path $Desktop "smartctl.exe"

function Find-Smartctl {
    # Usual install locations first, then PATH. choco and the smartmontools
    # installer both drop smartctl.exe in C:\Program Files\smartmontools\bin.
    $paths = @(
        (Join-Path $env:ProgramFiles "smartmontools\bin\smartctl.exe"),
        (Join-Path ${env:ProgramFiles(x86)} "smartmontools\bin\smartctl.exe"),
        (Join-Path $env:ProgramData "chocolatey\bin\smartctl.exe")
    )
    foreach ($p in $paths) { if (Test-Path $p) { return (Get-Item $p).FullName } }
    $cmd = Get-Command smartctl.exe -ErrorAction SilentlyContinue
    if (-not $cmd) { $cmd = Get-Command smartctl -ErrorAction SilentlyContinue }
    if ($cmd) { return $cmd.Source }
    return $null
}

# smartctl is optional. Resolve a real smartctl.exe if we can, copy it in
# beside the core, and remember to bundle it. If we can't, build without it
# rather than failing: the app finds a system smartctl.exe on PATH at runtime.
$bundleSmartctl = $false
if (Test-Path $SmartctlDst) {
    $bundleSmartctl = $true
    Step "Using existing apps/desktop/smartctl.exe"
}
else {
    $src = $null
    if ($env:WELOURY_SMARTCTL_SRC -and (Test-Path $env:WELOURY_SMARTCTL_SRC)) {
        $src = (Get-Item $env:WELOURY_SMARTCTL_SRC).FullName
    }
    else {
        $src = Find-Smartctl
    }

    if (-not $src) {
        Step "smartctl not present; trying to install smartmontools (winget, then choco)"
        $winget = Get-Command winget -ErrorAction SilentlyContinue
        if ($winget) {
            & winget install -e --id smartmontools.smartmontools --accept-source-agreements --accept-package-agreements --silent 2>&1 | Write-Host
            $src = Find-Smartctl
        }
        if (-not $src) {
            $choco = Get-Command choco -ErrorAction SilentlyContinue
            if ($choco) {
                & choco install smartmontools -y 2>&1 | Write-Host
                $src = Find-Smartctl
            }
        }
    }

    if ($src) {
        Copy-Item $src $SmartctlDst -Force
        $bundleSmartctl = $true
        Step "Bundled smartctl from $src"
    }
    else {
        Step "No smartctl.exe found; building the installer WITHOUT bundled SMART."
        Write-Host "    Real drive health still works two ways:" -ForegroundColor Yellow
        Write-Host "      1. Install smartmontools once from an elevated shell" -ForegroundColor Yellow
        Write-Host "         (winget install smartmontools.smartmontools, or choco install smartmontools -y)," -ForegroundColor Yellow
        Write-Host "         then re-run this script to BUNDLE it into the installer; or" -ForegroundColor Yellow
        Write-Host "      2. Leave smartmontools installed on the target machine: Weloury" -ForegroundColor Yellow
        Write-Host "         finds smartctl.exe on PATH at runtime and reads real SMART anyway." -ForegroundColor Yellow
        Write-Host "    Force a specific binary instead: set WELOURY_SMARTCTL_SRC, or drop" -ForegroundColor Yellow
        Write-Host "    smartctl.exe into apps/desktop/ before running this script." -ForegroundColor Yellow
    }
}

Step "Building frontend"
Push-Location (Join-Path $Root "frontend")
try {
    npm install
    npm run build
    if ($LASTEXITCODE -ne 0) { throw "frontend build failed" }
}
finally { Pop-Location }

if ($SkipInstaller) {
    Step "SkipInstaller set; core + frontend built, bundling skipped."
    return
}

Step "Building desktop shell (Tauri bundle)"
Push-Location $Desktop
try {
    # Call the project's own Tauri CLI (the version matched to the tauri
    # crate) rather than any globally installed `cargo tauri`, which on some
    # machines is an older 1.x that rejects this 2.x config. The resource list
    # is generated here, not hardcoded: smartctl.exe is added only when we
    # actually resolved it above. Listing it while absent would make Tauri's
    # bundler abort, so we never do that. The base tauri.conf.json stays
    # core-only so `tauri dev` and non-release builds never need smartctl.
    $Cli = Join-Path $Root "frontend/node_modules/@tauri-apps/cli/tauri.js"
    if (-not (Test-Path $Cli)) { throw "Tauri CLI not found at $Cli. Run npm install in frontend first." }

    $resources = @("weloury-core.exe")
    if ($bundleSmartctl) { $resources += "smartctl.exe" }
    # Write the array as literal JSON: ConvertTo-Json collapses a single-element
    # array to a bare string, which Tauri's resources schema rejects.
    $quoted = ($resources | ForEach-Object { '"' + $_ + '"' }) -join ','
    $overrideConf = Join-Path $Desktop "bundle-windows-runtime.conf.json"
    Set-Content -Path $overrideConf -Value ('{"bundle":{"resources":[' + $quoted + ']}}') -Encoding ascii

    node $Cli build --config $overrideConf
    if ($LASTEXITCODE -ne 0) { throw "tauri build failed" }
}
finally { Pop-Location }

Write-Host ""
Write-Host "Installer output:" -ForegroundColor Green
Write-Host "  apps/desktop/target/release/bundle/msi"
Write-Host "  apps/desktop/target/release/bundle/nsis"
