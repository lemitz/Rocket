# Builds everything the release workflow builds, into dist/:
#   Rocket-<version>-win-x64.exe, Rocket-<version>-android.apk, SHA256SUMS.txt
# Usage: ./scripts/build.ps1 [-SkipTests] [-SkipAndroid] [-Offline]
#   -Offline passes --offline to Gradle (needs a warm Gradle cache, like the dev machine).
param([switch]$SkipTests, [switch]$SkipAndroid, [switch]$Offline)

$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
Set-Location $root

& "$PSScriptRoot/check-release.ps1"
$version = (Get-Content VERSION -Raw).Trim()
$dist = Join-Path $root 'dist'
New-Item -ItemType Directory -Path $dist -Force | Out-Null

function Run($what, [scriptblock]$cmd) {
    Write-Host "==> $what"
    & $cmd
    if ($LASTEXITCODE -ne 0) { throw "$what failed ($LASTEXITCODE)" }
}

if (-not $SkipTests) {
    $unformatted = gofmt -l cmd internal tools
    if ($unformatted) { throw "gofmt: run 'gofmt -w' on:`n$unformatted" }
    Run 'go vet' { go vet ./... }
    Run 'go test -race' { go test -race ./... }
}

$exe = Join-Path $dist "Rocket-$version-win-x64.exe"
Run 'go build rocket.exe' { go build -trimpath -ldflags "-s -w -X main.version=$version" -o $exe ./cmd/rocket }

if (-not $SkipAndroid) {
    $gradleArgs = @(':app:assembleDebug', ':app:assembleRelease', '--no-daemon')
    if ($Offline) { $gradleArgs += '--offline' }
    Push-Location (Join-Path $root 'android')
    try { Run 'gradle assemble' { & .\gradlew.bat @gradleArgs } } finally { Pop-Location }
    Copy-Item (Join-Path $root 'android/app/build/outputs/apk/release/app-release.apk') (Join-Path $dist "Rocket-$version-android.apk") -Force
}

$hashes = Get-ChildItem $dist -File | Where-Object { $_.Name -like 'Rocket-*' } | Sort-Object Name | ForEach-Object {
    "$((Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant())  $($_.Name)"
}
[IO.File]::WriteAllText((Join-Path $dist 'SHA256SUMS.txt'), ($hashes -join "`n") + "`n", [Text.Encoding]::ASCII)
Write-Host "`nDone: v$version"
Get-ChildItem $dist | Select-Object Name, Length | Format-Table -AutoSize
