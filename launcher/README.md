# Boost Link launcher (exvs-launcher)

One window to set up and start EXVS2. Press **PLAY** and it:

1. Saves your settings into the game's `config.ini` and `card.ini` (backing them up first).
2. Starts the card server (`Server\Server-Infinite.exe`) in the background, with no console window.
3. In online modes, also starts `exvs-online.exe` in the background, set up as host or as a friend joining the host.
4. Starts the Live Monitor (`vsac29_Release_LM.exe`), hidden and muted by default, and gives it 6 seconds to come up. The game needs it running, or it stops at "LIVEMONITOR DISCONNECT".
5. Starts the game.
6. When you close the game, it closes the Live Monitor, the card server and exvs-online too.

It never touches the game's own files. The only files it changes are `config.ini`, `card.ini`, `lmnfig.ini` and, in online modes, the battle mode setting in `29\testmode_pcb.json` and `29\testmode_lm.json`. The network adapter setting is written to both `config.ini` and `lmnfig.ini`, because the Live Monitor fails with "failed to find interface" when they differ. The mode sets `Server=` in both files too (28090 online, 28080 offline), so the Live Monitor also goes through exvs-online.

In Host and Join a friend modes, the launcher also switches the game's battle mode to online before starting the Live Monitor. It's the same setting as Live Monitor F3 → GAME PCBs SETTING → Battle mode = Online, stored in `29\testmode_pcb.json` and `29\testmode_lm.json`. It changes only that value in the active setting file, backs both files up to `backups\` first, and logs the old value. Offline mode doesn't touch it.

The Live Monitor setting can hide it, minimize it, show it normally, or not start it. Muting uses Windows' per-app volume, so the Live Monitor also shows as muted in the Windows volume mixer.

## What's in the window

- **PLAY tab**: pick Offline, Host or Join. Host shows your Radmin/ZeroTier IP with a Copy button. Join remembers the last hosts you joined. The status panel lights up each step (card server, online, Live Monitor, game).
- **PILOT tab**: card IDs, "Make my IDs unique", and **Edit card & pilot name**, which opens the card server's own editor (pilot name, favorite suits, stickers, BGM). It starts the card server if needed.
- **SETTINGS tab**: network adapter (a list of this PC's adapters, with the recommended one marked), display, Live Monitor, desktop shortcuts.
- **CONTROLS tab**: every keyboard and controller key.
- **First run**: a short welcome sets the adapter, gives you your own card IDs, and checks for the Visual C++ 2010 runtime the Live Monitor needs.
- **Check my PC** (PLAY tab): runs exvs-online's pre-match checks (firewall, runtime, network and so on). It lists each result with a suggested fix, and has a **Fix firewall** button that adds the needed Windows Firewall rules after an admin prompt. Needs an exvs-online.exe that supports `-check`.
- Warnings at the top have one-click fixes (wrong adapter, default card IDs).

## Try it

1. Keep this folder where it is: `exv2\exvs-launcher\` (it looks for the game one folder up).
2. Double-click `exvs-launcher.exe`. A settings window opens.
3. Check the settings, then press **PLAY**. The status box shows each step as it happens.
4. Close the game. The status should say "Game closed. Everything is shut down."

Close the settings window when you're done. The launcher quits on its own a few seconds later (or up to 3 minutes later if Windows doesn't tell it the window closed).

**True one click:** press **Add desktop shortcuts**. You get "Boost Link Play", which starts the game straight away with your saved settings (no window), and "Boost Link", which opens the settings.

## Modes

| Mode | What it does | config.ini `Server=` |
|---|---|---|
| Offline | Same as before: card server only. | `127.0.0.1:28080` |
| Host online | Runs exvs-online as host. The window shows your Radmin IP to give your friends. | `127.0.0.1:28090` |
| Join a friend | Runs exvs-online pointed at the host's Radmin IP. | `127.0.0.1:28090` |

Online modes need `exv2\exvs-online\exvs-online.exe`. The launcher reads that folder's `exvs-online.host.json` or `exvs-online.friend.json` as a template and writes its own copy to `exvs-launcher\exvs-online.json`, so the exvs-online folder is never changed. exvs-online's `traffic.log` ends up in this folder.

## Player IDs

For online play, every player needs a different machine serial, chip ID and access code. **Make my IDs unique** makes new random ones in the same format as your current ones. New card IDs mean a new, empty card on your card server. Your old card isn't deleted: type the old values back in to use it again (they are also in `backups\card.ini.original`).

## If something goes wrong

- `logs\launcher.log`: every step the launcher took.
- `logs\card-server.log`, `logs\exvs-online.log` and `logs\live-monitor.log`: what those programs printed.
- `backups\`: `config.ini.original`, `card.ini.original` and `lmnfig.ini.original` are your files from before the launcher's first change, plus a timestamped copy before every later change. Copy one back into the game folder to undo.
- If the card server, exvs-online or the Live Monitor is already running when you press PLAY, the launcher uses the running one and leaves it running afterwards.

## Building from source

Source is in `src\`. With Go 1.24+, from `src\`:

```
GOOS=windows GOARCH=amd64 go build -ldflags "-H=windowsgui -s -w" -o ..\exvs-launcher.exe .
go test .
```
