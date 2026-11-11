package content

import (
	"encoding/json"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
)

// partialStrings extracts the string values of an unfinished JSON object as
// it streams in, keyed by property name (nesting is ignored: the reply's
// property names are unique). A value that is still being written is returned
// as far as it can be decoded; a trailing incomplete escape or UTF-8 sequence
// is held back until the next chunk completes it.
func partialStrings(buf string) map[string]string {
	out := map[string]string{}
	var key string
	expectValue := false
	for i := 0; i < len(buf); {
		switch c := buf[i]; {
		case c == '"':
			s, next, complete := readString(buf, i+1)
			if expectValue {
				out[key] = s
				expectValue = false
			} else if complete {
				key = s
			}
			i = next
		case c == ':':
			expectValue = true
			i++
		case c == ' ' || c == '\n' || c == '\r' || c == '\t':
			i++
		default:
			expectValue = false // object, array, number, literal: not a string value
			i++
		}
	}
	return out
}

// readString decodes a JSON string body starting at buf[start] (just after
// the opening quote). It returns the decoded text, the index after the
// closing quote (or len(buf)) and whether the string was closed.
func readString(buf string, start int) (string, int, bool) {
	for i := start; i < len(buf); i++ {
		switch buf[i] {
		case '\\':
			i++ // skip the escaped character
		case '"':
			var s string
			if err := json.Unmarshal([]byte(buf[start-1:i+1]), &s); err == nil {
				return s, i + 1, true
			}
			return decodePartial(buf[start:i]), i + 1, true
		}
	}
	return decodePartial(buf[start:]), len(buf), false
}

// decodePartial decodes as much of an unterminated JSON string body as is
// complete, dropping a trailing partial escape or rune.
func decodePartial(s string) string {
	out := make([]rune, 0, len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c != '\\' {
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size <= 1 && !utf8.FullRuneInString(s[i:]) {
				break // rune split across chunks
			}
			out = append(out, r)
			i += size
			continue
		}
		if i+1 >= len(s) {
			break
		}
		switch e := s[i+1]; e {
		case 'n':
			out = append(out, '\n')
		case 't':
			out = append(out, '\t')
		case 'r':
			out = append(out, '\r')
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'u':
			r, n, ok := decodeUnicode(s[i:])
			if !ok {
				return string(out)
			}
			out = append(out, r)
			i += n
			continue
		default: // " \ /
			out = append(out, rune(e))
		}
		i += 2
	}
	return string(out)
}

// decodeUnicode decodes \uXXXX (and a following low surrogate) at the start
// of s. ok is false when the sequence is not complete yet.
func decodeUnicode(s string) (rune, int, bool) {
	hex := func(at int) (rune, bool) {
		if len(s) < at+6 || s[at] != '\\' || s[at+1] != 'u' {
			return 0, false
		}
		v, err := strconv.ParseUint(s[at+2:at+6], 16, 16)
		return rune(v), err == nil
	}
	r, ok := hex(0)
	if !ok {
		return 0, 0, false
	}
	if utf16.IsSurrogate(r) {
		r2, ok := hex(6)
		if !ok {
			return 0, 0, false
		}
		return utf16.DecodeRune(r, r2), 12, true
	}
	return r, 6, true
}
