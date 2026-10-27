# Standalone docflow installer for Windows. Downloads a release archive,
# verifies its sha256 checksum, unpacks it, and runs `docflow install` from
# the unpacked binary. Windows 10/11, amd64 and arm64; no Go needed.
#
#   irm https://raw.githubusercontent.com/sboko83/docflow/main/install.ps1 | iex
#
# Environment overrides (same variables as install.sh):
#   DOCFLOW_VERSION=X.Y.Z      install this version instead of the latest
#   DOCFLOW_RELEASE_URL=BASE   release base URL (default: GitHub Releases)
#   DOCFLOW_SKILLS=global|project
#                              link personal skills (global) or keep the agent
#                              directories clean and use project copies only
#                              (project); passed to `docflow install --skills`.
#                              Unset: docflow install asks on a terminal and
#                              remembers the answer; without a terminal global.

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

function Fail([string]$Message) {
    throw "install.ps1: $Message"
}

# Windows PowerShell 5.1 does not enable TLS 1.2 by default on older builds.
[Net.ServicePointManager]::SecurityProtocol = `
    [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

if ($env:OS -ne 'Windows_NT') {
    Fail 'this installer is for Windows; on macOS and Linux run install.sh'
}

$Arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    default { Fail "unsupported CPU architecture: $($env:PROCESSOR_ARCHITECTURE) (supported: amd64, arm64)" }
}

$Base = if ($env:DOCFLOW_RELEASE_URL) { $env:DOCFLOW_RELEASE_URL } else { 'https://github.com/sboko83/docflow/releases' }
$Base = $Base.TrimEnd('/')

function Fetch([string]$Url, [string]$OutFile) {
    try {
        Invoke-WebRequest -UseBasicParsing -Uri $Url -OutFile $OutFile
    } catch {
        Fail "cannot download $Url"
    }
}

$Temp = Join-Path ([IO.Path]::GetTempPath()) "docflow-install-$([IO.Path]::GetRandomFileName())"
New-Item -ItemType Directory -Path $Temp | Out-Null

try {
    $Version = $env:DOCFLOW_VERSION
    if (-not $Version) {
        $VersionFile = Join-Path $Temp 'VERSION'
        Fetch "$Base/latest/download/VERSION" $VersionFile
        $Version = (Get-Content -Raw $VersionFile).Trim()
    }
    $Version = $Version.TrimStart('v')
    if ($Version -notmatch '^[0-9]+\.[0-9]+\.[0-9]+$') {
        Fail "invalid release version: '$Version' (want X.Y.Z)"
    }

    $Name = "docflow_windows_$Arch.tar.gz"
    $Url = "$Base/download/v$Version"
    $Archive = Join-Path $Temp $Name
    Fetch "$Url/$Name" $Archive
    $ChecksumsFile = Join-Path $Temp 'checksums.txt'
    Fetch "$Url/checksums.txt" $ChecksumsFile

    $Want = $null
    foreach ($Line in (Get-Content $ChecksumsFile)) {
        if ($Line -match ('^([0-9a-fA-F]+) [ *]' + [Regex]::Escape($Name) + '$')) {
            $Want = $Matches[1].ToLowerInvariant()
        }
    }
    if (-not $Want) {
        Fail "no checksum for $Name in checksums.txt"
    }
    $Got = (Get-FileHash -Algorithm SHA256 $Archive).Hash.ToLowerInvariant()
    if ($Got -ne $Want) {
        Fail "sha256 mismatch for ${Name}: expected $Want, got $Got"
    }

    # Windows ships bsdtar as tar.exe; prefer it over a tar from PATH: MSYS
    # GNU tar, which Git Bash puts ahead of System32, rejects drive-letter
    # paths. tar needs Windows 10 1803+; Git for Windows has one too.
    $Tar = Join-Path $env:SystemRoot 'System32\tar.exe'
    if (-not (Test-Path $Tar)) {
        $Tar = (Get-Command tar -ErrorAction SilentlyContinue).Source
        if (-not $Tar) {
            Fail 'tar is required to unpack the archive (Windows 10 1803+ or Git for Windows)'
        }
    }
    $Release = Join-Path $Temp 'release'
    New-Item -ItemType Directory -Path $Release | Out-Null
    & $Tar -xzf $Archive -C $Release
    if ($LASTEXITCODE -ne 0) {
        Fail "cannot unpack $Name"
    }
    $Binary = Join-Path $Release 'docflow.exe'
    if (-not (Test-Path $Binary)) {
        Fail "archive has no docflow.exe binary: $Name"
    }

    if ($env:DOCFLOW_SKILLS) {
        & $Binary install --skills $env:DOCFLOW_SKILLS
    } else {
        & $Binary install
    }
    if ($LASTEXITCODE -ne 0) {
        Fail 'docflow install failed'
    }
} finally {
    Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $Temp
}
