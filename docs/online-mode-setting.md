# Switching EXVS2 to online mode without the F3 menu

The operator settings live in `exv2\29\testmode_pcb.json` (client) and `exv2\29\testmode_lm.json` (Live Monitor).
Both are written by nbamsavdat.dll ("svd1" container). Changing the setting in the LM's F3 menu rewrote both files (06:56/06:57 UTC on 2026-11-21).

## Container format (verified on 3 files)
| offset | size | field |
|---|---|---|
| 0x00 | 4 | magic `svd1` |
| 0x04 | 4 | u32 write counter (the library writes old+1) |
| 0x08 | 8 | u64 payload size |
| 0x10 | 20 | SHA-1 of (bytes 0x00..0x0F + payload) |
| 0x24 | n | payload: UTF-8 JSON `{"storage": {key: {"value": v, "type": 1}}, "format_version": ...}` |

To edit: parse JSON, change values, re-serialize, set the counter to old+1, set size = len(payload), recompute SHA-1 over the new 16-byte header + payload.

## The setting
- Active file: `.TestModeParam.gameServerPcb.useSettingFile` (0 on Paul's PC = "設定ファイル1").
- Battle mode: `.TestModeParam.gameServerPcb.settingFile.[N].group.[G].battleMode` for G = 0..2.
- Enum from the exe's option list (0x15d47d8): 0 店外・ソロ/チーム (ONLINE), 1 店内・ソロ/チーム (in-store), 2 event solo, 3 event team, 4 event team hold, 5 in-store set rental, 6 tournament.
- Paul's switched PC: file [0] groups 0..2 battleMode = 0. File [4] = 6 (a tournament preset, leave it alone).
- Assumption to verify: a fresh install has battleMode 1 there. Confirm with a friend's untouched file before relying on it.

## Plan for the launcher
Before starting the LM, for both files: if settingFile[useSettingFile].group[0..2].battleMode != 0, back up the file, set it to 0, and rewrite it per the format above. Only when the game is not running.

Safer edit: don't re-serialize the JSON. Replace only the `"value": N` that follows each battleMode key in the original text, so formatting and key order stay byte-identical apart from the digit.
