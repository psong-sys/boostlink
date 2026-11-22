//go:build windows

package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The probe reads the running game's memory to find which game "sequence" (screen step)
// each part of the game is sitting in. It is read only and is used to find hangs such as
// the online result screen that never moves on.

const (
	memPrivate     = 0x20000
	probeExe       = "vsac29_release.exe"
	probeTimestamp = 0x68a458ad // PE TimeDateStamp of the 1.04.27 exe the vtable list was taken from
	probeImageSize = 0x3063000
	rdataStart     = 0x1335000
	rdataEnd       = 0x2367000
	textStart      = 0x1000
	textEnd        = 0x1335000
	stateOff       = 0x468 // current state function of a sequence node
	childCountOff  = 0x438
	nodeReadSize   = 0x640
)

type probeHit struct {
	addr  uint64
	class string
	state uint32 // RVA of the state function
	sub   uint32 // the word after the state pointer (state phase)
	kids  uint32
	raw   []byte
}

func findGame() (pid uint32, base uint64, size uint32, err error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, 0, 0, err
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for e := windows.Process32First(snap, &pe); e == nil; e = windows.Process32Next(snap, &pe) {
		if strings.EqualFold(windows.UTF16ToString(pe.ExeFile[:]), probeExe) {
			pid = pe.ProcessID
			break
		}
	}
	if pid == 0 {
		return 0, 0, 0, fmt.Errorf("the game (%s) is not running", probeExe)
	}
	ms, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPMODULE|windows.TH32CS_SNAPMODULE32, pid)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("listing game modules: %w", err)
	}
	defer windows.CloseHandle(ms)
	var me windows.ModuleEntry32
	me.Size = uint32(unsafe.Sizeof(me))
	for e := windows.Module32First(ms, &me); e == nil; e = windows.Module32Next(ms, &me) {
		if strings.EqualFold(windows.UTF16ToString(me.Module[:]), probeExe) {
			return pid, uint64(me.ModBaseAddr), me.ModBaseSize, nil
		}
	}
	return 0, 0, 0, fmt.Errorf("game module not found")
}

func loadVtables(base uint64) map[uint64]string {
	m := make(map[uint64]string, 10000)
	for _, ln := range strings.Split(probeVtables, "\n") {
		f := strings.SplitN(strings.TrimSpace(ln), " ", 2)
		if len(f) != 2 {
			continue
		}
		var rva uint64
		if _, err := fmt.Sscanf(f[0], "%x", &rva); err == nil {
			m[base+rva] = f[1]
		}
	}
	return m
}

func readMem(h windows.Handle, addr uint64, buf []byte) bool {
	var n uintptr
	err := windows.ReadProcessMemory(h, uintptr(addr), &buf[0], uintptr(len(buf)), &n)
	return err == nil && int(n) == len(buf)
}

func scanOnce(h windows.Handle, base uint64, vt map[uint64]string) []probeHit {
	var hits []probeHit
	chunk := make([]byte, 4<<20)
	node := make([]byte, nodeReadSize)
	var addr uintptr
	for {
		var mbi windows.MemoryBasicInformation
		if err := windows.VirtualQueryEx(h, addr, &mbi, unsafe.Sizeof(mbi)); err != nil {
			break
		}
		next := mbi.BaseAddress + mbi.RegionSize
		rw := mbi.Protect&(windows.PAGE_READWRITE|windows.PAGE_EXECUTE_READWRITE|windows.PAGE_WRITECOPY) != 0
		if mbi.State == windows.MEM_COMMIT && mbi.Type == memPrivate && rw && mbi.Protect&windows.PAGE_GUARD == 0 {
			for off := uintptr(0); off < mbi.RegionSize; off += uintptr(len(chunk)) {
				n := uintptr(len(chunk))
				if off+n > mbi.RegionSize {
					n = mbi.RegionSize - off
				}
				b := chunk[:n]
				if !readMem(h, uint64(mbi.BaseAddress+off), b) {
					continue
				}
				for i := 0; i+8 <= len(b); i += 8 {
					v := binary.LittleEndian.Uint64(b[i:])
					if v < base+rdataStart || v >= base+rdataEnd {
						continue
					}
					name, ok := vt[v]
					if !ok {
						continue
					}
					at := uint64(mbi.BaseAddress+off) + uint64(i)
					if !readMem(h, at, node) {
						continue
					}
					st := binary.LittleEndian.Uint64(node[stateOff:])
					if st < base+textStart || st >= base+textEnd {
						continue
					}
					raw := make([]byte, len(node))
					copy(raw, node)
					hits = append(hits, probeHit{
						addr: at, class: name, state: uint32(st - base),
						sub:  binary.LittleEndian.Uint32(node[stateOff+8:]),
						kids: binary.LittleEndian.Uint32(node[childCountOff:]),
						raw:  raw,
					})
				}
			}
		}
		if next <= addr {
			break
		}
		addr = next
	}
	return hits
}

// runProbe takes two snapshots a few seconds apart and writes what it found to outPath.
func runProbe(outPath string) error {
	pid, base, size, err := findGame()
	if err != nil {
		return err
	}
	h, err := windows.OpenProcess(windows.PROCESS_VM_READ|windows.PROCESS_QUERY_INFORMATION, false, pid)
	if err != nil {
		return fmt.Errorf("opening the game: %w (if the game runs as administrator, run this as administrator too)", err)
	}
	defer windows.CloseHandle(h)

	hdr := make([]byte, 0x400)
	if !readMem(h, base, hdr) {
		return fmt.Errorf("cannot read the game header")
	}
	peOff := binary.LittleEndian.Uint32(hdr[0x3c:])
	ts := uint32(0)
	if int(peOff)+12 <= len(hdr) {
		ts = binary.LittleEndian.Uint32(hdr[peOff+8:])
	}
	if ts != probeTimestamp || size != probeImageSize {
		return fmt.Errorf("this game exe is a different build (stamp %08x, size %x); the probe only knows 1.04.27", ts, size)
	}

	vt := loadVtables(base)
	t0 := time.Now()
	first := scanOnce(h, base, vt)
	time.Sleep(3 * time.Second)
	second := scanOnce(h, base, vt)
	took := time.Since(t0)

	before := map[uint64]probeHit{}
	for _, x := range first {
		before[x.addr] = x
	}
	sort.Slice(second, func(i, j int) bool {
		if second[i].class != second[j].class {
			return second[i].class < second[j].class
		}
		return second[i].addr < second[j].addr
	})

	var sb strings.Builder
	fmt.Fprintf(&sb, "Boost Link game probe %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&sb, "scan took %s, %d nodes in snapshot 1, %d in snapshot 2\n", took.Round(time.Millisecond), len(first), len(second))
	fmt.Fprintf(&sb, "columns: class | object offset | state fn RVA (+0x140000000 for Ghidra) | phase | children | same 3s earlier\n\n")
	for _, x := range second {
		same := "new"
		if p, ok := before[x.addr]; ok {
			if p.state == x.state && p.class == x.class {
				same = "stuck"
			} else {
				same = fmt.Sprintf("was %x", p.state)
			}
		}
		fmt.Fprintf(&sb, "%-60s %012x  state %08x  phase %d  kids %d  %s\n", x.class, x.addr, x.state, x.sub, x.kids, same)
	}
	sb.WriteString("\nraw fields of battle-flow, mediator and party nodes (offset 0x480 on)\n")
	for _, x := range second {
		c := x.class
		if !(strings.Contains(c, "BttleFlow") || strings.Contains(c, "Mediator") || strings.Contains(c, "Party") || strings.Contains(c, "Rpc") || strings.Contains(c, "Result")) {
			continue
		}
		fmt.Fprintf(&sb, "\n== %s %012x state %08x\n", c, x.addr, x.state)
		for o := 0x480; o < len(x.raw); o += 16 {
			fmt.Fprintf(&sb, "%04x:", o)
			for k := 0; k < 16; k += 8 {
				v := binary.LittleEndian.Uint64(x.raw[o+k:])
				if v >= base && v < base+uint64(size) {
					fmt.Fprintf(&sb, "  exe+%07x       ", v-base)
				} else {
					fmt.Fprintf(&sb, "  %016x", v)
				}
			}
			sb.WriteByte('\n')
		}
	}
	return os.WriteFile(outPath, []byte(sb.String()), 0644)
}
