# Rocket v1.0.0

Rocket v1.0.0 is a Windows 10/11 x64 and Android 14+ app for direct file and short-text transfers over a trusted local network. Keep Rocket open on both devices while transferring.

## What's included

- **File transfer in both directions:** send selected files from Android to Windows, or select/drag files on Windows for the open Android app to download. Transfers verify file contents and avoid overwriting existing files.
- **Short text in both directions:** stage text on Windows with Ctrl+V in Rocket, or compose/share it on Android; slide to send. The receiving app copies it to the system clipboard. Text is limited to 8192 UTF-8 bytes and is not saved as a file.
- **Long-text UI responsiveness:** fixed a UI responsiveness issue when working with long text messages.
- Portable Windows executable and Android APK; no installer, account, cloud service, telemetry, or background service.

## Requirements

- **Windows:** Windows 10 or 11, 64-bit (x64).
- **Android:** Android 14 or newer.
- **To build from source:** Go 1.25+; Android builds also need JDK 21 and Android SDK API 36.

## Important security and installation notes

- Rocket has **no TLS, passwords, or authentication**. Anyone on a reachable local network may impersonate a device or send files/text that replaces the recipient's clipboard. Use Rocket only on a trusted private network; do not expose it to the internet or use it on public Wi-Fi.
- The release APK is signed with an **ephemeral CI debug key** because no persistent release keystore is configured. Android may refuse to install v1.0.0 over an existing Rocket APK signed with a different key. Uninstalling the old app may be required and clears Rocket's saved PC and folder settings; back up or note those settings and do not uninstall until ready to configure again.
- Windows SmartScreen may warn that the portable executable is unsigned.

## Downloads

- `Rocket-1.0.0-win-x64.exe` — portable Windows executable.
- `Rocket-1.0.0-android.apk` — Android app.
- `SHA256SUMS.txt` — checksums. In PowerShell: `Get-FileHash .\Rocket-1.0.0-win-x64.exe -Algorithm SHA256`.

For Windows connectivity, allow Rocket on the Private network profile and check that Wi-Fi client isolation is disabled. See `README.md` and `SECURITY.md` for usage and security details.
