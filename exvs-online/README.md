# Boost Link: exvs-online

Part of **Boost Link**, a fan project. `exvs-online` is a small proxy and matchmaking server that tries to make **online versus** work in EXVS2 Infinite Boost on PC.
It sits between the game and `Server-Infinite.exe`. It doesn't change any existing files.

```
game ──► exvs-online (proxy, :28090) ──► Server-Infinite (:28080)   card data, unchanged
                    │
                    └── /match ──► match server (the host's exvs-online)   pairs players into rooms
```

What it does:
1. Passes every card server request through unchanged.
2. Adds the STUN, TURN and match server addresses to the game's `RegisterPcb` reply, which is how the game learns where they are.
3. Implements the game's matchmaking protocol (`nue.protocol.mms`, pulled out of `vsac29_Release.exe`): ISSUE_NODE_ID, ENTRY_MATCHING, CHECK_MATCHING, CANCEL_MATCHING and MATCHING_SETTING.
4. Writes every decoded request and response to `traffic.log`, so a failed test shows exactly where the game stopped.

**This is untested against the real game.** The message formats come from the game itself, but some behaviour is guesswork. The guesses are configurable (see "Knobs to try").

## Solo test: one PC, one fake opponent

This can't produce a real match, but it shows whether the game accepts each step:
registration, joining the queue and being told a match was found.

1. Copy `exvs-online.solo.json` to `exvs-online.json` and run `exvs-online.exe`. (If you use exvs-launcher, close it first, or start exvs-online yourself before the launcher, which reuses a running copy.)
2. In `config.ini`, set `Server =127.0.0.1:28090`.
3. Start the game, insert a card and pick online versus.
4. Expect it to search, "find" an opponent within a few seconds, then fail to connect, because the fake opponent doesn't exist. That failure is the success signal.
5. Send me `traffic.log` and a photo or the text of whatever error the game shows.

## First real test: 2 PCs on Radmin VPN (1v1)

Both PCs: put this folder anywhere (for example inside the game folder), keep `config.ini` set to `InterfaceName=Radmin VPN`, and start `Server-Infinite.exe` as usual.

**Host PC**
1. Copy `exvs-online.host.json` to `exvs-online.json`.
2. Run `exvs-online.exe`. Allow it through Windows Firewall when asked (private networks).
3. In the game's `config.ini`, change `Server =127.0.0.1:28080` to `Server =127.0.0.1:28090`.

**Friend PC**
1. Copy `exvs-online.friend.json` to `exvs-online.json` and replace `HOST_IP_HERE` with the host's Radmin IP.
2. Run `exvs-online.exe`.
3. Same `config.ini` change: `Server =127.0.0.1:28090`.

Then both of you start the game, insert a card and pick online versus. `roomSize` is 2 in the host config, so two solo players get matched against each other.

To undo everything, change `Server=` back to `28080` and close `exvs-online.exe`.

**What to send back after a test:** `traffic.log` from both PCs, plus how far the game got and any error code on screen.

## Knobs to try (in `match` in the host's config)

| Setting | Default | Why it might need changing |
|---|---|---|
| `roomSize` | 2 (host test file), 4 (default) | 4 for real 2v2. A pair on one entry counts as 2. |
| `groupIdBase` | 0 | Team ids might start at 1 instead. Try 1 if the match forms but teams come out wrong or it errors. |
| `waitCode` | `SUCCESS` | The reply while still waiting for players. Ghidra shows the game treats any error code here as a failure, so leave this unless a log says otherwise. |
| `testBots` | 0 | Solo testing only: adds this many fake players to each room. |
| `fallbackAfterSeconds` | 0 (off) | Lets a smaller room (`fallbackRoomSize`) start after waiting this long. |

## Live status page

Whoever runs the match server can open **`http://<match server>:28090/status`** in a browser (or `:28100/status` when it runs in `match` mode). It shows how many players are searching, how long they've waited and the recent matches, and refreshes every 5 seconds. `/status.json` has the same data for a website or a Discord bot. It shows no IPs or card serials.

## Built-in relay (TURN), for when two players match but can't connect

Some home routers block the direct game connection. The game then falls back to a relay (TURN) server.
exvs-online has one built in. It's off by default. To turn it on:

- **Host's config:** add
  `"localTurn": { "listen": "0.0.0.0:3480", "relayIP": "auto" }` and `"turn": { "host": "auto" }` inside `"proxy"`.
  `relayIP: auto` finds the host's public IP through STUN. The password is random each run.
- **Each player's config:** add `"turn": { "host": "auto" }` inside `"proxy"`. Their exvs-online fetches the relay details from the host (`/turninfo`).
- **Host's router:** forward **UDP 3480 and UDP 49160-49200** to the host PC, and allow them in Windows Firewall. A rented server needs no forwarding, only those ports open.

traffic.log shows `TURN>` lines when a game uses the relay.

## Pre-match check ("Check my PC")

Before you queue, click **Check my PC** in the launcher, or double-click `Check my PC.cmd` in
this folder. It checks the game files, the Visual C++ runtimes, the network adapter, online
mode, your VPN, Windows Firewall, your NAT type, and whether the host answers. Anything that
needs fixing shows a FAIL or WARN with what to do. If the firewall is the problem it offers to
add the rules for you (Windows asks for admin permission once). The result is also saved to
`check-report.txt`, so you can send it.

Command line: `exvs-online.exe -check [-json] [-config <file>] [-game <folder>]`, and
`exvs-online.exe -fix-firewall [-game <folder>]`.

## Small-scene matchmaking options (host config, `match` section)

| Key | Default | What it does |
|---|---|---|
| `rttLimitMs` | 0 (card server's 300) | Ping limit sent to every player's game. The host decides it for everyone, so both sides agree. Raise it (for example 450) to let far-apart players match, at the cost of more input delay. |
| `delayFrameLimit` | 0 (card server's 5) | The game's input-delay frame limit. Raise together with `rttLimitMs` if far matches still drop. |
| `retryFailedPairAfterSeconds` | 45 | When two players fail to connect, they're paired with someone else first. If nobody else shows up within this many seconds, they're tried again. |
| `discordWebhook` | empty | A Discord webhook link. When the queue goes from empty to someone searching, it posts "Someone just started searching…" (at most once per `discordCooldownMinutes`, default 10). No names, serials or IPs are sent. |
| `publicStatusUrl` | empty | Added to the Discord post so people can open the live status page. |

All players already share one queue; only different game versions are kept apart.

## Reading connection results (what went wrong in a match)

After every match try, the game reports how the connection went. exvs-online turns that
into one `RESULT` line in the console window and in `traffic.log`, on each PC. A player's
proxy also sends its result (no IPs or serials) to the host, so the host's status page
(`http://<host>:28090/status`) shows both sides under "Connection results".

| Result | What it means | What to do |
|---|---|---|
| `SUCCESS` | The two games connected. | Nothing. If the fight still doesn't start, send both `live-monitor.log` files. |
| no opponent / `NONE` | Nobody was matched on that try. | Both search at the same time, with the same host IP. Check the status page shows both of you queued. |
| `FAIL_TIMEOUT` | Matched, but the games couldn't reach each other in time. Our solo test against the fake opponent shows this, which is expected there. | Allow `vsac29_Release.exe` through Windows Firewall (private and public) on both PCs. Then try both PCs on the ZeroTier adapter. Then turn on the relay (section above). |
| `FAIL_TIMEOUT_NO_PACKETS` | Matched, but not one packet arrived from the other player. | Same as above, and check both PCs picked the right adapter in the launcher. |
| `FAIL_RTT_OUT_OF_LIMIT` | Connected, but ping was over the game's 300 ms limit, so it dropped the match. | The route is too slow. Avoid a relayed VPN path; a closer host helps. |
| `FAIL_CANCELED_MYSELF` | This PC stopped the attempt (search cancelled or timer ran out). | Only a problem if it repeats; then queue together again. |
| `FAIL_CANCELED_SOMEONE` | The other player dropped out first. | Read the other PC's RESULT line, it has the real reason. |
| `FAIL_OTHERS` | The game didn't say why. | Send both `traffic.log` and `live-monitor.log`. |

The NAT part of the line says how hard your router is to connect through. "easy NAT" is
the good case. "strict NAT" on both PCs usually means hole punching can't work and the
relay is needed. "mapping not measured" is what our STUN server reports and is normal.

The 300 ms limit comes from the card server (`thresholdDelayedRtt` in its LOAD_BLACK_LIST
reply), so it can be raised there if ping turns out to be the only problem.

## Playing over the internet without a VPN (later)

- Host runs `exvs-online.exe match` on a reachable server (port 28100 open). Everyone else runs proxy mode with `matchServer` set to `http://SERVER_IP:28100`.
- For players behind strict routers, run coturn with `coturn/turnserver.conf` (UDP 3478 plus 49160 to 49200 open) and add the `turn` block to each proxy config:
  `"turn": { "host": "SERVER_IP", "port": 3478, "user": "exvs", "pass": "CHANGE_ME" }`
- Set `InterfaceName` in `config.ini` to your real network adapter (for example `Ethernet`) instead of Radmin.

## Building from source

Go 1.24+: `GOOS=windows GOARCH=amd64 go build -o exvs-online.exe .` and `go test .` for the tests.
The `pb/` packages were generated from the protobuf descriptors embedded in `vsac29_Release.exe` (v1.04.27).
