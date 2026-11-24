package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Game exe patch for the online result-screen hang (vsac29_Release.exe).
//
// After an online match the battle "finish" step (CSeqAcBattleSubFlow_Finish) waits for a
// signal that the other PC has finished too. Over the internet that signal never arrives,
// so both PCs sit on the result screen forever. The patch keeps the wait but gives up after
// 600 frames (about 10 seconds) and carries on as if it had arrived.
//
// The patch is found by its byte pattern, not by a fixed address, so it works on any game
// build where that code is unchanged. Known builds:
//
//	2025-08-19 (1.04.27)          wait check at 0x1408c8a52, routine at 0x141334880
//	2026-04-17 (GQuuuuuuX update) wait check at 0x1408e1662, routine at 0x141394640
//
// The wait check jumps to a small routine placed in the unused tail of .text:
//
//	mov  rcx,[rdi+0x628]      ; the "other PC is done" check
//	test rcx,rcx
//	je   proceed
//	mov  rax,[rcx]
//	call [rax+0x10]
//	test al,al
//	jne  proceed              ; signal arrived: carry on as normal
//	cmp  dword [rdi+0x638],600 ; frames spent in the finish step
//	jae  proceed              ; waited long enough: carry on anyway
//	jmp  wait                 ; keep waiting (counts one more frame)
//	proceed: jmp <after the check>
//
// If a future build changes this code, the pattern no longer matches and the patch refuses
// with "unsupported game build" instead of writing anything.

const gpTimeout = 600

// wildcard marks a byte a pattern doesn't care about.
const wildcard = -1

// gpPrefix is the instruction pair just before the wait check: cmp eax,0x3c / jb rel8.
var gpPrefix = []int{0x83, 0xf8, 0x3c, 0x72, wildcard}

// gpSiteOrig is the wait check itself. The last byte (the je target) is build-specific.
var gpSiteOrig = []int{
	0x48, 0x8b, 0x89, 0x28, 0x06, 0x00, 0x00, // mov rcx,[rcx+0x628]
	0x48, 0x85, 0xc9, // test rcx,rcx
	0x74, 0x0a, // je +10
	0x48, 0x8b, 0x01, // mov rax,[rcx]
	0xff, 0x50, 0x10, // call [rax+0x10]
	0x84, 0xc0, // test al,al
	0x74, wildcard, // je wait
}

const gpSiteLen = 22

// gpFollow is what comes right after the wait check in every known build.
var gpFollow = []int{0x48, 0x8b, 0x8f, 0xa0, 0x04, 0x00, 0x00} // mov rcx,[rdi+0x4a0]

// gpCounterInc is "inc dword [rdi+0x638]", the frame counter the routine compares against.
// It has to appear shortly before the wait check, or the routine would read the wrong field.
var gpCounterInc = []byte{0xff, 0x87, 0x38, 0x06, 0x00, 0x00}

// peInfo is what the patches need from the PE headers.
type peInfo struct {
	timestamp   uint32
	entry       uint32 // AddressOfEntryPoint (RVA)
	imageBase   uint64
	textRaw     uint32 // .text PointerToRawData
	textVA      uint32 // .text VirtualAddress
	textVSize   uint32 // .text VirtualSize
	textRawSize uint32 // .text SizeOfRawData
	vsizeOff    int    // file offset of .text's VirtualSize
}

func parsePE(d []byte) (peInfo, error) {
	var p peInfo
	if len(d) < 0x400 || d[0] != 'M' || d[1] != 'Z' {
		return p, errors.New("not an exe")
	}
	pe := int(binary.LittleEndian.Uint32(d[0x3c:]))
	if pe+24+0x70 > len(d) || !bytes.Equal(d[pe:pe+4], []byte("PE\x00\x00")) {
		return p, errors.New("not a PE file")
	}
	p.timestamp = binary.LittleEndian.Uint32(d[pe+8:])
	optSize := int(binary.LittleEndian.Uint16(d[pe+20:]))
	opt := pe + 24
	if binary.LittleEndian.Uint16(d[opt:]) != 0x20b {
		return p, errors.New("not a 64-bit PE file")
	}
	p.entry = binary.LittleEndian.Uint32(d[opt+16:])
	p.imageBase = binary.LittleEndian.Uint64(d[opt+24:])
	sec := opt + optSize
	if sec+40 > len(d) || !bytes.Equal(bytes.TrimRight(d[sec:sec+8], "\x00"), []byte(".text")) {
		return p, errors.New("first section is not .text")
	}
	p.vsizeOff = sec + 8
	p.textVSize = binary.LittleEndian.Uint32(d[sec+8:])
	p.textVA = binary.LittleEndian.Uint32(d[sec+12:])
	p.textRawSize = binary.LittleEndian.Uint32(d[sec+16:])
	p.textRaw = binary.LittleEndian.Uint32(d[sec+20:])
	if int(p.textRaw)+int(p.textRawSize) > len(d) {
		return p, errors.New("exe is too short")
	}
	return p, nil
}

func (p peInfo) va(off int) uint64 {
	return p.imageBase + uint64(p.textVA) + uint64(off) - uint64(p.textRaw)
}
func (p peInfo) off(va uint64) int { return int(va-p.imageBase-uint64(p.textVA)) + int(p.textRaw) }
func (p peInfo) textEnd() int      { return int(p.textRaw + p.textRawSize) }

func alignUp(v, a uint32) uint32 { return (v + a - 1) &^ (a - 1) }

func rel32At(d []byte, off int) int64 { return int64(int32(binary.LittleEndian.Uint32(d[off:]))) }

func rel32(from, to uint64) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, uint32(int32(int64(to)-int64(from))))
	return b
}

// matchAt reports whether pat matches d at off.
func matchAt(d []byte, off int, pat []int) bool {
	if off < 0 || off+len(pat) > len(d) {
		return false
	}
	for i, b := range pat {
		if b != wildcard && int(d[off+i]) != b {
			return false
		}
	}
	return true
}

// findPattern returns every offset in d[lo:hi] where pat matches. pat[0] must not be a wildcard.
func findPattern(d []byte, lo, hi int, pat []int) []int {
	var out []int
	first := byte(pat[0])
	for i := lo; i+len(pat) <= hi; i++ {
		j := bytes.IndexByte(d[i:hi], first)
		if j < 0 {
			break
		}
		i += j
		if matchAt(d, i, pat) {
			out = append(out, i)
		}
	}
	return out
}

func concat(parts ...[]int) []int {
	var out []int
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// gpPlan is where the patch goes in one particular exe.
type gpPlan struct {
	pe        peInfo
	siteOff   int    // file offset of the wait check
	waitVA    uint64 // where "keep waiting" jumps to
	proceedVA uint64 // the instruction after the wait check
	caveVA    uint64
	vsizeNew  uint32
	state     string // "original" or "patched"
}

func (g gpPlan) siteVA() uint64 { return g.pe.va(g.siteOff) }

func (g gpPlan) sitePatched() []byte {
	b := append([]byte{0xe9}, rel32(g.siteVA()+5, g.caveVA)...)
	for len(b) < gpSiteLen {
		b = append(b, 0x90)
	}
	return b
}

func (g gpPlan) siteOrig() []byte {
	b := make([]byte, gpSiteLen)
	for i, v := range gpSiteOrig {
		b[i] = byte(v)
	}
	b[gpSiteLen-1] = byte(int8(int64(g.waitVA) - int64(g.proceedVA)))
	return b
}

func (g gpPlan) cave() []byte { return gpCaveFor(g.caveVA, g.waitVA, g.proceedVA) }

func gpCaveFor(caveVA, waitVA, proceedVA uint64) []byte {
	var c []byte
	c = append(c, 0x48, 0x8b, 0x8f, 0x28, 0x06, 0x00, 0x00) // mov rcx,[rdi+0x628]
	c = append(c, 0x48, 0x85, 0xc9)                         // test rcx,rcx
	c = append(c, 0x74, 0)                                  // je proceed (fixed below)
	jeAt := len(c) - 1
	c = append(c, 0x48, 0x8b, 0x01) // mov rax,[rcx]
	c = append(c, 0xff, 0x50, 0x10) // call [rax+0x10]
	c = append(c, 0x84, 0xc0)       // test al,al
	c = append(c, 0x75, 0)          // jne proceed
	jneAt := len(c) - 1
	c = append(c, 0x81, 0xbf, 0x38, 0x06, 0x00, 0x00) // cmp dword [rdi+0x638],imm32
	c = binary.LittleEndian.AppendUint32(c, gpTimeout)
	c = append(c, 0x73, 0) // jae proceed
	jaeAt := len(c) - 1
	c = append(c, 0xe9)
	c = append(c, rel32(caveVA+uint64(len(c))+4, waitVA)...)
	proceed := len(c)
	c = append(c, 0xe9)
	c = append(c, rel32(caveVA+uint64(len(c))+4, proceedVA)...)
	for _, at := range []int{jeAt, jneAt, jaeAt} {
		c[at] = byte(proceed - (at + 1))
	}
	return c
}

var gpCaveLen = len(gpCaveFor(0, 0, 0))

// planGamePatch finds the wait check in d and works out where everything goes.
// It errors for an exe it doesn't recognise, so nothing is ever written blind.
func planGamePatch(d []byte) (gpPlan, error) {
	pe, err := parsePE(d)
	if err != nil {
		return gpPlan{}, err
	}
	lo, hi := int(pe.textRaw), pe.textEnd()
	unsupported := func(why string) (gpPlan, error) {
		return gpPlan{}, fmt.Errorf("unsupported game build (%s): %s", buildName(pe), why)
	}

	orig := findPattern(d, lo, hi, concat(gpPrefix, gpSiteOrig, gpFollow))
	jmpNops := []int{0xe9, wildcard, wildcard, wildcard, wildcard}
	for len(jmpNops) < gpSiteLen {
		jmpNops = append(jmpNops, 0x90)
	}
	patched := findPattern(d, lo, hi, concat(gpPrefix, jmpNops, gpFollow))

	switch {
	case len(orig) == 1 && len(patched) == 0:
		g := gpPlan{pe: pe, siteOff: orig[0] + len(gpPrefix), state: "original"}
		g.proceedVA = g.siteVA() + gpSiteLen
		g.waitVA = uint64(int64(g.proceedVA) + int64(int8(d[g.siteOff+gpSiteLen-1])))
		if !bytes.Contains(d[max(lo, g.siteOff-0x200):g.siteOff], gpCounterInc) {
			return unsupported("the frame counter the fix relies on isn't where it used to be")
		}
		caveRVA := alignUp(pe.textVA+pe.textVSize, 16) - pe.textVA
		if caveRVA+uint32(gpCaveLen) > pe.textRawSize {
			return unsupported("no free room at the end of .text for the fix")
		}
		g.caveVA = pe.imageBase + uint64(pe.textVA) + uint64(caveRVA)
		g.vsizeNew = min(alignUp(caveRVA+uint32(gpCaveLen), 0x80), pe.textRawSize)
		caveOff := pe.off(g.caveVA)
		if bytes.Count(d[caveOff:caveOff+gpCaveLen], []byte{0}) != gpCaveLen {
			return unsupported("the free room at the end of .text is already in use")
		}
		return g, nil
	case len(orig) == 0 && len(patched) == 1:
		g := gpPlan{pe: pe, siteOff: patched[0] + len(gpPrefix), state: "patched"}
		g.proceedVA = g.siteVA() + gpSiteLen
		g.caveVA = uint64(int64(g.siteVA()+5) + rel32At(d, g.siteOff+1))
		caveOff := pe.off(g.caveVA)
		if g.caveVA < pe.imageBase || caveOff < lo || caveOff+gpCaveLen > hi {
			return unsupported("the jump at the patch spot goes outside .text")
		}
		// the "keep waiting" jump is the second-to-last instruction of the routine
		jmpAt := caveOff + gpCaveLen - 10
		g.waitVA = uint64(int64(pe.va(jmpAt)+5) + rel32At(d, jmpAt+1))
		if !bytes.Equal(d[caveOff:caveOff+gpCaveLen], g.cave()) {
			return unsupported("the patch spot jumps to code this tool didn't write")
		}
		g.vsizeNew = pe.textVSize
		return g, nil
	case len(orig) == 0 && len(patched) == 0:
		return unsupported("the code the fix changes wasn't found")
	}
	return unsupported(fmt.Sprintf("the code the fix changes was found %d times", len(orig)+len(patched)))
}

// gamePatchState reports "original", "patched" or an error for an unknown exe.
func gamePatchState(d []byte) (string, error) {
	g, err := planGamePatch(d)
	if err != nil {
		return "", err
	}
	return g.state, nil
}

// applyGamePatch returns the patched bytes of an unpatched exe.
func applyGamePatch(d []byte) ([]byte, error) {
	g, err := planGamePatch(d)
	if err != nil {
		return nil, err
	}
	if g.state != "original" {
		return nil, errors.New("already patched")
	}
	out := append([]byte(nil), d...)
	copy(out[g.siteOff:], g.sitePatched())
	copy(out[g.pe.off(g.caveVA):], g.cave())
	binary.LittleEndian.PutUint32(out[g.pe.vsizeOff:], g.vsizeNew)
	return out, nil
}

// patchGameExe applies the result-screen patch to the exe at path. It keeps a copy of the
// unpatched exe next to it (<name>.before-result-fix) and is a no-op when already patched.
func patchGameExe(path string) (string, error) {
	d, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	st, err := gamePatchState(d)
	if err != nil {
		return "", err
	}
	if st == "patched" {
		return "already patched", nil
	}
	out, err := applyGamePatch(d)
	if err != nil {
		return "", err
	}
	backup := path + ".before-result-fix"
	if err := saveBackup(backup, d); err != nil {
		return "", err
	}
	if err := replaceFile(path, out); err != nil {
		return "", err
	}
	return "patched (backup: " + filepath.Base(backup) + ")", nil
}

// saveBackup writes d to backup unless an identical backup is already there. A backup of a
// different file (an older game build) is moved aside first, so updating never loses it.
func saveBackup(backup string, d []byte) error {
	if old, err := os.ReadFile(backup); err == nil {
		if bytes.Equal(old, d) {
			return nil
		}
		aside := fmt.Sprintf("%s.%08x", backup, peStamp(old))
		if _, err := os.Stat(aside); err == nil {
			aside += ".old"
		}
		if err := os.Rename(backup, aside); err != nil {
			return fmt.Errorf("moving an older backup aside: %w", err)
		}
	}
	if err := os.WriteFile(backup, d, 0644); err != nil {
		return fmt.Errorf("saving a backup: %w", err)
	}
	return nil
}

func peStamp(d []byte) uint32 {
	if p, err := parsePE(d); err == nil {
		return p.timestamp
	}
	return 0
}

func replaceFile(path string, d []byte) error {
	tmp := path + ".patching"
	if err := os.WriteFile(tmp, d, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("replacing %s (is the game still running?): %w", filepath.Base(path), err)
	}
	return nil
}

// unpatchGameExe puts back the original bytes. It uses the backup when the backup is this
// exact exe before patching, and otherwise undoes the patch in place.
func unpatchGameExe(path string) (string, error) {
	d, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	g, err := planGamePatch(d)
	if err != nil {
		return "", err
	}
	if g.state == "original" {
		return "not patched", nil
	}
	if b, err := os.ReadFile(path + ".before-result-fix"); err == nil {
		if p, err := applyGamePatch(b); err == nil && bytes.Equal(p, d) {
			if err := replaceFile(path, b); err != nil {
				return "", err
			}
			return "restored from backup", nil
		}
	}
	out := append([]byte(nil), d...)
	copy(out[g.siteOff:], g.siteOrig())
	copy(out[g.pe.off(g.caveVA):], make([]byte, gpCaveLen))
	// The original .text size isn't kept in the exe. The start of the routine is at most
	// 15 bytes past it and still covers all the original code, which is all the loader needs.
	binary.LittleEndian.PutUint32(out[g.pe.vsizeOff:], uint32(g.caveVA-g.pe.imageBase)-g.pe.textVA)
	if err := replaceFile(path, out); err != nil {
		return "", err
	}
	return "restored", nil
}

// knownBuilds names the game builds Boost Link has been checked against, by PE timestamp.
// An unknown build is still patched when every pattern matches exactly once.
var knownBuilds = map[uint32]string{
	0x68a458ad: "1.04.27, 2025-08-19",
	0x69e2081b: "GQuuuuuuX update, 2026-04-17",
}

func buildName(p peInfo) string {
	if n, ok := knownBuilds[p.timestamp]; ok {
		return n
	}
	return fmt.Sprintf("unknown build %08x", p.timestamp)
}
