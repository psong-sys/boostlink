# exvs-english

English patch for EXVS2 IB (vsac29_Release.exe and vsac29_Release_LM.exe). Made on 1.04.27; the screen set (`ui-en`) is rebuilt for the 2026-04 build, which already ships most screens in English.

## What it translates

The text that lives inside the exe: about 600 strings (812 spots), including
- the Live Monitor GAME PCBs SETTING editor (battle mode 店外 = Online, 店内 = Local, etc.), LIVEMONITOR OPTIONS, the tournament booking screens and the confirm dialogs
- the test-menu dialogs ("exit and switch settings?", time rental resume, remote mode warnings)
- network, update, maintenance and error messages, matching/NAT status, button assignment names
- the developer settings menu and debug overlays

Menu headings like TEST MENU and the error codes were already English.

In-game player screens (mode select banners, card prompts, results) live in the encrypted
data\x64\dplcache_release\*.fhm2d assets, not in the exe. `exvs-ui/` installs translated copies of those,
and `repaint/` is the toolkit that builds them from your own game files. The translated files themselves
are never committed here.

## Use

Put exvs-english.exe in an `exvs-english` folder inside the game folder and double-click it. Close the game and Live Monitor first.
- `install`: backs up both exes to exvs-english\backup\ (first time only), then writes English into them in place. The launcher works unchanged.
- `restore`: puts the Japanese originals back from the backup.
- `copies`: leaves the originals alone and writes vsac29_Release_EN.exe / vsac29_Release_LM_EN.exe.
- `status`: shows which version each exe is.

It finds each string by its content, so it works on newer game builds: strings a build no longer has are skipped and counted.
It refuses an exe that has fewer than 80% of the strings. Where it wrote is saved next to the backup (`<exe>.english.json`), so restore never guesses.

## Source

This folder holds the Go source, patches.json (offset, Japanese, English) and translations.py (the translation table).
Rebuild: `GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o exvs-english.exe .`
