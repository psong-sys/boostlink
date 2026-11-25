# Boost Link

Boost Link adds online versus to the PC build of *Mobile Suit Gundam Extreme Vs. 2
Infinite Boost* (EXVS2 IB). It targets the 2026-04 build (GQuuuuuuX update, card protocol 141);
it was first made on version 1.04.27.

The game already contains the arcade's online code, but it expects the official
matchmaking servers. Boost Link provides a replacement match server, a local proxy that
connects the game to it, and a launcher that sets everything up and starts the game.

Boost Link is a fan project. It is not affiliated with or endorsed by Bandai Namco or
Sunrise. This repository contains no game files, and none are distributed with releases.
Each player needs their own copy of the game.

## Contents

- [Status](#status)
- [How it works](#how-it-works)
- [Requirements](#requirements)
- [Getting started](#getting-started)
- [Troubleshooting](#troubleshooting)
- [Running your own match server](#running-your-own-match-server)
- [Repository layout](#repository-layout)
- [Building from source](#building-from-source)
- [Releases and updates](#releases-and-updates)
- [Security](#security)

## Status

| Feature | State |
|---|---|
| 1v1 online versus between two PCs on different networks | Working |
| Matchmaking queue, live status page | Working |
| Relay (TURN) for players whose routers block direct connections | Working |
| Launcher with self-update | Working (1.5.0 and later) |
| English text for menus, card screens and the Live Monitor | Working |
| English card editor in the launcher | Working |
| 2v2 matches, and team codes to play on the same side as a friend | Built, not tested yet (needs 4 players online) |

## How it works

```
game ──► exvs-online proxy (127.0.0.1:28090) ──► card server (127.0.0.1:28080)
                 │
                 └──► match server: matchmaking (TCP 28090), STUN (UDP 3478), relay (UDP 3480)

game ◄──────────── game traffic, PC to PC (or through the relay) ────────────► opponent's game
```

1. **Card server.** Each player runs a local card server (`Server-Infinite`), which stores
   their card data. Boost Link doesn't replace it.
2. **Proxy.** `exvs-online` sits between the game and the card server. It passes card
   requests through unchanged and adds the match server's address to the game's
   registration reply, so the game knows where to search.
3. **Matchmaking.** When a player searches in online versus, the game talks to the match
   server using its own protocol (`nue.protocol.mms`, recovered from the game). The server
   puts players in a queue and pairs them.
4. **Connection.** The two games then connect to each other directly, as they do on arcade
   cabinets. STUN tells each game its public address. If a direct connection fails, the
   game falls back to the relay.

The match server only introduces players. Inputs and game state go between the two PCs.

## Requirements

- Windows 10 or 11 (64-bit).
- Your own copy of EXVS2 Infinite Boost (the 2026-04 GQuuuuuuX build; 1.04.27 may still work but is no longer tested), with its card server (`Server-Infinite`)
  and Live Monitor (`vsac29_Release_LM.exe`).
- The Microsoft Visual C++ 2010 runtime (the Live Monitor needs it; the launcher checks for it).
- A wired connection is recommended. Ping above 300 ms is rejected by the game.

## Getting started

1. Download `Boost-Link-<version>.zip` from the
   [latest release](https://github.com/psong-sys/boostlink/releases/latest) and extract it
   into your game folder (the one with `vsac29_Release.exe`). You get `exvs-launcher\` and
   `exvs-online\` next to the game. For English menus, also get the player pack from the
   Boost Link Discord; it's built from game files, so it isn't on GitHub.
2. Open `exvs-launcher\exvs-launcher.exe`. On first run it picks your network adapter, gives
   you unique card IDs and checks for the Visual C++ runtime.
3. On the **PLAY** tab, click **Check my PC** and fix anything marked FAIL. If it offers
   **Fix firewall**, click it and accept the Windows prompt.
4. Choose **Online** and press **PLAY**. The launcher starts the card server, the proxy, the
   Live Monitor (hidden) and the game, and closes them all when the game exits.
5. In game, insert your card, go to online versus and search.

The live queue is shown on the match server's home page, linked from the Discord.

## Troubleshooting

Each match attempt produces one `RESULT` line in `exvs-launcher\traffic.log`.

| Result | Meaning | What to try |
|---|---|---|
| `SUCCESS` | The games connected. | Nothing. |
| `NONE` / no opponent | Nobody else was searching. | Search at the same time as your opponent. |
| `FAIL_TIMEOUT` or `FAIL_TIMEOUT_NO_PACKETS` | Matched, but the PCs couldn't reach each other. | Run **Check my PC** and **Fix firewall** on both PCs. Check both use their real Ethernet or Wi-Fi adapter. |
| `FAIL_RTT_OUT_OF_LIMIT` | Ping was over 300 ms. | Use a wired connection. The match server can raise the limit. |
| `FAIL_CANCELED_SOMEONE` | The other player dropped out. | Read their `RESULT` line for the reason. |

**Blue screen while playing** (`VIDEO_MEMORY_MANAGEMENT_INTERNAL`, 0x10E): turn on **Vulkan mode** in the
launcher's SETTINGS > Graphics. It installs the official [vkd3d-proton](https://github.com/HansKristian-Work/vkd3d-proton)
and [DXVK](https://github.com/doitsujin/dxvk) next to the game (pinned versions, checked by SHA-256), so the game
draws through Vulkan instead of DirectX 12. Turning it off restores what was there. Check my PC counts recent
0x10E blue screens.

Logs are in `exvs-launcher\logs\`. Your original `config.ini`, `card.ini` and `lmnfig.ini`
are backed up in `exvs-launcher\backups\` before the launcher changes anything. More detail:
[`exvs-online/README.md`](exvs-online/README.md#reading-connection-results-what-went-wrong-in-a-match).

## Running your own match server

Players connect to the public Boost Link server by default. To run your own (for a region
or a private group), use the kit in [`server/`](server). A 1 CPU / 1 GB Linux VPS is enough.

| Port | Protocol | Purpose |
|---|---|---|
| 28090 | TCP | Matchmaking, home page and status page |
| 3478-3479 | UDP | STUN |
| 3480 | UDP | Relay (TURN) |
| 49160-49999 | UDP | Relay traffic (one port per relayed player) |

`server/install.sh` installs it as a systemd service. `server/harden.sh` adds a firewall,
automatic security updates and login protection. Players point the launcher at your server
under **SETTINGS > Advanced**.

## Repository layout

| Folder | Contents |
|---|---|
| [`launcher/`](launcher) | The Windows launcher (Go). Local web UI on 127.0.0.1, game setup, Check my PC, English card editor, self-update. |
| [`exvs-online/`](exvs-online) | The proxy and match server (Go). Matchmaking, STUN, relay, status page, pre-match checks, the result-screen fix. |
| [`server/`](server) | Linux install and hardening scripts and a sample server config. |
| [`english/`](english) | English patch tools: `exvs-english` (exe strings), `exvs-ui` (installs translated screens), and `repaint/` (builds those screens from your own game files). |
| [`docs/`](docs) | Reverse-engineering notes. |
| [`.github/workflows/`](.github/workflows) | The release workflow. |

## Building from source

Each folder is its own Go module. Go 1.24 or later is required.

```
cd launcher           && GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w -H windowsgui" -o exvs-launcher.exe .
cd exvs-online        && GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o exvs-online.exe .
cd english            && GOOS=windows GOARCH=amd64 go build -o exvs-english.exe .
cd english/exvs-ui    && GOOS=windows GOARCH=amd64 go build -o exvs-ui.exe .
```

Match server for Linux:

```
cd exvs-online && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ../server/exvs-online-linux-amd64 .
```

Run the tests with `go test ./...` in `launcher/` and `exvs-online/`.

## Releases and updates

Releases are built by GitHub Actions ([`release.yml`](.github/workflows/release.yml)), not
uploaded by hand. When `main` gets a new `version` in `launcher/main.go`, the workflow
builds `exvs-launcher.exe` and `exvs-online.exe` and publishes them as release
`v<version>`, together with a `latest.json` manifest listing each file's SHA-256.

The launcher checks that manifest when it opens. If a newer version exists, it offers
**Update now**, downloads the files over HTTPS, verifies their checksums and restarts.

Releases contain only these built files. Game files and the translated screen files, which
are derived from the game, are never committed or attached.

## Security

See [SECURITY.md](SECURITY.md) for how updates are trusted, what Boost Link exposes on your
network, the results of the November 2026 review, and how to report a problem privately.

## Game builds

Boost Link finds what it patches by content, not by fixed addresses, so a new game build
usually works without a Boost Link update. Run `exvs-online.exe -check` (or **Check my PC**)
to see which build you have and whether everything fits it.

| Game build | exe PE timestamp | Notes |
|---|---|---|
| 1.04.27 | 2025-08-19 | Protocol revision 131. The build Boost Link was first made on. |
| GQuuuuuuX update | 2026-04-17 | Protocol revision 141 (what `exvs-online/pb` is generated from now). Card server `server\Server.exe` (Server-Infinite 1.1.2). Renders with DirectX 12; use Vulkan mode if it blue-screens. Most screens ship in English already; the English set covers the rest. 21 debug strings of the English table aren't in it. Players on different builds aren't matched together. |

If a later build changes the code a patch needs, that patch refuses with "unsupported game
build" and changes nothing. If it changes the protocol, Check my PC says so ("Game protocol");
`go run ./pb/gen <path to vsac29_Release.exe>` in `exvs-online/` regenerates `pb/` from that exe.
