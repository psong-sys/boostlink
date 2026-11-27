# Boost Link launcher (exvs-launcher)

One window to set up and start EXVS2. Press **PLAY** and it:

1. Saves your settings into the game's `config.ini` and `card.ini` (backing them up first).
2. Starts the card server (`Server\Server-Infinite.exe`) in the background, with no console window.
3. In online modes, also starts `exvs-online.exe` in the background, set up as host or as a friend joining the host.
4. Starts the Live Monitor (`vsac29_Release_LM.exe`), hidden and muted by default, and gives it 6 seconds to come up. The game needs it running, or it stops at "LIVEMONITOR DISCONNECT".
5. Starts the game.
6. When you close the game, it closes the Live Monitor, the card server and exvs-online too.

It never touches the game's own files. The only files it changes are `config.ini`, `card.ini`, `lmnfig.ini` and, in online modes, the battle mode setting in `29\testmode_pcb.json` and `29\testmode_lm.json`. The network adapter setting is written to both `config.ini` and `lmnfig.ini`, because the Live Monitor fails with "failed to find interface" when they differ. The mode sets `Server=` in both files too (28090 online, 28080 offline), so the Live Monitor also goes through exvs-online.

In Online (and Host) mode, the launcher also switches the game's battle mode to online before starting the Live Monitor. It's the same setting as Live Monitor F3 → GAME PCBs SETTING → Battle mode = Online, stored in `29\testmode_pcb.json` and `29\testmode_lm.json`. It changes only that value in the active setting file, backs both files up to `backups\` first, and logs the old value. Offline mode doesn't touch it.

The Live Monitor setting can hide it, minimize it, show it normally, not start it, or use the **Light Live Monitor** instead. Light runs `exvs-online -light-lm`, which answers the game the way the real Live Monitor does, so no second copy of the game runs. If the game hasn't linked to it 25 seconds after starting, the launcher closes the game, starts the real Live Monitor, starts the game again and switches the setting back to "hidden". Muting uses Windows' per-app volume, so the Live Monitor also shows as muted in the Windows volume mixer.

## What's in the window

- **PLAY tab**: pick Offline or Online. Online connects to the public Boost Link match server. The status panel lights up each step (card server, online, Live Monitor, game).
- **PILOT tab**: a card editor in English, with these sub-tabs:
  - **Card IDs**: card IDs and "Make my IDs unique".
  - **Profile**: pilot name, title text, Quick Start, BGM safe mode, fixed radar, your BGM list and gauge design.
  - **Favorite suits**: up to 12 suits, each with its burst type.
  - **Comms**: start, end and direction messages for solo, team and quick-team play, plus the comms options.
  - **Sticker**: opening sticker background and effect (with a preview), stats, comment, and the pilot picture options.
  - **Buttons**: the game's own controller button layout (Normal Type (Custom)).

  It reads and saves through the card server's own web API on 127.0.0.1:28080, starting the card server if needed, and keeps any card fields it doesn't show. Before every save it copies `Server\ServerInfinite.db` once per run, and the part being changed every time, to `backups\card\`. Saving is blocked while the game is running. Play once first so the card exists.
- **SETTINGS tab**: network adapter (picked automatically; VPN and virtual adapters such as Radmin and ZeroTier are hidden unless you tick "Show all adapters"), display, Live Monitor, desktop shortcuts.
- **Graphics** (SETTINGS tab): **Vulkan mode** puts the official vkd3d-proton (`d3d12.dll`, `d3d12core.dll`) and DXVK (`dxgi.dll`) next to the game so it draws through Vulkan instead of DirectX 12. Use it if the PC blue-screens with VIDEO_MEMORY_MANAGEMENT_INTERNAL while playing. The archives come from the projects' GitHub releases at pinned versions and are checked by SHA-256, then kept in `vulkan-cache\`. Files it replaces go to `vulkan-backup\` and come back when you turn it off; `vulkan.json` records what it installed.
- **Advanced** (SETTINGS tab): tick "Show advanced options" to change the match server address (IP or name) and to show **HOST** on the PLAY tab, where one PC runs the match server for a group. "Use the Boost Link server" puts the default back.
- The window is one fixed size with no scroll bar.
- **CONTROLS tab**: every keyboard and controller key.
- **First run**: a short welcome sets the adapter, gives you your own card IDs, and checks for the Visual C++ 2010 runtime the Live Monitor needs.
- **Check my PC** (PLAY tab): runs exvs-online's pre-match checks (firewall, runtime, network and so on). It lists each result with a suggested fix, and has a **Fix firewall** button that adds the needed Windows Firewall rules after an admin prompt. Needs an exvs-online.exe that supports `-check`.
- **Updates**: when a new version is published, a banner on the PLAY tab offers **Update now**. It downloads the new launcher and exvs-online, checks each file's SHA-256, replaces them (never anything outside `exvs-launcher\` and `exvs-online\`), and restarts the launcher.
- Warnings at the top have one-click fixes (wrong adapter, default card IDs).

## Using it

1. Keep this folder where it is: `exv2\exvs-launcher\` (it looks for the game one folder up).
2. Double-click `exvs-launcher.exe`. A settings window opens.
3. Check the settings, then press **PLAY**. The status box shows each step as it happens.
4. Close the game. The status should say "Game closed. Everything is shut down."

Close the settings window when you're done. The launcher quits on its own a few seconds later (or up to 3 minutes later if Windows doesn't tell it the window closed).

**Desktop shortcuts:** press **Add desktop shortcuts**. You get "Boost Link Play", which starts the game straight away with your saved settings (no window), and "Boost Link", which opens the settings.

## Modes

| Mode | What it does | config.ini `Server=` |
|---|---|---|
| Offline | Same as before: card server only. | `127.0.0.1:28080` |
| Online | Runs exvs-online pointed at the Boost Link server (or the address set under Advanced). | `127.0.0.1:28090` |
| Host (Advanced) | Runs exvs-online as the match server for a group. | `127.0.0.1:28090` |

Online modes need `exv2\exvs-online\exvs-online.exe`. The launcher reads that folder's `exvs-online.host.json` or `exvs-online.friend.json` as a template and writes its own copy to `exvs-launcher\exvs-online.json`, so the exvs-online folder is never changed. exvs-online's `traffic.log` ends up in this folder.

## Player IDs

For online play, every player needs a different machine serial, chip ID and access code. **Make my IDs unique** makes new random ones in the same format as your current ones. New card IDs mean a new, empty card on your card server. Your old card isn't deleted: type the old values back in to use it again (they are also in `backups\card.ini.original`).

## If something goes wrong

- `logs\launcher.log`: every step the launcher took.
- `logs\card-server.log`, `logs\exvs-online.log` and `logs\live-monitor.log`: what those programs printed.
- `backups\`: `config.ini.original`, `card.ini.original` and `lmnfig.ini.original` are your files from before the launcher's first change, plus a timestamped copy before every later change. Copy one back into the game folder to undo.
- If the card server, exvs-online or the Live Monitor is already running when you press PLAY, the launcher uses the running one and leaves it running afterwards.

## Building from source

With Go 1.24+, from this folder:

```
GOOS=windows GOARCH=amd64 go build -ldflags "-H=windowsgui -s -w" -o exvs-launcher.exe .
go test .
```
