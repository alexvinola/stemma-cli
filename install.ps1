<#
.SYNOPSIS
    Installs the stemma CLI for the current Windows user, no administrator
    rights required.

.DESCRIPTION
    Downloads the correct binary (amd64 or arm64, detected automatically) for
    the latest tagged release from GitHub, verifies it against the release's
    published SHA-256 checksums, and installs it to a per-user directory that
    is added to the current user's PATH.
#>
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$ErrorActionPreference = "Stop"

$Repo = "alexvinola/stemma-cli"
$InstallDir = Join-Path $env:LOCALAPPDATA "Programs\stemma"

function Get-StemmaArch {
    # PROCESSOR_ARCHITEW6432 is only set when the current process is running
    # under x64 emulation on an ARM64 machine; when it is set, it reports the
    # *real* processor architecture, which PROCESSOR_ARCHITECTURE alone would
    # otherwise hide.
    $real = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
    switch ($real) {
        "ARM64" { return "arm64" }
        "AMD64" { return "amd64" }
        default {
            throw "Unsupported processor architecture '$real'. Stemma publishes amd64 and arm64 " +
                  "Windows builds only."
        }
    }
}

function Get-LatestRelease {
    Write-Host "Looking up the latest release of $Repo..."
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" `
        -Headers @{ "User-Agent" = "stemma-install-script" }
    if (-not $release.assets) {
        throw "The latest release of $Repo has no attached files."
    }
    return $release
}

function Get-Asset($release, [string]$name) {
    $asset = $release.assets | Where-Object { $_.name -eq $name }
    if (-not $asset) {
        throw "The latest release ($($release.tag_name)) does not include an asset named '$name'."
    }
    return $asset
}

$arch = Get-StemmaArch
$release = Get-LatestRelease
$binaryName = "stemma-windows-$arch.exe"

Write-Host "Installing stemma $($release.tag_name) ($arch) to $InstallDir"

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
$tempDir = Join-Path ([System.IO.Path]::GetTempPath()) ("stemma-install-" + [Guid]::NewGuid())
New-Item -ItemType Directory -Force -Path $tempDir | Out-Null

try {
    $binaryAsset = Get-Asset $release $binaryName
    $checksumsAsset = Get-Asset $release "checksums.txt"

    $binaryTemp = Join-Path $tempDir $binaryName
    $checksumsTemp = Join-Path $tempDir "checksums.txt"

    Write-Host "Downloading $binaryName..."
    Invoke-WebRequest -Uri $binaryAsset.browser_download_url -OutFile $binaryTemp -UseBasicParsing
    Invoke-WebRequest -Uri $checksumsAsset.browser_download_url -OutFile $checksumsTemp -UseBasicParsing

    # Verify against the release's published SHA-256, rather than trusting the
    # download unconditionally. Stemma does not ship a signed binary, so this
    # is the integrity check that exists in its place.
    $expectedLine = Select-String -Path $checksumsTemp -Pattern ([Regex]::Escape($binaryName)) |
        Select-Object -First 1
    if (-not $expectedLine) {
        throw "No checksum entry for $binaryName in checksums.txt."
    }
    $expectedHash = ($expectedLine.Line -split '\s+')[0].ToLowerInvariant()
    $actualHash = (Get-FileHash -Path $binaryTemp -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($expectedHash -ne $actualHash) {
        throw "Checksum mismatch for $binaryName.`n  expected: $expectedHash`n  got:      $actualHash`n" +
              "The download may be corrupt or tampered with. Nothing was installed."
    }
    Write-Host "Checksum verified."

    Move-Item -Force -Path $binaryTemp -Destination (Join-Path $InstallDir "stemma.exe")
}
finally {
    Remove-Item -Recurse -Force -Path $tempDir -ErrorAction SilentlyContinue
}

# Add the install directory to the user's PATH, without admin rights and
# without clobbering whatever is already there. setx PATH is deliberately not
# used here: it can silently truncate long PATH values.
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
$pathEntries = @()
if ($userPath) { $pathEntries = $userPath -split ";" | Where-Object { $_ -ne "" } }
if ($pathEntries -notcontains $InstallDir) {
    $newPath = if ($userPath) { "$userPath;$InstallDir" } else { $InstallDir }
    [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
    Write-Host "Added $InstallDir to your user PATH."
} else {
    Write-Host "$InstallDir is already on your user PATH."
}

# Also update the current session, so `stemma` works immediately in the
# terminal that ran this script, without waiting for a new window.
if (($env:Path -split ";") -notcontains $InstallDir) {
    $env:Path = "$env:Path;$InstallDir"
}

Write-Host ""
Write-Host "Installed stemma $($release.tag_name) to $InstallDir\stemma.exe"
Write-Host "Open a new terminal window (or use this one) and run: stemma version"
