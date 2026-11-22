# Security

## Reporting a problem

Please don't open a public issue for a security problem. Use **Report a vulnerability**
on this repo's [Security tab](../../security), or message the maintainer privately on the
Boost Link Discord. Say what you found and how to reproduce it. We'll reply as soon as we
can and credit you in the fix if you'd like.

## How updates are trusted

The launcher (1.5.0 and later) updates itself from this repo's Releases:

- It downloads over HTTPS only and checks every file's SHA-256 against `latest.json`
  before using it.
- It never installs an older version than the one it has.
- It only writes inside its own `exvs-launcher/` and `exvs-online/` folders. There is no
  unzip step and it never touches game files.
- Releases are built by GitHub Actions from `main`
  ([release.yml](.github/workflows/release.yml)), never uploaded by hand.

Updates are only as trustworthy as the GitHub account and `main` branch that publish them,
so access to both is kept to the maintainer.

## What Boost Link exposes

- **The launcher's page** listens only on `127.0.0.1`. It checks the origin of every change
  and refuses requests addressed to any other host name, so websites can't read or change
  your settings.
- **The card server and the online proxy** listen only on your own PC (`127.0.0.1`) when
  you play Online. The launcher sets the card server's `kestrel.json` to `127.0.0.1` and
  backs up the original first. Host mode stays reachable, since friends connect to it.
- **The match server** only introduces players. It never sees game inputs, and its status
  page and Discord posts show no IPs or card serials.

## Security review (November 2026)

The match server, relay, online proxy, launcher, server kit and this repo were reviewed in
launcher **1.5.3**. Nothing was found that would let an opponent or a stranger run code on
a player's PC through Boost Link's code. The following were fixed:

- The card server and the Join-mode proxy now listen only on the player's own PC, so others
  on the same Wi-Fi or VPN can't read or change their cards.
- Each search is tied to the address that queued it, so nobody else can check or cancel it.
- The match server now has connection timeouts, a request size limit and a queue cap, and it
  trims player details before passing them on.
- Forwarded client addresses are only trusted when they come from the server itself.
- The relay refuses to forward to the server's own private and internal addresses.
- The launcher's local page now rejects requests from other host names.
- Added [`server/harden.sh`](server/harden.sh), which turns on a firewall, automatic security
  updates and login protection on a match server.

Server and account hardening is also recommended for anyone running their own match server.

## Known limits

- **Matches are peer to peer**, like the arcade game, so your opponent's PC learns your IP
  address. Boost Link doesn't add to this. If that matters to you, play through a VPN.
- **The game's own network code** is unchanged. Boost Link can't make it safer against a
  modified opponent.
- **The relay is open to anyone with the game**, because the game needs its login. Its
  bandwidth can be used by strangers.
