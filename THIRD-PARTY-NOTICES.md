# Third-party notices

Rocket ships **no third-party runtime code**.

- **Windows (`rocket.exe`)** is built from this repository with the Go standard library only (BSD-3-Clause, Go Authors) and talks to the operating system through system DLLs (`user32`, `gdi32`, `d2d1`, `shell32`, ...). There are no Go modules besides the standard library.
- **Android (`app-release.apk`)** uses the Android platform APIs only: no AndroidX, Compose, coroutines or other libraries. The Kotlin standard library is bundled by the Kotlin Gradle plugin (Apache-2.0, JetBrains).
- **Build tools** (not shipped): Go, JDK, Gradle, Android Gradle Plugin and Kotlin Gradle plugin (Apache-2.0), `windres` from GNU binutils (used only to compile the icon and manifest resource, whose result is committed as `cmd/rocket/rocket_windows_amd64.syso`).
- **Artwork:** the Rocket icon and the rocket sprite derived from it (`assets/`, `internal/desktop/assets/rocket.png`, the Android launcher and rocket drawables, `cmd/rocket/rocket.ico`) are the owner's own artwork and are covered by the repository licence.