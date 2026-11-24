// exvs-ui: installs the English game-screen files (exvs-english\ui-en\) into data\x64\dplcache_release.
// Run from exv2\exvs-english\ (double-click = install). Commands: install, restore, status.
package main

import (
	"bufio"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func md5file(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func readSums(p string) (map[string]string, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m := map[string]string{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		fs := strings.Fields(s.Text())
		if len(fs) == 2 {
			m[fs[1]] = strings.ToLower(fs[0])
		}
	}
	return m, s.Err()
}

func readList(p string) ([]string, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

type ctx struct{ here, cache, uien, backup string }

func find() (*ctx, error) {
	exe, _ := os.Executable()
	cands := []string{filepath.Dir(exe)}
	if wd, err := os.Getwd(); err == nil {
		cands = append(cands, wd)
	}
	for _, d := range cands {
		for _, h := range []string{d, filepath.Join(d, "exvs-english")} {
			game := filepath.Dir(h)
			c := filepath.Join(game, "data", "x64", "dplcache_release")
			if st, err := os.Stat(filepath.Join(h, "ui-en")); err == nil && st.IsDir() {
				if st2, err := os.Stat(c); err == nil && st2.IsDir() {
					return &ctx{h, c, filepath.Join(h, "ui-en"), filepath.Join(h, "backup", "data")}, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("put this program in exv2\\exvs-english\\ (next to the ui-en folder)")
}

func run(cmd string) error {
	c, err := find()
	if err != nil {
		return err
	}
	list, err := readList(filepath.Join(c.uien, "install.txt"))
	if err != nil {
		return err
	}
	orig, err := readSums(filepath.Join(c.uien, "orig-md5.txt"))
	if err != nil {
		return err
	}
	eng, err := readSums(filepath.Join(c.uien, "md5.txt"))
	if err != nil {
		return err
	}
	switch cmd {
	case "status":
		counts := map[string]int{}
		for _, f := range list {
			cur, err := md5file(filepath.Join(c.cache, filepath.Base(f)))
			state := "MISSING"
			if err == nil {
				switch cur {
				case orig[f]:
					state = "Japanese (original)"
				case eng[f]:
					state = "English"
				default:
					state = "UNKNOWN version"
				}
			}
			counts[state]++
			if !strings.Contains(f, "/") {
				fmt.Printf("%-18s %s\n", f, state)
			}
		}
		fmt.Println("Totals:")
		for k, v := range counts {
			fmt.Printf("  %-20s %d file(s)\n", k, v)
		}
		return nil
	case "install":
		// Our own files must all be intact before anything is written.
		for _, f := range list {
			if orig[f] == "" || eng[f] == "" {
				return fmt.Errorf("%s has no checksum in ui-en, nothing changed", f)
			}
			if s, err := md5file(filepath.Join(c.uien, filepath.FromSlash(f))); err != nil || s != eng[f] {
				return fmt.Errorf("ui-en\\%s is damaged or missing, nothing changed", f)
			}
		}
		// Game files are checked one by one. A file this build changed (or another mod
		// already replaced) is left alone and listed; the rest are installed. This keeps
		// most of the English on a newer game build while those few files get rebuilt
		// with repaint\ from the new originals.
		var todo, skipped []string
		for _, f := range list {
			cur, err := md5file(filepath.Join(c.cache, filepath.Base(f)))
			switch {
			case err != nil:
				skipped = append(skipped, filepath.Base(f)+" (not in this game build)")
			case cur == eng[f]:
			case cur == orig[f]:
				todo = append(todo, f)
			default:
				skipped = append(skipped, filepath.Base(f)+" (different version in this game build)")
			}
		}
		if len(todo) == 0 && len(skipped) == len(list) {
			return fmt.Errorf("none of the %d game files match what this patch expects; nothing changed", len(list))
		}
		if err := os.MkdirAll(c.backup, 0o755); err != nil {
			return err
		}
		n := 0
		for _, f := range todo {
			g := filepath.Join(c.cache, filepath.Base(f))
			b := filepath.Join(c.backup, filepath.Base(f))
			if bs, err := md5file(b); err != nil || bs != orig[f] {
				if err := copyFile(g, b); err != nil {
					return fmt.Errorf("backup of %s failed: %v", f, err)
				}
				if bs, _ := md5file(b); bs != orig[f] {
					return fmt.Errorf("backup of %s did not verify", f)
				}
			}
			if err := copyFile(filepath.Join(c.uien, filepath.FromSlash(f)), g); err != nil {
				return fmt.Errorf("writing %s failed: %v (run restore)", f, err)
			}
			n++
		}
		fmt.Printf("English installed (%d file(s) changed). Originals are in %s\n", n, c.backup)
		if len(skipped) > 0 {
			fmt.Printf("Left in their current version (%d file(s)); these need rebuilding for this game build:\n", len(skipped))
			for _, s := range skipped {
				fmt.Println("  " + s)
			}
		}
		return nil
	case "restore":
		n := 0
		for _, f := range list {
			g := filepath.Join(c.cache, filepath.Base(f))
			if cur, _ := md5file(g); cur == orig[f] {
				continue
			}
			b := filepath.Join(c.backup, filepath.Base(f))
			if bs, err := md5file(b); err != nil || bs != orig[f] {
				fmt.Printf("WARNING: no good backup for %s, left as is\n", f)
				continue
			}
			if err := copyFile(b, g); err != nil {
				return fmt.Errorf("restoring %s failed: %v", f, err)
			}
			n++
		}
		fmt.Printf("Japanese originals restored (%d file(s)).\n", n)
		return nil
	}
	return fmt.Errorf("unknown command %q (use install, restore or status)", cmd)
}

func main() {
	cmd := "install"
	if len(os.Args) > 1 {
		cmd = strings.ToLower(os.Args[1])
	}
	err := run(cmd)
	if err != nil {
		fmt.Println("ERROR:", err)
	}
	if len(os.Args) <= 1 {
		fmt.Println("\nPress Enter to close.")
		bufio.NewReader(os.Stdin).ReadString('\n')
	}
	if err != nil {
		os.Exit(1)
	}
}
