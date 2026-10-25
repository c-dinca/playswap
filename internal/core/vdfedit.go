package core

import (
	"errors"
	"strings"
)

// Targeted edits of Valve KeyValues text: change one value and leave the
// rest of the file byte-for-byte as it was.

type vtok struct {
	kind       byte // 's' string, '{', '}'
	val        string
	start, end int // byte span in the source (including quotes)
}

func vdfScan(s string) ([]vtok, error) {
	var out []vtok
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		case c == '/' && i+1 < len(s) && s[i+1] == '/':
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case c == '{' || c == '}':
			out = append(out, vtok{kind: c, start: i, end: i + 1})
			i++
		case c == '"':
			start := i
			i++
			var b strings.Builder
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' && i+1 < len(s) {
					i++
					switch s[i] {
					case 'n':
						b.WriteByte('\n')
					case 't':
						b.WriteByte('\t')
					default:
						b.WriteByte(s[i])
					}
				} else {
					b.WriteByte(s[i])
				}
				i++
			}
			if i >= len(s) {
				return nil, errors.New("vdf: unterminated string")
			}
			i++
			out = append(out, vtok{kind: 's', val: b.String(), start: start, end: i})
		case c == '[':
			for i < len(s) && s[i] != ']' {
				i++
			}
			i++
		default:
			start := i
			for i < len(s) && !strings.ContainsRune(" \t\r\n{}\"", rune(s[i])) {
				i++
			}
			out = append(out, vtok{kind: 's', val: s[start:i], start: start, end: i})
		}
	}
	return out, nil
}

// vdfQuote escapes a value the way Steam writes it.
func vdfQuote(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	return `"` + v + `"`
}

// block is a { ... } region: token index of '{' and of the matching '}'.
type block struct{ open, close int }

// children walks the direct entries of a block. For each key it reports the
// key token index and either the value token index (string) or the sub-block.
func children(toks []vtok, b block, f func(key int, val int, sub *block) bool) error {
	i := b.open + 1
	for i < b.close {
		if toks[i].kind != 's' {
			return errors.New("vdf: expected key")
		}
		key := i
		i++
		if i >= b.close {
			return errors.New("vdf: missing value")
		}
		switch toks[i].kind {
		case 's':
			if !f(key, i, nil) {
				return nil
			}
			i++
		case '{':
			depth, j := 0, i
			for ; j < len(toks); j++ {
				if toks[j].kind == '{' {
					depth++
				} else if toks[j].kind == '}' {
					depth--
					if depth == 0 {
						break
					}
				}
			}
			if j >= len(toks) {
				return errors.New("vdf: unbalanced braces")
			}
			sub := block{i, j}
			if !f(key, -1, &sub) {
				return nil
			}
			i = j + 1
		default:
			return errors.New("vdf: unexpected }")
		}
	}
	return nil
}

func lineIndent(s string, pos int) string {
	ls := strings.LastIndexByte(s[:pos], '\n') + 1
	j := ls
	for j < len(s) && (s[j] == '\t' || s[j] == ' ') {
		j++
	}
	return s[ls:j]
}

// SetVDFValue sets path/.../key to value (creating missing blocks), keeping
// everything else in the text unchanged. Keys match case-insensitively.
func SetVDFValue(text string, path []string, key, value string) (string, error) {
	if len(path) == 0 {
		return "", errors.New("vdf: empty path")
	}
	toks, err := vdfScan(text)
	if err != nil {
		return "", err
	}
	bal := 0
	for _, t := range toks {
		if t.kind == '{' {
			bal++
		} else if t.kind == '}' {
			bal--
		}
		if bal < 0 {
			return "", errors.New("vdf: unbalanced braces")
		}
	}
	if bal != 0 {
		return "", errors.New("vdf: unbalanced braces")
	}
	// The root is a virtual block around everything.
	toks = append([]vtok{{kind: '{', start: -1, end: -1}}, toks...)
	toks = append(toks, vtok{kind: '}', start: len(text), end: len(text)})
	cur := block{0, len(toks) - 1}
	depth := 0
	for pi, name := range path {
		var found *block
		if err := children(toks, cur, func(k, v int, sub *block) bool {
			if sub != nil && strings.EqualFold(toks[k].val, name) {
				found = sub
				return false
			}
			return true
		}); err != nil {
			return "", err
		}
		if found == nil {
			// Build the missing blocks and the value, insert before cur's '}'.
			return insertBefore(text, toks, cur, depth, path[pi:], key, value), nil
		}
		cur = *found
		depth++
	}
	var valTok = -1
	if err := children(toks, cur, func(k, v int, sub *block) bool {
		if sub == nil && strings.EqualFold(toks[k].val, key) {
			valTok = v
			return false
		}
		return true
	}); err != nil {
		return "", err
	}
	if valTok >= 0 {
		t := toks[valTok]
		return text[:t.start] + vdfQuote(value) + text[t.end:], nil
	}
	return insertBefore(text, toks, cur, depth, nil, key, value), nil
}

// insertBefore adds `"a" { "b" { "key" "value" } }` just before the closing
// brace of block b, indented with tabs like Steam does.
func insertBefore(text string, toks []vtok, b block, depth int, missing []string, key, value string) string {
	var sb strings.Builder
	ind := func(n int) string { return strings.Repeat("\t", n) }
	d := depth
	for _, m := range missing {
		sb.WriteString(ind(d) + vdfQuote(m) + "\n" + ind(d) + "{\n")
		d++
	}
	sb.WriteString(ind(d) + vdfQuote(key) + "\t\t" + vdfQuote(value) + "\n")
	for i := len(missing) - 1; i >= 0; i-- {
		d--
		sb.WriteString(ind(d) + "}\n")
	}
	pos := toks[b.close].start
	if pos >= len(text) { // virtual root: append at the end
		if len(text) > 0 && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		return text + sb.String()
	}
	// insert at the start of the line holding the closing brace
	ls := strings.LastIndexByte(text[:pos], '\n') + 1
	if strings.TrimSpace(text[ls:pos]) != "" {
		// brace shares a line with content: put ours on a new line before it
		return text[:pos] + "\n" + sb.String() + lineIndent(text, pos) + text[pos:]
	}
	return text[:ls] + sb.String() + text[ls:]
}

// SteamAppsPath is where a game's per-user settings live in localconfig.vdf.
func SteamAppsPath(appID int) []string {
	return []string{"UserLocalConfigStore", "Software", "Valve", "Steam", "apps", itoa(appID)}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
