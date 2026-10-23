# Rocket v0.2.0

Windows 10/11 x64 and Android 14+ release of Rocket, a tiny file transfer app for your local network: pick files, slide, fly.

## Changes

- **One screen, one gesture.** The main screen is the rocket, a *Choose files* button and a gear. Pick files, slide **Let's go!** to the right and the interface fades while the rocket flies; when the files have arrived it leaves through the top edge and the transfer statistics (files, size, time, average speed) appear out of the smoke. The PC and the phone work and look the same.
- **New icon** on both platforms: Android adaptive icon (with a themed-icon layer), and the exe's icon, taskbar and window icon.
- **Everything else moved into Settings:** receive folder, PC and phone selection, addresses, connection check and a firewall check with a one-click fix (Windows asks for permission).
- **It picks the other device for you.** One phone online or one PC found is chosen automatically and remembered; you only choose when there are several. If the PC gets a new IP address, the phone finds it again by its ID.
- **Drag and drop** files onto the PC window.
- **A transfer from the PC counts as delivered only after the phone confirms it** (previously the PC reported success as soon as the last byte left).
- Windows: the window is drawn with antialiasing at any display scale (per-monitor DPI), a dark title bar, and resizing works (a Direct2D call used a wrong method index before).
- Android: haptic feedback on the slider and the result, portrait layout, back button cancels a running transfer.
- Releases are built and published by GitHub Actions when a pull request that bumps `VERSION` is merged.

## Downloads

- `Rocket-0.2.0-win-x64.exe` - the PC app; a single portable executable, no installation.
- `Rocket-0.2.0-android.apk` - the phone app (Android 14 or newer).
- `SHA256SUMS.txt` - checksums for both files.

The binaries are not code-signed, so Windows SmartScreen may show a warning ("More info" → "Run anyway"), and Android asks you to allow installing from your browser or file manager.

To verify a download in PowerShell, run `Get-FileHash .\Rocket-0.2.0-win-x64.exe -Algorithm SHA256` and compare the result with `SHA256SUMS.txt`.

## Good to know

- Rocket has **no encryption or passwords** by design. Use it only on a network you trust.
- Allow Rocket through the Windows firewall once: open the gear on the PC and press **Fix** next to *Firewall* (or run `rocket.exe firewall install` from an administrator terminal).
- To receive on the phone from the PC, keep Rocket open on the phone with the screen on.
