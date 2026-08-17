#
# Astra Harness one-line installer (Windows PowerShell 5.1+).
#
#   irm https://astracode.topodrive.top/install/install.ps1 | iex
#
# Release assets and checksums are published by the tag workflow.
# Override the download base or target directory with:
#   $env:ASTRA_BASE_URL = "https://..."; $env:ASTRA_INSTALL_DIR = "D:\astra"
$ErrorActionPreference = "Stop"

$BaseUrl = if ($env:ASTRA_BASE_URL) { $env:ASTRA_BASE_URL } else { "https://github.com/kevenhu001-cyber/astra-harness/releases/latest/download" }
$InstallDir = if ($env:ASTRA_INSTALL_DIR) { $env:ASTRA_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA "Programs\astra" }

$arch = $env:PROCESSOR_ARCHITECTURE
if ($arch -eq "x86" -and $env:PROCESSOR_ARCHITEW6432) {
  $arch = $env:PROCESSOR_ARCHITEW6432
}
switch ($arch) {
  "AMD64" { $targetArch = "amd64" }
  "ARM64" { $targetArch = "arm64" }
  default { throw "Unsupported architecture: $arch" }
}

$asset = "astra-windows-$targetArch.zip"
$tmp = Join-Path $env:TEMP ("astra-install-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $tmp | Out-Null

try {
  Write-Host "Downloading $asset ..."
  Invoke-WebRequest -UseBasicParsing -Uri "$BaseUrl/$asset" -OutFile (Join-Path $tmp "astra.zip")
  Invoke-WebRequest -UseBasicParsing -Uri "$BaseUrl/sha256sums.txt" -OutFile (Join-Path $tmp "sha256sums.txt")

  $expected = Get-Content (Join-Path $tmp "sha256sums.txt") |
    Where-Object { $_ -match ("^[0-9a-f]{64}  " + [regex]::Escape($asset) + "$") } |
    ForEach-Object { ($_ -split "\s+")[0] }
  if (-not $expected) { throw "Checksum entry not found for $asset" }

  $actual = (Get-FileHash (Join-Path $tmp "astra.zip") -Algorithm SHA256).Hash.ToLowerInvariant()
  if ($actual -ne $expected.ToLowerInvariant()) { throw "Checksum mismatch for $asset" }

  New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
  Expand-Archive -Path (Join-Path $tmp "astra.zip") -DestinationPath $InstallDir -Force

  $astra = Join-Path $InstallDir "astra.exe"
  $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
  $parts = if ($userPath) { $userPath -split ";" } else { @() }
  $pathUpdated = $false
  if ($parts -notcontains $InstallDir) {
    $newPath = if ($userPath) { "$InstallDir;$userPath" } else { $InstallDir }
    [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
    $pathUpdated = $true
    Write-Host "Added $InstallDir to your user PATH (persistent)."
  }

  # Also update this PowerShell session so `astra` works immediately.
  $currentParts = $env:Path -split ";"
  if ($currentParts -notcontains $InstallDir) {
    $env:Path = "$InstallDir;$env:Path"
    Write-Host "Updated PATH for this terminal."
  }

  if ($pathUpdated) {
    Write-Host ""
    Write-Host "VS Code: quit VS Code completely and reopen it, then open a new terminal."
    Write-Host "Other apps: restart them so they pick up the new PATH."
    Write-Host "This terminal already works: 'astra version' below."
  }

  & $astra version
  Write-Host "Astra installed: $InstallDir\astra.exe"
} finally {
  Remove-Item -Recurse -Force $tmp
}
