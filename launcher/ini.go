package main

import (
	"os"
	"strings"
)

// IniFile is a line-preserving INI editor. It only rewrites the lines whose
// values change, so comments, spacing and key order in the original file stay
// exactly as they were.
type IniFile struct {
	lines []string
	crlf  bool
	bom   bool
}

func LoadIni(path string) (*IniFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseIni(string(b)), nil
}

func ParseIni(s string) *IniFile {
	f := &IniFile{}
	if strings.HasPrefix(s, "\uFEFF") {
		f.bom = true
		s = strings.TrimPrefix(s, "\uFEFF")
	}
	f.crlf = strings.Contains(s, "\r\n")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	f.lines = strings.Split(s, "\n")
	return f
}

func (f *IniFile) String() string {
	nl := "\n"
	if f.crlf {
		nl = "\r\n"
	}
	s := strings.Join(f.lines, nl)
	if f.bom {
		s = "\uFEFF" + s
	}
	return s
}

func (f *IniFile) Save(path string) error {
	return os.WriteFile(path, []byte(f.String()), 0644)
}

func sectionName(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if len(t) >= 2 && t[0] == '[' && t[len(t)-1] == ']' {
		return strings.TrimSpace(t[1 : len(t)-1]), true
	}
	return "", false
}

func isComment(line string) bool {
	t := strings.TrimSpace(line)
	return t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, ";")
}

// sectionRange returns the index of the section header and the end (exclusive)
// of its body. header is -1 when the section doesn't exist.
func (f *IniFile) sectionRange(section string) (header, end int) {
	header = -1
	for i, l := range f.lines {
		if name, ok := sectionName(l); ok {
			if header >= 0 {
				return header, i
			}
			if strings.EqualFold(name, section) {
				header = i
			}
		}
	}
	return header, len(f.lines)
}

func splitKV(line string) (key, val string, ok bool) {
	if isComment(line) {
		return "", "", false
	}
	i := strings.Index(line, "=")
	if i < 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:]), true
}

type KV struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Entries lists the active key=value lines of a section, in file order.
func (f *IniFile) Entries(section string) []KV {
	h, end := f.sectionRange(section)
	if h < 0 {
		return nil
	}
	var out []KV
	for i := h + 1; i < end; i++ {
		if k, v, ok := splitKV(f.lines[i]); ok {
			out = append(out, KV{k, v})
		}
	}
	return out
}

func (f *IniFile) Get(section, key string) (string, bool) {
	h, end := f.sectionRange(section)
	if h < 0 {
		return "", false
	}
	for i := h + 1; i < end; i++ {
		if k, v, ok := splitKV(f.lines[i]); ok && strings.EqualFold(k, key) {
			return v, true
		}
	}
	return "", false
}

func (f *IniFile) Has(section, key string) bool {
	_, ok := f.Get(section, key)
	return ok
}

// Set changes the first matching key in place (keeping whatever comes before
// the '='), or adds the key at the end of the section, creating it if needed.
func (f *IniFile) Set(section, key, value string) {
	h, end := f.sectionRange(section)
	if h < 0 {
		if n := len(f.lines); n > 0 && strings.TrimSpace(f.lines[n-1]) != "" {
			f.lines = append(f.lines, "")
		}
		f.lines = append(f.lines, "["+section+"]", key+"="+value)
		return
	}
	for i := h + 1; i < end; i++ {
		l := f.lines[i]
		if k, v, ok := splitKV(l); ok && strings.EqualFold(k, key) {
			if v == value {
				return
			}
			eq := strings.Index(l, "=")
			f.lines[i] = l[:eq+1] + value
			return
		}
	}
	// insert after the last non-blank line of the section
	at := h + 1
	for i := h + 1; i < end; i++ {
		if strings.TrimSpace(f.lines[i]) != "" {
			at = i + 1
		}
	}
	f.lines = append(f.lines[:at], append([]string{key + "=" + value}, f.lines[at:]...)...)
}

// Comment turns every active key=value line for key in the section into a
// "# key=value" comment. It reports whether anything changed.
func (f *IniFile) Comment(section, key string) bool {
	h, end := f.sectionRange(section)
	if h < 0 {
		return false
	}
	changed := false
	for i := h + 1; i < end; i++ {
		if k, _, ok := splitKV(f.lines[i]); ok && strings.EqualFold(k, key) {
			f.lines[i] = "# " + strings.TrimLeft(f.lines[i], " \t")
			changed = true
		}
	}
	return changed
}
