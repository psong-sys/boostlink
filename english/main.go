// exvs-english: puts English text into the EXVS2 test menus, Live Monitor
// settings, dialogs and status/error messages.
//
// It swaps Japanese UTF-8 strings inside vsac29_Release.exe and
// vsac29_Release_LM.exe for English ones of equal or shorter length.
// Originals are copied to exvs-english\backup\ before anything is written.
//
// Strings are found by their content, not by fixed offsets, so the same table
// works on newer game builds: every whole (NUL-bounded) copy of a Japanese
// string in .rdata is replaced. Strings a build no longer has are skipped and
// counted. Where each string was written is recorded next to the backup, so
// restore never has to guess.
//
//	exvs-english.exe            ask what to do (install / restore)
//	exvs-english.exe install    patch both exes in place (backup first)
//	exvs-english.exe restore    put the Japanese originals back
//	exvs-english.exe copies     leave originals alone, write *_EN.exe copies
//	exvs-english.exe status     show whether each exe is Japanese or English
package main

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// patches.json lists every Japanese string with its English text. The offsets
// are where each copy sits in the 1.04.27 exe; they are only used to recognise
// exes that an older exvs-english (which wrote by offset) already patched.
//
//go:embed patches.json
var patchesJSON []byte

type patch struct {
	Offset int64  `json:"offset"`
	JP     string `json:"jp"`
	EN     string `json:"en"`
}

// pair is one string to translate, wherever it appears.
type pair struct{ JP, EN string }

var exeNames = []string{"vsac29_Release.exe", "vsac29_Release_LM.exe"}

// minCoverage is the share of the string table a build must contain for
// install to go ahead. A different game (or a badly damaged exe) has far fewer.
const minCoverage = 0.80

type state int

const (
	stJapanese state = iota
	stEnglish
	stUnknown
)

func (s state) String() string {
	return [...]string{"Japanese (original)", "English (patched)", "unknown version"}[s]
}

func loadPatches() []patch {
	var ps []patch
	if err := json.Unmarshal(patchesJSON, &ps); err != nil {
		panic(err)
	}
	return ps
}

func pairsOf(ps []patch) []pair {
	seen := map[string]bool{}
	var out []pair
	for _, p := range ps {
		if !seen[p.JP] {
			seen[p.JP] = true
			out = append(out, pair{p.JP, p.EN})
		}
	}
	return out
}

// slot returns the bytes a patch writes: the English text padded with NULs
// to the Japanese length (the terminator after it is left alone).
func slot(jp, en string) []byte {
	b := make([]byte, len(jp))
	copy(b, en)
	return b
}

func (p patch) slot() []byte { return slot(p.JP, p.EN) }

// spot is one place a string was written.
type spot struct {
	Offset int64  `json:"offset"`
	JP     string `json:"jp"`
	EN     string `json:"en"`
}

// record is what install writes next to the backup: which exe it patched and where.
type record struct {
	Timestamp uint32 `json:"peTimestamp"`
	Size      int    `json:"size"`
	Spots     []spot `json:"spots"`
	Missing   int    `json:"missingStrings"`
}

// peInfo returns the exe's PE TimeDateStamp and its .rdata file range.
func peInfo(d []byte) (ts uint32, lo, hi int, err error) {
	if len(d) < 0x400 || d[0] != 'M' || d[1] != 'Z' {
		return 0, 0, 0, errors.New("not an exe")
	}
	pe := int(binary.LittleEndian.Uint32(d[0x3c:]))
	if pe+24 > len(d) || !bytes.Equal(d[pe:pe+4], []byte("PE\x00\x00")) {
		return 0, 0, 0, errors.New("not a PE file")
	}
	ts = binary.LittleEndian.Uint32(d[pe+8:])
	n := int(binary.LittleEndian.Uint16(d[pe+6:]))
	sec := pe + 24 + int(binary.LittleEndian.Uint16(d[pe+20:]))
	for i := 0; i < n && sec+40*(i+1) <= len(d); i++ {
		s := d[sec+40*i:]
		if string(bytes.TrimRight(s[:8], "\x00")) == ".rdata" {
			raw := int(binary.LittleEndian.Uint32(s[20:]))
			size := int(binary.LittleEndian.Uint32(s[16:]))
			if raw+size <= len(d) {
				return ts, raw, raw + size, nil
			}
		}
	}
	return 0, 0, 0, errors.New("no .rdata section")
}

// findSpots returns every whole copy of each Japanese string in .rdata, and how
// many strings of the table weren't found at all.
func findSpots(d []byte, pairs []pair) (spots []spot, missing int, err error) {
	_, lo, hi, err := peInfo(d)
	if err != nil {
		return nil, 0, err
	}
	for _, p := range pairs {
		key := append(append([]byte{0}, p.JP...), 0)
		n := 0
		for i := lo; ; {
			j := bytes.Index(d[i:hi], key)
			if j < 0 {
				break
			}
			spots = append(spots, spot{int64(i + j + 1), p.JP, p.EN})
			n++
			i += j + 1
		}
		if n == 0 {
			missing++
		}
	}
	return spots, missing, nil
}

func spotsHold(d []byte, sp []spot, english bool) bool {
	if len(sp) == 0 {
		return false
	}
	for _, s := range sp {
		want := []byte(s.JP)
		if english {
			want = slot(s.JP, s.EN)
		}
		if s.Offset+int64(len(want)) > int64(len(d)) || !bytes.Equal(d[s.Offset:s.Offset+int64(len(want))], want) {
			return false
		}
	}
	return true
}

func legacySpots(ps []patch) []spot {
	out := make([]spot, len(ps))
	for i, p := range ps {
		out[i] = spot{p.Offset, p.JP, p.EN}
	}
	return out
}

// classify says what an exe is. rec is the install record for it, if any.
// It also returns the spots English is (or would be) at.
func classify(d []byte, ps []patch, rec *record) (state, []spot, int) {
	if rec != nil && rec.Size == len(d) && spotsHold(d, rec.Spots, true) {
		return stEnglish, rec.Spots, rec.Missing
	}
	if leg := legacySpots(ps); spotsHold(d, leg, true) {
		return stEnglish, leg, 0 // patched by an older exvs-english
	}
	pairs := pairsOf(ps)
	sp, missing, err := findSpots(d, pairs)
	if err != nil || float64(len(pairs)-missing) < minCoverage*float64(len(pairs)) {
		return stUnknown, nil, missing
	}
	return stJapanese, sp, missing
}

func apply(data []byte, sp []spot, toEnglish bool) {
	for _, s := range sp {
		if toEnglish {
			copy(data[s.Offset:], slot(s.JP, s.EN))
		} else {
			copy(data[s.Offset:], s.JP)
		}
	}
}

func gameDir(flagDir string) (string, error) {
	cands := []string{}
	if flagDir != "" {
		cands = append(cands, flagDir)
	}
	if exe, err := os.Executable(); err == nil {
		d := filepath.Dir(exe)
		cands = append(cands, d, filepath.Dir(d))
	}
	if wd, err := os.Getwd(); err == nil {
		cands = append(cands, wd, filepath.Dir(wd))
	}
	for _, d := range cands {
		if _, err := os.Stat(filepath.Join(d, exeNames[0])); err == nil {
			return d, nil
		}
	}
	return "", errors.New("can't find vsac29_Release.exe; put exvs-english in the exv2 folder (or a subfolder) or pass -dir")
}

func backupDir(game string) string { return filepath.Join(game, "exvs-english", "backup") }

func recordPath(game, name string) string {
	return filepath.Join(backupDir(game), name+".english.json")
}

func loadRecord(game, name string) *record {
	b, err := os.ReadFile(recordPath(game, name))
	if err != nil {
		return nil
	}
	var r record
	if json.Unmarshal(b, &r) != nil {
		return nil
	}
	return &r
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp-english"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("%v (is the game or Live Monitor still running?)", err)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// backupOriginal copies the Japanese exe into the backup folder. A backup of a
// different exe (an older game build) is renamed, never overwritten.
func backupOriginal(path, bak string, data []byte) (bool, error) {
	if old, err := os.ReadFile(bak); err == nil {
		if bytes.Equal(old, data) {
			return false, nil
		}
		ts, _, _, _ := peInfo(old)
		aside := fmt.Sprintf("%s.%08x", bak, ts)
		if _, err := os.Stat(aside); err == nil {
			aside += ".old"
		}
		if err := os.Rename(bak, aside); err != nil {
			return false, err
		}
	}
	return true, copyFile(path, bak)
}

func describe(name string, st state, sp []spot, missing int) string {
	s := fmt.Sprintf("  %s: %s", name, st)
	if st != stUnknown && missing > 0 {
		s += fmt.Sprintf(", %d strings of the table aren't in this build", missing)
	}
	return s
}

func run(cmd, dir string, ps []patch) error {
	game, err := gameDir(dir)
	if err != nil {
		return err
	}
	fmt.Println("Game folder:", game)
	for _, name := range exeNames {
		path := filepath.Join(game, name)
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) && name != exeNames[0] {
				fmt.Printf("  %s: not found, skipped\n", name)
				continue
			}
			return err
		}
		rec := loadRecord(game, name)
		st, sp, missing := classify(data, ps, rec)
		switch cmd {
		case "status":
			fmt.Println(describe(name, st, sp, missing))
		case "install":
			if st == stEnglish {
				fmt.Printf("  %s: already English\n", name)
				continue
			}
			if st != stJapanese {
				return fmt.Errorf("%s doesn't look like a game build this patch knows (too few of its strings were found); nothing changed", name)
			}
			bdir := backupDir(game)
			if err := os.MkdirAll(bdir, 0o755); err != nil {
				return err
			}
			bak := filepath.Join(bdir, name)
			made, err := backupOriginal(path, bak, data)
			if err != nil {
				return fmt.Errorf("backup failed, nothing changed: %v", err)
			}
			if made {
				fmt.Printf("  %s: backed up to %s\n", name, bak)
			}
			ts, _, _, _ := peInfo(data)
			rb, _ := json.MarshalIndent(record{ts, len(data), sp, missing}, "", " ")
			if err := os.WriteFile(recordPath(game, name), rb, 0o644); err != nil {
				return fmt.Errorf("couldn't save the install record, nothing changed: %v", err)
			}
			apply(data, sp, true)
			if err := writeAtomic(path, data); err != nil {
				return err
			}
			msg := fmt.Sprintf("  %s: now English (%d strings, %d spots)", name, len(pairsOf(ps))-missing, len(sp))
			if missing > 0 {
				msg += fmt.Sprintf("; %d strings aren't in this build and were skipped", missing)
			}
			fmt.Println(msg)
		case "restore":
			if st == stJapanese {
				fmt.Printf("  %s: already Japanese\n", name)
				continue
			}
			bak := filepath.Join(backupDir(game), name)
			if b, err := os.ReadFile(bak); err == nil && st == stEnglish && len(b) == len(data) && spotsHold(b, sp, false) {
				// the backup is this exe before patching
				if err := writeAtomic(path, b); err != nil {
					return err
				}
			} else if st == stEnglish {
				apply(data, sp, false) // no matching backup: undo the patch in place
				if err := writeAtomic(path, data); err != nil {
					return err
				}
			} else {
				return fmt.Errorf("%s is an unknown version and there's no backup; nothing changed", name)
			}
			os.Remove(recordPath(game, name))
			fmt.Printf("  %s: restored to Japanese\n", name)
		case "copies":
			if st == stUnknown {
				return fmt.Errorf("%s doesn't look like a game build this patch knows", name)
			}
			apply(data, sp, true)
			out := strings.TrimSuffix(path, ".exe") + "_EN.exe"
			if err := os.WriteFile(out, data, 0o755); err != nil {
				return err
			}
			fmt.Printf("  wrote %s\n", out)
		default:
			return fmt.Errorf("unknown command %q (use install, restore, copies or status)", cmd)
		}
	}
	return nil
}

func main() {
	dir := flag.String("dir", "", "exv2 game folder (default: found automatically)")
	flag.Parse()
	ps := loadPatches()
	cmd := flag.Arg(0)
	interactive := cmd == ""
	if interactive {
		fmt.Println("EXVS2 English patch (test menus, Live Monitor settings, dialogs, messages)")
		_ = run("status", *dir, ps)
		fmt.Println()
		fmt.Println("  1) Install English   2) Restore Japanese   3) Quit")
		fmt.Print("Choose: ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		switch strings.TrimSpace(line) {
		case "1":
			cmd = "install"
		case "2":
			cmd = "restore"
		default:
			return
		}
	}
	err := run(cmd, *dir, ps)
	if err != nil {
		fmt.Println("ERROR:", err)
	} else if cmd != "status" {
		fmt.Println("Done.")
	}
	if interactive {
		fmt.Print("\nPress Enter to close.")
		bufio.NewReader(os.Stdin).ReadString('\n')
	}
	if err != nil {
		os.Exit(1)
	}
}
