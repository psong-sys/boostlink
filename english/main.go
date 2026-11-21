// exvs-english: puts English text into the EXVS2 test menus, Live Monitor
// settings, dialogs and status/error messages.
//
// It swaps Japanese UTF-8 strings inside vsac29_Release.exe and
// vsac29_Release_LM.exe for English ones of equal or shorter length.
// Originals are copied to exvs-english\backup\ before anything is written.
//
//	exvs-english.exe            ask what to do (install / restore)
//	exvs-english.exe install    patch both exes in place (backup first)
//	exvs-english.exe restore    put the Japanese originals back
//	exvs-english.exe copies     leave originals alone, write *_EN.exe copies
//	exvs-english.exe status     show whether each exe is Japanese or English
package main

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

//go:embed patches.json
var patchesJSON []byte

type patch struct {
	Offset int64  `json:"offset"`
	JP     string `json:"jp"`
	EN     string `json:"en"`
}

var exeNames = []string{"vsac29_Release.exe", "vsac29_Release_LM.exe"}

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

// slot returns the bytes a patch writes: the English text padded with NULs
// to the Japanese length (plus the existing terminator).
func (p patch) slot() []byte {
	b := make([]byte, len(p.JP))
	copy(b, p.EN)
	return b
}

func classify(data []byte, ps []patch) state {
	jp, en := 0, 0
	for _, p := range ps {
		end := p.Offset + int64(len(p.JP))
		if end >= int64(len(data)) {
			return stUnknown
		}
		seg := data[p.Offset:end]
		switch {
		case string(seg) == p.JP:
			jp++
		case string(seg) == string(p.slot()):
			en++
		}
	}
	switch {
	case jp == len(ps):
		return stJapanese
	case en == len(ps):
		return stEnglish
	}
	return stUnknown
}

func apply(data []byte, ps []patch, toEnglish bool) {
	for _, p := range ps {
		if toEnglish {
			copy(data[p.Offset:], p.slot())
		} else {
			copy(data[p.Offset:], p.JP)
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
		st := classify(data, ps)
		switch cmd {
		case "status":
			fmt.Printf("  %s: %s\n", name, st)
		case "install":
			if st == stEnglish {
				fmt.Printf("  %s: already English\n", name)
				continue
			}
			if st != stJapanese {
				return fmt.Errorf("%s isn't the 1.04.27 build this patch was made for; nothing changed", name)
			}
			bdir := backupDir(game)
			if err := os.MkdirAll(bdir, 0o755); err != nil {
				return err
			}
			bak := filepath.Join(bdir, name)
			if _, err := os.Stat(bak); os.IsNotExist(err) {
				if err := copyFile(path, bak); err != nil {
					return fmt.Errorf("backup failed, nothing changed: %v", err)
				}
				fmt.Printf("  %s: backed up to %s\n", name, bak)
			}
			apply(data, ps, true)
			if err := writeAtomic(path, data); err != nil {
				return err
			}
			fmt.Printf("  %s: now English (%d strings)\n", name, len(ps))
		case "restore":
			if st == stJapanese {
				fmt.Printf("  %s: already Japanese\n", name)
				continue
			}
			bak := filepath.Join(backupDir(game), name)
			if b, err := os.ReadFile(bak); err == nil && classify(b, ps) == stJapanese {
				if err := writeAtomic(path, b); err != nil {
					return err
				}
			} else if st == stEnglish {
				apply(data, ps, false) // no backup: undo the patch in place
				if err := writeAtomic(path, data); err != nil {
					return err
				}
			} else {
				return fmt.Errorf("%s is an unknown version and there's no backup; nothing changed", name)
			}
			fmt.Printf("  %s: restored to Japanese\n", name)
		case "copies":
			if st != stJapanese && st != stEnglish {
				return fmt.Errorf("%s isn't the 1.04.27 build this patch was made for", name)
			}
			apply(data, ps, true)
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
