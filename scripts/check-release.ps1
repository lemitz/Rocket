# Checks the release metadata the workflow relies on:
#   - VERSION holds a plain semver (1.2.3)
#   - RELEASE_NOTES.md starts with "# Rocket v<VERSION>"
#   - optionally, a tag (argument, or GITHUB_REF_NAME for a tag build) equals v<VERSION>
# Usage: ./scripts/check-release.ps1 [v1.2.3]
param([string]$Tag)

$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent

$version = (Get-Content -LiteralPath (Join-Path $root 'VERSION') -Raw).Trim()
if ($version -notmatch '^\d+\.\d+\.\d+$') {
    throw "VERSION must be a stable semver like 1.2.3, got '$version'"
}

$notes = Get-Content -LiteralPath (Join-Path $root 'RELEASE_NOTES.md') -TotalCount 1
$notes = $notes.TrimStart([char]0xFEFF)
if ($notes -ne "# Rocket v$version") {
    throw "RELEASE_NOTES.md must start with '# Rocket v$version', got '$notes'"
}

if (-not $Tag -and $env:GITHUB_REF_TYPE -eq 'tag') { $Tag = $env:GITHUB_REF_NAME }
if ($Tag -and $Tag -ne "v$version") {
    throw "Tag $Tag does not match v$version"
}

Write-Host "Release metadata OK: v$version$(if ($Tag) { " ($Tag)" })"
