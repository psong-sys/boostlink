package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Keyboard focus fix for bngrw.dll, the PC input hook from EXVS2-POC.
//
// The hook reads the keyboard system-wide, so keys typed into Discord or a browser reach
// the game. The fix sends the keyboard-poll function (0x1800482a0) through a small routine
// that first checks the foreground window belongs to the game's own process
// (GetForegroundWindow + GetWindowThreadProcessId, looked up once through user32.dll) and
// skips the poll otherwise. Controllers are not affected.
//
// The same base bngrw.dll (2025-08-30 build) ships with every game pack seen so far. Packs
// differ only in the game exe entry point the hook looks for (see dllEntryCheckOff), which
// this patch doesn't touch, so it applies to each pack's own copy. Every run below is
// checked before writing; any other bngrw.dll is refused.
type dllRun struct {
	off       int
	orig, new string // hex
}

const dllSize = 1474048

var focusRuns = []dllRun{
	{0x218, "3319", "001a"},
	{0x240, "24cd", "00ce"},
	{0x268, "e851", "0052"},
	{0x482a0, "48895c2408", "e99b9a0200"},
	{0x71d40, "0000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000", "51524883ec28e8150000004883c4285a5985c0740a48895c2408e94665fdffc3534883ec30488b0584280f009090904885c07553488d0db5d30e00ff158f0800004885c0746a4889c34889d9488d15b3d30e00ff15570800004885c0745248890553280f009090904889d9488d15add30e00ff15380800004885c074334889052c280f00909090ffd04885c074294889c1c744242000000000488d542420ff1514280f00ff151e0700003b4424207507b801000000eb0231c04883c4305bc3"},
	{0x15eb30, "000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000", "7500730065007200330032002e0064006c006c00000047657457696e646f7754687265616450726f63657373496400476574466f726567726f756e6457696e646f77"},
}

// dllEntryCheckOff is the imm32 of "cmp rdx, <game exe entry point RVA>" in the hook's
// start-up check. The hook only attaches to an exe with that entry point.
const dllEntryCheckOff = 0x223df

var dllEntryCheckOp = []byte{0x48, 0x81, 0xfa} // cmp rdx, imm32

func focusRunsMatch(d []byte, patched bool) bool {
	if len(d) != dllSize {
		return false
	}
	for _, r := range focusRuns {
		h := r.orig
		if patched {
			h = r.new
		}
		want, _ := hex.DecodeString(h)
		if !bytes.Equal(d[r.off:r.off+len(want)], want) {
			return false
		}
	}
	return true
}

// focusPatchState reports "original" or "patched", or an error for an unknown DLL.
func focusPatchState(d []byte) (string, error) {
	switch {
	case focusRunsMatch(d, false):
		return "original", nil
	case focusRunsMatch(d, true):
		return "patched", nil
	}
	return "", errors.New("unsupported bngrw.dll: not the EXVS2-POC build this fix was made for")
}

// dllEntryPoint returns the game exe entry point bngrw.dll is built to hook.
func dllEntryPoint(d []byte) (uint32, bool) {
	if len(d) < dllEntryCheckOff+4 || !bytes.Equal(d[dllEntryCheckOff-3:dllEntryCheckOff], dllEntryCheckOp) {
		return 0, false
	}
	return binary.LittleEndian.Uint32(d[dllEntryCheckOff:]), true
}

// checkDLLMatchesExe explains a bngrw.dll that was made for a different game exe, which
// happens when a DLL is copied from one game pack into another. The game then starts
// without the PC hook (no keyboard, no card reader, no config.ini).
func checkDLLMatchesExe(dll, exe []byte) error {
	ep, ok := dllEntryPoint(dll)
	if !ok {
		return nil
	}
	p, err := parsePE(exe)
	if err != nil {
		return nil
	}
	if ep != p.entry {
		return fmt.Errorf("bngrw.dll is made for a different game exe (hooks entry %#x, this exe's is %#x); use the bngrw.dll that came with this game build", ep, p.entry)
	}
	return nil
}

func applyFocusPatch(d []byte) ([]byte, error) {
	st, err := focusPatchState(d)
	if err != nil {
		return nil, err
	}
	if st != "original" {
		return nil, errors.New("already patched")
	}
	out := append([]byte(nil), d...)
	for _, r := range focusRuns {
		b, _ := hex.DecodeString(r.new)
		copy(out[r.off:], b)
	}
	return out, nil
}

// patchFocusDLL applies the focus fix to the bngrw.dll at path, keeping
// <name>.before-focusfix, and is a no-op when already patched.
func patchFocusDLL(path string) (string, error) {
	d, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	st, err := focusPatchState(d)
	if err != nil {
		return "", err
	}
	if st == "patched" {
		return "already patched", nil
	}
	out, err := applyFocusPatch(d)
	if err != nil {
		return "", err
	}
	backup := path + ".before-focusfix"
	if err := saveBackup(backup, d); err != nil {
		return "", err
	}
	if err := replaceFile(path, out); err != nil {
		return "", err
	}
	return "patched (backup: " + filepath.Base(backup) + ")", nil
}

// unpatchFocusDLL undoes the focus fix in place.
func unpatchFocusDLL(path string) (string, error) {
	d, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	st, err := focusPatchState(d)
	if err != nil {
		return "", err
	}
	if st == "original" {
		return "not patched", nil
	}
	out := append([]byte(nil), d...)
	for _, r := range focusRuns {
		b, _ := hex.DecodeString(r.orig)
		copy(out[r.off:], b)
	}
	if err := replaceFile(path, out); err != nil {
		return "", err
	}
	return "restored", nil
}
