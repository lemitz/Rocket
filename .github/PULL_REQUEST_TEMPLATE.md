## What and why

<!-- A sentence or two. Link the issue if there is one. -->

## How I checked it

- [ ] `go vet ./...` and `go test -race ./...`
- [ ] `./scripts/build.ps1` (builds the exe and the APK)
- [ ] Tried it on a real device (say which): <!-- phone/PC, Android version -->

## Release

- [ ] This change should be released: I bumped `VERSION` and rewrote `RELEASE_NOTES.md` (merging publishes `v<VERSION>` automatically)
- [ ] No release yet: `VERSION` is unchanged (merging only builds)

<!-- If the protocol changed: internal/wire, internal/server, RocketProtocol.kt, tests and docs/PROTOCOL.md must change together.
     If the look of the scene changed: change both internal/desktop/scene_windows.go and LaunchView.kt. -->
