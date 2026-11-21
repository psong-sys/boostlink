# Translation rules: EXVS2 Infinite Boost arcade message table (Japanese -> English)

Input: a JSON list of {"key": ..., "ja": ...}. Output: a JSON object {key: english} covering EVERY key, written UTF-8.

Context: Mobile Suit Gundam Extreme VS. 2 Infinite Boost, a Japanese arcade game. Keys hint at the screen
(PCB_/CARD_ = card reader, GM_ = game mode select, LM_ = Live Monitor operator menus, TUTO_ = tutorial,
PRC_ = Practice, RB_ = Route Battle, TRAIN_ = Training, TK_/TAIKAI = tournament, P_ACHIEVE = achievements,
CHR_/WAZA = units/moves, REGION = Japanese prefectures).

Hard rules (the game breaks otherwise):
1. Control codes start with U+0010 (\u0010 in JSON). Copy every one exactly, with what follows it:
   \u0010C1 ... \u0010Cr (colour on/off), \u0010c00CCFF (hex colour), \u0010F2 ... \u0010Fr (button glyph font),
   \u0010[NAME] placeholders (keep the bracketed name exactly, e.g. \u0010[COIN], \u0010[pname1]).
   Keep the same count and order of codes; move them only if English word order needs it.
2. Keep the same number of lines (\n) where practical, and never more than 1 extra line.
   Each English line should be at most about 2x the number of characters in the matching Japanese line
   (Japanese chars are double width). Shorten wording rather than overflow.
3. Use only printable ASCII, plus these symbols if they appear in the source: ※ ○ ● ★ ☆ ← → ↑ ↓ 【 】 「 」 · … ×.
   Replace full-width spaces (　) with two ASCII spaces, full-width digits/letters/punctuation with ASCII.
   Keep leading spaces if the Japanese line starts with spaces.
4. Keep %d %s %f %zu style format specifiers exactly.
5. Text that is already English, numbers only, or symbols only: return unchanged.
6. Placeholder/dummy strings (e.g. "...ダミーテキスト0", "〇〇〇"): translate literally and short ("Dummy text 0").

Terminology (be consistent): 僚機 = wingman (or partner), 出撃 = sortie, 機体 = unit, 覚醒 = Awakening,
EXバースト = EX Burst, ブースト = Boost, 射撃 = Shoot / shooting, 格闘 = Melee, 特殊射撃 = Special Shot,
特殊格闘 = Special Melee, サブ射撃 = Sub Shot, コスト = cost, 撃破 = defeat / shoot down, 被撃破 = times defeated,
シャッフル = Shuffle, チーム戦 = Team battle, 対戦 = battle / versus, 店内 = in-store, CPU戦 = CPU battle,
ルートバトル = Route Battle, プラクティス = Practice, トレーニング = Training, チュートリアル = Tutorial,
タイムレンタル = Time Rental, クレジット = credit, カードをタッチ = touch your card, 筐体 = cabinet,
運営メニュー = operator menu, 大会 = tournament, グループプレイ = Group Play, リプレイ = replay,
称号 = title, ランク/クラス/グレード = rank/class/grade, 戦績 = record, 連勝 = win streak,
Bandai Namco Passport and VS.モバイル (= VS. Mobile) stay as names. Unit and character names: use the official
English Gundam spelling (e.g. ガンダム = Gundam, ザク = Zaku, νガンダム = Nu Gundam, シャア = Char).
Prefectures: standard romanisation (東京都 = Tokyo, 北海道 = Hokkaido).

Style: short, clear arcade UI English. Sentence case for messages, Title Case for menu items and titles.
Do not add explanations or notes. Do not skip any key.

Validate before finishing: run
  python3 /tmp/claude-0/-home-user/c2208f31-42f0-573b-b6cf-14a0fcad2a81/scratchpad/msg/validate.py <input chunk> <your output>
and fix everything it reports until it prints OK.
