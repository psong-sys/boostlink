# EXVS2 test menu: switching to online (English guide)

All text below was read straight out of vsac29_Release.exe (1.04.27).

## Where the setting is

The battle mode (対戦モード) is **not** in the game window's F7 menu. The game (client) TEST MENU only has
GAME PCB ID, COIN OPTIONS, GAME OPTIONS, DISPLAY TEST, SOUND TEST, I/O TEST, CARD R/W TEST,
NETWORK STATUS, BOOKKEEPING, OTHERS, EXIT & SAVE.

It is in the **Live Monitor** window (vsac29_Release_LM.exe). Its test key is **F3** (lmnfig.ini `Test=F3`).
The LM TEST MENU has: LIVEMONITOR OPTIONS, CLOSE TIME SETTING, DISPLAY TEST, SOUND TEST, I/O TEST,
CARD R/W TEST, NETWORK STATUS, BOOKKEEPING, OTHERS, EXIT & SAVE, SOFTWARE UPDATE, **GAME PCBs SETTING**.

Keys in the LM window (lmnfig.ini): arrows move, Left/Right change a value, Z = BUTTON1 (enter), X = BUTTON2 (back).

## Steps

1. Focus the LM window, press **F3**, pick **GAME PCBs SETTING**.
2. Prompt: この項目に入ると全ての筐体（基板）が強制的にリモートモードに切り替わります。よろしいですか？
   = "Entering this forces all cabinets (boards) into remote mode. OK?" → **はい** (Yes).
3. You get a list of settings files (設定ファイル1, 2, ...).
   先頭に「*」がついているものが現在使用されている設定ファイルです = "The file marked * is the one in use. Pick a file."
   Pick the * file. Its sub-menu:
   - このファイルの設定を有効にする = make this file active
   - このファイルを編集する = edit this file
   - このファイルを削除する = delete this file
4. **Edit** the file and set **対戦モード: 店外・ソロ/チーム** (out-of-store = online, solo/team). Back out with X.
5. If you edited a file without a *, go back and choose **このファイルの設定を有効にする** on it.
6. Check the schedule (スケジュール切り替え) so it doesn't switch back: 開店時 = at opening,
   設定切替１ / 設定切替２ = switch 1 / switch 2, per weekday 月火水木金土日 (Mon..Sun), なし = none, %d時 = hour.
7. Back to TEST MENU → **EXIT & SAVE**. Prompt: テストモードを抜けると設定切り替えのため、上記のゲーム基盤が再起動します。よろしいですか？
   = "Leaving test mode restarts the boards above to switch settings. OK?"
   - いいえ ( テストモードに戻る ) = No, back to test mode
   - いいえ ( テストモードを終了 : 設定は切り替えない ) = No, exit WITHOUT switching
   - **はい ( テストモードを終了 : 設定を切り替える ) = Yes, exit AND switch** ← pick this
8. The client restarts. The next SaveInsideData in traffic.log should show MODE_NORMAL_ONLINE.

## 対戦モード (battle mode) values

| Japanese | English | proto enum (best guess) |
|---|---|---|
| 店外・ソロ/チーム | Online, solo/team | MODE_NORMAL_ONLINE + TEAM_ONLINE_SOLO_OR_TEAM |
| 店内・ソロ/チーム | In-store, solo/team | MODE_NORMAL_LOCAL + TEAM_LOCAL_SOLO/TEAM |
| イベント・ソロ | Event, solo | MODE_EVENT |
| イベント・チーム | Event, team | MODE_EVENT |
| イベント・チームホールド | Event, team hold | MODE_EVENT + TEAM_LOCAL_HOLD |
| 店内・セット貸し | In-store, set rental | MODE_RENTAL |
| 大会モード | Tournament mode | MODE_MEET |

## Other fields in the settings-file editor

| Japanese | English |
|---|---|
| ゲーム基板管理数 | Game boards managed |
| リンクグループ数 | Link groups |
| 用途 | Purpose (標準 standard / グループプレイ group play / 大会 tournament) |
| 基板接続数 | Boards connected |
| 対戦モード | Battle mode |
| タイムレンタル | Time rental |
| 時間設定(店内対戦) | Time setting (in-store battle) |
| 対戦ルール(イベント) | Battle rule (event) |
| ステージ選択(イベント) | Stage select (event): フリー free / ランダム限定 random only |
| 対戦待機(イベント・ソロ) | Battle wait (event solo): 4人 4 players / 全員 everyone |
| フリープレイ | Free play |
| 【全ゲーム基盤の共通設定】 | [Shared by all boards] |
| アトラクトサウンド | Attract sound (ON / OFF / HALF) |
| イベント系対戦BGM設定 | Event battle BGM |
| START / PLUS COIN / CONTINUE COST | cost per start / extra coin / continue |
| おすすめ設定をロードする | Load recommended settings (asks: for how many boards? %d台 = N boards) |
| 店舗指定マッチング設定 | Store-specified matching (leave alone) |

Errors you may hit in this editor:
- 基板接続数が4の倍数でなければ対戦モードを「イベント」に、また4でなければ「店内・セット貸し」に設定できません。
  = "Event needs a board count that is a multiple of 4, and set rental needs exactly 4." (Online has no such rule.)
- 店内のネットワーク環境に問題があるため、オンライン対戦ができません。
  = "Online battle unavailable because of a problem with the store network." (game-side, means online checks failed)
