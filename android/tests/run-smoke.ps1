$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
$compiled = Join-Path $root 'app/build/tmp/kotlin-classes/debug'
if (-not (Test-Path $compiled)) { throw 'Build the app with gradlew.bat :app:assembleDebug --offline --no-daemon first' }
$gradleHome = if ($env:GRADLE_USER_HOME) { $env:GRADLE_USER_HOME } else { Join-Path $env:USERPROFILE '.gradle' }
$stdlib = Get-ChildItem (Join-Path $gradleHome 'caches/modules-2/files-2.1/org.jetbrains.kotlin/kotlin-stdlib/2.1.0') -Recurse -Filter 'kotlin-stdlib-2.1.0.jar' | Select-Object -First 1 -ExpandProperty FullName
if (-not $stdlib) { throw 'Offline Kotlin stdlib 2.1.0 not found in Gradle cache' }
$out = Join-Path $root 'app/build/discovery-smoke'
New-Item -ItemType Directory -Path $out -Force | Out-Null
& javac -cp "$compiled;$stdlib" -d $out (Join-Path $PSScriptRoot 'PcDiscoverySmoke.java')
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
& java -cp "$out;$compiled;$stdlib" dev.rocket.android.PcDiscoverySmoke
exit $LASTEXITCODE
