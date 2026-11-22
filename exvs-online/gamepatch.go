package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Game exe patch for the online result-screen hang (EXVS2 IB 1.04.27, vsac29_Release.exe).
//
// After an online match the battle "finish" step (CSeqAcBattleSubFlow_Finish, state at
// 0x1408c89a0) waits for a signal that the other PC has finished too. Over the internet that
// signal never arrives, so both PCs sit on the result screen forever. The patch keeps the
// wait but gives up after 600 frames (about 10 seconds) and carries on as if it had arrived.
//
// The wait check at 0x1408c8a52 jumps to a small routine placed in the unused tail of .text:
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
//	jmp  0x1408c89e9          ; keep waiting (counts one more frame)
//	proceed: jmp 0x1408c8a68
const (
	gpImageBase   = 0x140000000
	gpTextRaw     = 0x400  // .text PointerToRawData
	gpTextVA      = 0x1000 // .text VirtualAddress
	gpSiteVA      = 0x1408c8a52
	gpWaitVA      = 0x1408c89e9
	gpProceedVA   = 0x1408c8a68
	gpCaveVA      = 0x141334880
	gpTextVSize   = 0x133387c // original .text VirtualSize
	gpTextVSizeOK = 0x1333900 // after the patch (covers the routine)
	gpTimeout     = 600
)

var gpSiteOrig = []byte{
	0x48, 0x8b, 0x89, 0x28, 0x06, 0x00, 0x00, // mov rcx,[rcx+0x628]
	0x48, 0x85, 0xc9, // test rcx,rcx
	0x74, 0x0a, // je
	0x48, 0x8b, 0x01, // mov rax,[rcx]
	0xff, 0x50, 0x10, // call [rax+0x10]
	0x84, 0xc0, // test al,al
	0x74, 0x81, // je wait
}

func gpFileOff(va uint64) int64 { return int64(va-gpImageBase-gpTextVA) + gpTextRaw }

func rel32(from, to uint64) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, uint32(int32(int64(to)-int64(from))))
	return b
}

func gpSitePatched() []byte {
	b := append([]byte{0xe9}, rel32(gpSiteVA+5, gpCaveVA)...)
	for len(b) < len(gpSiteOrig) {
		b = append(b, 0x90)
	}
	return b
}

func gpCave() []byte {
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
	c = append(c, rel32(gpCaveVA+uint64(len(c))+4, gpWaitVA)...)
	proceed := len(c)
	c = append(c, 0xe9)
	c = append(c, rel32(gpCaveVA+uint64(len(c))+4, gpProceedVA)...)
	for _, at := range []int{jeAt, jneAt, jaeAt} {
		c[at] = byte(proceed - (at + 1))
	}
	return c
}

// textVSizeOff is the file offset of .text's VirtualSize in the section table.
func textVSizeOff(d []byte) (int, error) {
	if len(d) < 0x400 || d[0] != 'M' || d[1] != 'Z' {
		return 0, errors.New("not an exe")
	}
	pe := int(binary.LittleEndian.Uint32(d[0x3c:]))
	if pe+24 > len(d) || !bytes.Equal(d[pe:pe+4], []byte("PE\x00\x00")) {
		return 0, errors.New("not a PE file")
	}
	optSize := int(binary.LittleEndian.Uint16(d[pe+20:]))
	sec := pe + 24 + optSize
	if !bytes.Equal(bytes.TrimRight(d[sec:sec+8], "\x00"), []byte(".text")) {
		return 0, errors.New("first section is not .text")
	}
	return sec + 8, nil
}

// gamePatchState reports "original", "patched" or an error for an unknown exe.
func gamePatchState(d []byte) (string, error) {
	vs, err := textVSizeOff(d)
	if err != nil {
		return "", err
	}
	ts := binary.LittleEndian.Uint32(d[int(binary.LittleEndian.Uint32(d[0x3c:]))+8:])
	if ts != probeTimestamp {
		return "", fmt.Errorf("not the 1.04.27 game exe (stamp %08x)", ts)
	}
	site := gpFileOff(gpSiteVA)
	cave := gpFileOff(gpCaveVA)
	c := gpCave()
	if int(cave)+len(c) > len(d) {
		return "", errors.New("exe is too short")
	}
	siteB := d[site : site+int64(len(gpSiteOrig))]
	caveB := d[cave : cave+int64(len(c))]
	switch {
	case bytes.Equal(siteB, gpSiteOrig) && bytes.Count(caveB, []byte{0}) == len(c) &&
		binary.LittleEndian.Uint32(d[vs:]) == gpTextVSize:
		return "original", nil
	case bytes.Equal(siteB, gpSitePatched()) && bytes.Equal(caveB, c):
		return "patched", nil
	}
	return "", errors.New("the bytes at the patch spot are not what this patch expects")
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
	backup := path + ".before-result-fix"
	if _, err := os.Stat(backup); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(backup, d, 0644); err != nil {
			return "", fmt.Errorf("saving a backup: %w", err)
		}
	}
	vs, _ := textVSizeOff(d)
	copy(d[gpFileOff(gpSiteVA):], gpSitePatched())
	copy(d[gpFileOff(gpCaveVA):], gpCave())
	binary.LittleEndian.PutUint32(d[vs:], gpTextVSizeOK)
	tmp := path + ".patching"
	if err := os.WriteFile(tmp, d, 0644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("replacing the exe (is the game still running?): %w", err)
	}
	return "patched (backup: " + filepath.Base(backup) + ")", nil
}

// unpatchGameExe puts back the original bytes.
func unpatchGameExe(path string) (string, error) {
	d, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	st, err := gamePatchState(d)
	if err != nil {
		return "", err
	}
	if st == "original" {
		return "not patched", nil
	}
	vs, _ := textVSizeOff(d)
	copy(d[gpFileOff(gpSiteVA):], gpSiteOrig)
	copy(d[gpFileOff(gpCaveVA):], make([]byte, len(gpCave())))
	binary.LittleEndian.PutUint32(d[vs:], gpTextVSize)
	tmp := path + ".patching"
	if err := os.WriteFile(tmp, d, 0644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("replacing the exe (is the game still running?): %w", err)
	}
	return "restored", nil
}
