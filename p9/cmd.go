// Package p9 implements the 9P filesystem for 9beads.
package p9

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ParseArgs splits a command string into arguments, handling single/double
// quotes and backslash escaping (including \uXXXX unicode escapes).
func ParseArgs(input string) ([]string, error) {
	var args []string
	var cur strings.Builder
	wasQuoted := false
	i := 0
	for i < len(input) {
		c := input[i]
		switch {
		case c == ' ' || c == '\t':
			if cur.Len() > 0 || wasQuoted {
				args = append(args, cur.String())
				cur.Reset()
				wasQuoted = false
			}
			i++
		case c == '\'':
			wasQuoted = true
			i++
			for i < len(input) && input[i] != '\'' {
				cur.WriteByte(input[i])
				i++
			}
			if i >= len(input) {
				return nil, fmt.Errorf("unterminated single quote")
			}
			i++ // skip closing '
		case c == '"':
			wasQuoted = true
			i++
			for i < len(input) && input[i] != '"' {
				if input[i] == '\\' && i+1 < len(input) {
					r, n := parseEscape(input[i+1:])
					cur.WriteRune(r)
					i += 1 + n
				} else {
					cur.WriteByte(input[i])
					i++
				}
			}
			if i >= len(input) {
				return nil, fmt.Errorf("unterminated double quote")
			}
			i++ // skip closing "
		case c == '\\':
			if i+1 >= len(input) {
				return nil, fmt.Errorf("trailing backslash")
			}
			r, n := parseEscape(input[i+1:])
			cur.WriteRune(r)
			i += 1 + n
		default:
			cur.WriteByte(c)
			i++
		}
	}
	if cur.Len() > 0 || wasQuoted {
		args = append(args, cur.String())
	}
	return args, nil
}

// parseEscape interprets the escape sequence starting at s (after the backslash).
// Returns the rune and number of bytes consumed from s.
func parseEscape(s string) (rune, int) {
	if len(s) == 0 {
		return '\\', 0
	}
	switch s[0] {
	case 'n':
		return '\n', 1
	case 't':
		return '\t', 1
	case '\\':
		return '\\', 1
	case '\'':
		return '\'', 1
	case '"':
		return '"', 1
	case 'u':
		if len(s) >= 5 {
			if v, err := strconv.ParseUint(s[1:5], 16, 32); err == nil {
				r := rune(v)
				if utf8.ValidRune(r) {
					return r, 5
				}
			}
		}
		return unicode.ReplacementChar, 1
	default:
		return rune(s[0]), 1
	}
}

// ParseKV extracts key=value pairs from args. Returns the remaining positional
// args and the map of key-value pairs.
func ParseKV(args []string) ([]string, map[string]string) {
	var pos []string
	kv := make(map[string]string)
	for _, a := range args {
		if k, v, ok := strings.Cut(a, "="); ok && k != "" {
			kv[k] = v
		} else {
			pos = append(pos, a)
		}
	}
	return pos, kv
}

// ParseBatchCreate parses a JSON array of bead creation objects.
func ParseBatchCreate(jsonStr string) ([]map[string]interface{}, error) {
	var batch []map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &batch); err != nil {
		return nil, fmt.Errorf("invalid JSON array: %w", err)
	}
	return batch, nil
}
