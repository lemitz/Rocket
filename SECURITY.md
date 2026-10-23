# Security

## What Rocket promises, and what it does not

Rocket is built for a **trusted local network** (your home or office Wi-Fi) and deliberately has **no encryption, passwords or authentication**. This is a design decision, not a bug:

- Anyone who can reach the PC on TCP 42070 can send it a file (it lands in the receive folder, never outside it, and never overwrites an existing file) and can pretend to be a phone.
- Device IDs are public labels used to pick and match devices. They are **not** credentials.
- CRC32C detects accidental corruption only; it protects against nothing malicious.
- The PC serves only private, loopback or link-local clients, and the optional firewall rules (`rocket.exe firewall install`) are limited to the Private profile and the local subnet. Neither identifies a device.
- The PC never offers a listing of its files, and an arbitrary path cannot be requested: only files you explicitly picked and addressed to a specific phone can be fetched, for 30 minutes at most.

Do not run Rocket on public or untrusted Wi-Fi, and do not expose TCP 42070 or UDP 42069 to the internet.

## Reporting a problem

If you find a way to read or write outside the receive folder, overwrite files, run code, crash the PC server from the network, or make Rocket do something its description above does not allow, please report it **privately** through [GitHub security advisories](https://github.com/lemitz/Rocket/security/advisories/new) rather than a public issue. Weaknesses that follow directly from the missing authentication are known and documented here.