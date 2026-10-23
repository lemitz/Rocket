# Creates the Android release keystore once and prints the four GitHub secrets the release
# workflow reads. KEEP THE KEYSTORE: every future release must be signed with the same key,
# otherwise Android refuses to update an installed Rocket (it would need an uninstall).
#
# Usage: ./scripts/make-keystore.ps1 [-OutDir $env:USERPROFILE\.rocket-signing]
# Needs `keytool` (it ships with the JDK). Nothing is written inside the repository.
param([string]$OutDir = (Join-Path $env:USERPROFILE '.rocket-signing'))

$ErrorActionPreference = 'Stop'
$keytool = (Get-Command keytool -ErrorAction SilentlyContinue).Source
if (-not $keytool -and $env:JAVA_HOME) { $keytool = Join-Path $env:JAVA_HOME 'bin/keytool.exe' }
if (-not $keytool -or -not (Test-Path $keytool)) { throw 'keytool not found: install a JDK or set JAVA_HOME' }

New-Item -ItemType Directory -Path $OutDir -Force | Out-Null
$store = Join-Path $OutDir 'rocket-release.jks'
if (Test-Path $store) { throw "$store already exists; refusing to overwrite a signing key" }

function RandomSecret { -join ((48..57) + (65..90) + (97..122) | Get-Random -Count 24 | ForEach-Object { [char]$_ }) }
$password = RandomSecret
$alias = 'rocket'

& $keytool -genkeypair -v -keystore $store -alias $alias -keyalg RSA -keysize 4096 -validity 10000 `
    -storepass $password -keypass $password -dname 'CN=Rocket, O=lemitz'
if ($LASTEXITCODE -ne 0) { throw 'keytool failed' }

$base64 = [Convert]::ToBase64String([IO.File]::ReadAllBytes($store))
[IO.File]::WriteAllText("$store.b64", $base64, [Text.Encoding]::ASCII)
$secrets = Join-Path $OutDir 'github-secrets.txt'
@"
ANDROID_KEYSTORE_BASE64=$base64
ANDROID_KEYSTORE_PASSWORD=$password
ANDROID_KEY_ALIAS=$alias
ANDROID_KEY_PASSWORD=$password
"@ | Set-Content -LiteralPath $secrets -Encoding ASCII

Write-Host @"

Keystore: $store
Secrets : $secrets   (contains the password: do not commit or share it)

Add them to the repository (Settings -> Secrets and variables -> Actions), or with the GitHub CLI:
  gh secret set ANDROID_KEYSTORE_BASE64 < "$store.b64"
  gh secret set ANDROID_KEYSTORE_PASSWORD
  gh secret set ANDROID_KEY_ALIAS
  gh secret set ANDROID_KEY_PASSWORD

Back up $OutDir somewhere safe.
"@
