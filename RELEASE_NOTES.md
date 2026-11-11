# Rocket v0.3.0

Windows 10/11 x64 and Android 14+ release of Rocket. Alongside the existing file transfers, you can now send short text messages over your trusted local network and have the recipient automatically copy them to their clipboard.

## Changes

- **PC → phone text:** copy text, focus Rocket's main window and press **Ctrl+V**. Check the preview and slide **Let's go!**. The open Android app copies the message to its clipboard automatically. You still choose the destination phone when more than one is online.
- **Phone → PC text:** tap **Send text** to type/paste a message, or share text from another Android app to Rocket. Slide **Let's go!**; the Windows app copies it to the system clipboard before acknowledging delivery. Copying, pasting, or sharing alone does **not** send anything.
- Messages are limited to **8192 bytes of UTF-8** and aren't saved as files. PC → phone messages are held in memory until the phone receives and confirms them (or for up to 30 minutes). Receiving text **replaces the previous contents of the recipient's clipboard**. The phone must have Rocket open to receive.
- **Windows result card fixes:** long message titles now wrap instead of being cut off, including in a narrow window and in Russian. The **OK** button on a received-message card now closes it instead of opening File Explorer; file-result cards retain their separate **Open folder** button.
- Existing file transfer workflow and network roles are unchanged. Text and file events use separate session statistics. New commands are documented in `docs/PROTOCOL.md`; old builds do not support messages, so update both devices.

## Downloads

- `Rocket-0.3.0-win-x64.exe` — portable Windows executable.
- `Rocket-0.3.0-android.apk` — Android 14+ app.
- `SHA256SUMS.txt` — checksums. In PowerShell: `Get-FileHash .\Rocket-0.3.0-win-x64.exe -Algorithm SHA256`.

Windows may show a SmartScreen warning for an unsigned executable. **Android signing:** no persistent release keystore is configured in the repository yet. CI signs the release APK with an ephemeral debug key; it may **not** install over v0.2.0 or a locally built APK. If Android rejects the update, backing up your settings/selected destination and uninstalling the old APK is necessary before installing this one. Uninstalling clears Rocket's saved PC and folder settings. Do not uninstall a working installation until you are ready to set it up again.

## Good to know

- Rocket deliberately has **no TLS, passwords or authentication**. On an untrusted network somebody can send text that overwrites your Windows clipboard or pretend to be the receiving phone. Only use Rocket on a LAN you trust; do not send secrets over public Wi-Fi.
- If the PC cannot see the phone, keep Rocket open on the phone and check the PC's Private-network firewall rules in Settings (or run `rocket.exe firewall install` as administrator).
- The user tested file and text transfers in both directions, including automatic clipboard copying, on real Windows PCs and Android phones and confirmed they work. Automated Go protocol tests and Android builds also pass. A lost acknowledgement can still cause a message to be copied again.
