# Boost Link

Online versus for **EXVS2 Infinite Boost** on PC.

Boost Link puts players into the same matchmaking queue through a small match server, then
lets the game connect the two PCs directly, the way the arcade does. A built-in relay
covers routers that can't connect directly.

> A fan project, not affiliated with Bandai Namco or Sunrise. **This repository contains no
> game files.** Every player brings their own copy of the game (v1.04.27).

## Playing

Ask in the Boost Link Discord for the player pack, extract it into your game folder and
follow `START-HERE.txt`. After that, open the launcher, click **Check my PC**, pick
**Online** and press **PLAY**.

The launcher updates itself from this repo's [Releases](../../releases), so there's
nothing to download here by hand.

## What's in this repo

| Folder | What it is |
|---|---|
| [`launcher/`](launcher) | The Boost Link launcher (Windows). One click starts the card server, the online proxy, the Live Monitor and the game. Includes Check my PC and an English card editor. |
| [`exvs-online/`](exvs-online) | The online proxy and match server (Go). Sits between the game and the card server and handles matchmaking, STUN/TURN, the status page and the pre-match check. |
| [`server/`](server) | Kit for running your own match server on a $5 Linux VPS in about 15 minutes. |
| [`english/`](english) | English patch tools for the exe strings and the in-game screens, plus the `repaint/` toolkit used to build them. |
| [`docs/`](docs) | Reverse-engineering notes. |

## Building

Each folder is its own Go module (Go 1.24+):

```
cd launcher    && GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w -H windowsgui" -o exvs-launcher.exe .
cd exvs-online && GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o exvs-online.exe .
cd english     && GOOS=windows GOARCH=amd64 go build -o exvs-english.exe .
cd english/exvs-ui && GOOS=windows GOARCH=amd64 go build -o exvs-ui.exe .
```

Match server (Linux): `cd exvs-online && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ../server/exvs-online-linux-amd64 .`

Tests: `go test ./...` in `launcher/` and `exvs-online/`.

## Releases

Bump `version` in `launcher/main.go` and merge to `main`.
[`.github/workflows/release.yml`](.github/workflows/release.yml) then builds
`exvs-launcher.exe` and `exvs-online.exe` and publishes them with a `latest.json` manifest
as release `v<version>`. Launchers 1.5.0 and later check that manifest when they open and
offer **Update now**.

Releases only ever hold those built files. Game files and the translated screen files
(which are built from the game) are never committed or attached here.
