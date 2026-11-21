package main

// Switching the game to online battle mode without the Live Monitor's F3 menu.
//
// The operator settings live in 29\testmode_pcb.json (client) and
// 29\testmode_lm.json (Live Monitor), inside nbamsavdat.dll's "svd1" container:
//
//	0x00  4  magic "svd1"
//	0x04  4  u32 write counter (old+1 on each write)
//	0x08  8  u64 payload size
//	0x10 20  SHA-1 of (bytes 0x00..0x0F + payload)
//	0x24  n  payload: JSON {"storage": {key: {"value": v, "type": 1}}, ...}
//
// The setting is .TestModeParam.gameServerPcb.settingFile.[F].group.[G].battleMode
// for the active file F (useSettingFile) and G = 0..2. 0 = online (店外), 1 = in-store.
// Only the digits of those values are replaced; the rest of the JSON stays byte-identical.

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

const svdHeaderLen = 36

var valueRe = regexp.MustCompile(`"value"\s*:\s*(-?\d+)`)

func readSvd(path string) (header []byte, payload []byte, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	if len(b) < svdHeaderLen || string(b[:4]) != "svd1" {
		return nil, nil, errors.New("not an svd1 file")
	}
	size := binary.LittleEndian.Uint64(b[8:16])
	if size != uint64(len(b)-svdHeaderLen) {
		return nil, nil, fmt.Errorf("size field %d doesn't match the file (%d)", size, len(b)-svdHeaderLen)
	}
	h := sha1.New()
	h.Write(b[:16])
	h.Write(b[svdHeaderLen:])
	if !bytes.Equal(h.Sum(nil), b[16:36]) {
		return nil, nil, errors.New("checksum doesn't match, so the format may be different from what the launcher expects")
	}
	return b[:svdHeaderLen], b[svdHeaderLen:], nil
}

func encodeSvd(oldHeader, payload []byte) []byte {
	hdr := make([]byte, svdHeaderLen)
	copy(hdr[:4], "svd1")
	binary.LittleEndian.PutUint32(hdr[4:8], binary.LittleEndian.Uint32(oldHeader[4:8])+1)
	binary.LittleEndian.PutUint64(hdr[8:16], uint64(len(payload)))
	h := sha1.New()
	h.Write(hdr[:16])
	h.Write(payload)
	copy(hdr[16:36], h.Sum(nil))
	return append(hdr, payload...)
}

// findValue locates the "value" number inside the object of a storage key.
// It returns the value and the byte range of its digits.
func findValue(payload []byte, key string) (val, start, end int, ok bool) {
	k := bytes.Index(payload, []byte(strconv.Quote(key)))
	if k < 0 {
		return 0, 0, 0, false
	}
	open := bytes.IndexByte(payload[k:], '{')
	if open < 0 {
		return 0, 0, 0, false
	}
	open += k
	closeIdx := bytes.IndexByte(payload[open:], '}')
	if closeIdx < 0 {
		return 0, 0, 0, false
	}
	obj := payload[open : open+closeIdx]
	m := valueRe.FindSubmatchIndex(obj)
	if m == nil {
		return 0, 0, 0, false
	}
	v, err := strconv.Atoi(string(obj[m[2]:m[3]]))
	if err != nil {
		return 0, 0, 0, false
	}
	return v, open + m[2], open + m[3], true
}

const battleModeOnline = 0

func battleModeKey(file, group int) string {
	return fmt.Sprintf(".TestModeParam.gameServerPcb.settingFile.[%d].group.[%d].battleMode", file, group)
}

// ensureOnlineMode sets the active setting file's battleMode to online in one
// testmode file. It returns a description of what it found and whether it
// changed anything. The file is backed up before it is rewritten.
func ensureOnlineMode(path, backupDir string) (string, bool, error) {
	hdr, payload, err := readSvd(path)
	if err != nil {
		return "", false, err
	}
	file := 0
	if v, _, _, ok := findValue(payload, ".TestModeParam.gameServerPcb.useSettingFile"); ok {
		file = v
	}
	var before []string
	out := append([]byte(nil), payload...)
	changed := false
	// edit from the end so earlier offsets stay valid
	type edit struct{ start, end int }
	var edits []edit
	for g := 0; g < 3; g++ {
		v, s, e, ok := findValue(out, battleModeKey(file, g))
		if !ok {
			before = append(before, "?")
			continue
		}
		before = append(before, strconv.Itoa(v))
		if v != battleModeOnline {
			edits = append(edits, edit{s, e})
		}
	}
	for i := 0; i < len(edits); i++ {
		for j := i + 1; j < len(edits); j++ {
			if edits[j].start > edits[i].start {
				edits[i], edits[j] = edits[j], edits[i]
			}
		}
	}
	for _, ed := range edits {
		out = append(out[:ed.start], append([]byte(strconv.Itoa(battleModeOnline)), out[ed.end:]...)...)
		changed = true
	}
	desc := fmt.Sprintf("setting file %d, battle mode per group = %v", file, before)
	if !changed {
		return desc, false, nil
	}
	if err := backup(path, backupDir); err != nil {
		return desc, false, fmt.Errorf("backing up: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, encodeSvd(hdr, out), 0644); err != nil {
		return desc, false, err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return desc, false, err
	}
	return desc, true, nil
}

func testmodeFiles(gameDir string) []string {
	return []string{
		filepath.Join(gameDir, "29", "testmode_pcb.json"),
		filepath.Join(gameDir, "29", "testmode_lm.json"),
	}
}
