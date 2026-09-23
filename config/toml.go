package config

import (
	"fmt"
	"strconv"
	"strings"
)

type tomlTableKind int

const (
	tomlSingleTable tomlTableKind = iota
	tomlArrayTable
)

type tomlValueKind int

const (
	tomlString tomlValueKind = iota
	tomlBool
	tomlStringList
)

type tomlTable struct {
	path   string
	kind   tomlTableKind
	name   string
	line   int
	values map[string]tomlValue
}

type tomlValue struct {
	kind        tomlValueKind
	stringValue string
	boolValue   bool
	strings     []string
	line        int
}

func parseTOMLFile(path string, data []byte) ([]tomlTable, error) {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	var tables []tomlTable
	var current *tomlTable
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			name, kind, ok, err := parseHeader(path, i+1, line)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, fmt.Errorf("%s: line %d: expected table header", path, i+1)
			}
			tables = append(tables, tomlTable{path: path, kind: kind, name: name, line: i + 1, values: map[string]tomlValue{}})
			current = &tables[len(tables)-1]
			continue
		}
		if current == nil {
			return nil, fmt.Errorf("%s: line %d: key outside table", path, i+1)
		}
		key, raw, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s: line %d: expected key = value", path, i+1)
		}
		key = strings.TrimSpace(key)
		raw = strings.TrimSpace(raw)
		if key == "" || raw == "" {
			return nil, fmt.Errorf("%s: line %d: expected key = value", path, i+1)
		}
		if _, exists := current.values[key]; exists {
			return nil, fmt.Errorf("%s: line %d: duplicate key %q", path, i+1, key)
		}
		value, consumed, err := parseValue(path, lines, i, raw)
		if err != nil {
			return nil, err
		}
		current.values[key] = value
		i += consumed
	}
	return tables, nil
}

func parseHeader(path string, lineNo int, line string) (string, tomlTableKind, bool, error) {
	switch {
	case strings.HasPrefix(line, "[["):
		if !strings.HasSuffix(line, "]]") {
			return "", 0, true, fmt.Errorf("%s: line %d: malformed array table header", path, lineNo)
		}
		name := strings.TrimSpace(line[2 : len(line)-2])
		if name == "" {
			return "", 0, true, fmt.Errorf("%s: line %d: empty array table name", path, lineNo)
		}
		return name, tomlArrayTable, true, nil
	case strings.HasPrefix(line, "["):
		if !strings.HasSuffix(line, "]") {
			return "", 0, true, fmt.Errorf("%s: line %d: malformed table header", path, lineNo)
		}
		name := strings.TrimSpace(line[1 : len(line)-1])
		if name == "" {
			return "", 0, true, fmt.Errorf("%s: line %d: empty table name", path, lineNo)
		}
		return name, tomlSingleTable, true, nil
	default:
		return "", 0, false, nil
	}
}

func parseValue(path string, lines []string, lineIndex int, raw string) (tomlValue, int, error) {
	switch {
	case strings.HasPrefix(raw, "\"") || strings.HasPrefix(raw, "'"):
		value, consumed, err := parseQuotedValue(path, lines, lineIndex, raw)
		if err != nil {
			return tomlValue{}, 0, err
		}
		return tomlValue{kind: tomlString, stringValue: value, line: lineIndex + 1}, consumed, nil
	case strings.HasPrefix(raw, "["):
		values, consumed, err := parseStringList(path, lines, lineIndex, raw)
		if err != nil {
			return tomlValue{}, 0, err
		}
		return tomlValue{kind: tomlStringList, strings: values, line: lineIndex + 1}, consumed, nil
	case raw == "true" || strings.HasPrefix(raw, "true "):
		if tail := strings.TrimSpace(strings.TrimPrefix(raw, "true")); tail != "" && !strings.HasPrefix(tail, "#") {
			return tomlValue{}, 0, fmt.Errorf("%s: line %d: trailing data after boolean", path, lineIndex+1)
		}
		return tomlValue{kind: tomlBool, boolValue: true, line: lineIndex + 1}, 0, nil
	case raw == "false" || strings.HasPrefix(raw, "false "):
		if tail := strings.TrimSpace(strings.TrimPrefix(raw, "false")); tail != "" && !strings.HasPrefix(tail, "#") {
			return tomlValue{}, 0, fmt.Errorf("%s: line %d: trailing data after boolean", path, lineIndex+1)
		}
		return tomlValue{kind: tomlBool, boolValue: false, line: lineIndex + 1}, 0, nil
	default:
		return tomlValue{}, 0, fmt.Errorf("%s: line %d: unsupported value %q", path, lineIndex+1, raw)
	}
}

func parseQuotedValue(path string, lines []string, lineIndex int, raw string) (string, int, error) {
	buf := raw
	consumed := 0
	for {
		value, rest, ok, multiline, err := scanQuoted(buf)
		if err != nil {
			return "", 0, fmt.Errorf("%s: line %d: %w", path, lineIndex+1, err)
		}
		if ok {
			if strings.TrimSpace(rest) != "" && !strings.HasPrefix(strings.TrimSpace(rest), "#") {
				return "", 0, fmt.Errorf("%s: line %d: trailing data after string", path, lineIndex+1)
			}
			if strings.HasPrefix(buf, "\"") {
				decoded, err := unescapeBasic(value)
				return decoded, consumed, err
			}
			return value, consumed, nil
		}
		if !multiline {
			return "", 0, fmt.Errorf("%s: line %d: unterminated string", path, lineIndex+1)
		}
		if lineIndex+consumed+1 >= len(lines) {
			return "", 0, fmt.Errorf("%s: line %d: unterminated multiline string", path, lineIndex+1)
		}
		consumed++
		buf += "\n" + lines[lineIndex+consumed]
	}
}

func parseStringList(path string, lines []string, lineIndex int, raw string) ([]string, int, error) {
	buf := raw
	consumed := 0
	for !hasListClose(buf) {
		if lineIndex+consumed+1 >= len(lines) {
			return nil, 0, fmt.Errorf("%s: line %d: unterminated array", path, lineIndex+1)
		}
		consumed++
		buf += "\n" + lines[lineIndex+consumed]
	}
	end := listCloseIndex(buf)
	if end < 0 {
		return nil, 0, fmt.Errorf("%s: line %d: unterminated array", path, lineIndex+1)
	}
	if tail := strings.TrimSpace(buf[end+1:]); tail != "" && !strings.HasPrefix(tail, "#") {
		return nil, 0, fmt.Errorf("%s: line %d: trailing data after array", path, lineIndex+1)
	}
	inner := strings.TrimSpace(buf[1:end])
	if inner == "" {
		return nil, consumed, nil
	}
	var values []string
	for len(inner) > 0 {
		inner = strings.TrimSpace(inner)
		if inner == "" {
			break
		}
		if inner[0] == ',' {
			inner = strings.TrimSpace(inner[1:])
			continue
		}
		value, rest, ok, _, err := scanQuoted(inner)
		if err != nil {
			return nil, 0, fmt.Errorf("%s: line %d: %w", path, lineIndex+1, err)
		}
		if !ok {
			return nil, 0, fmt.Errorf("%s: line %d: expected quoted string", path, lineIndex+1)
		}
		if strings.HasPrefix(inner, "\"") {
			decoded, err := unescapeBasic(value)
			if err != nil {
				return nil, 0, fmt.Errorf("%s: line %d: %w", path, lineIndex+1, err)
			}
			values = append(values, decoded)
		} else {
			values = append(values, value)
		}
		inner = strings.TrimSpace(rest)
		if inner == "" {
			break
		}
		if inner[0] == ',' {
			inner = inner[1:]
		}
	}
	return values, consumed, nil
}

func scanQuoted(raw string) (value, rest string, ok bool, multiline bool, err error) {
	if raw == "" {
		return "", "", false, false, nil
	}
	quote := raw[0]
	if quote != '"' && quote != '\'' {
		return "", "", false, false, nil
	}
	multiline = len(raw) >= 3 && raw[1] == quote && raw[2] == quote
	open := 1
	if multiline {
		open = 3
	}
	if quote == '\'' {
		if multiline {
			idx := strings.Index(raw[open:], "'''")
			if idx < 0 {
				return "", "", false, true, nil
			}
			return raw[open : open+idx], raw[open+idx+3:], true, true, nil
		}
		idx := strings.IndexByte(raw[open:], '\'')
		if idx < 0 {
			return "", "", false, false, nil
		}
		return raw[open : open+idx], raw[open+idx+1:], true, false, nil
	}
	if multiline {
		for i := open; i+2 < len(raw); i++ {
			if raw[i] != '"' {
				continue
			}
			if raw[i-1] == '\\' {
				continue
			}
			if raw[i+1] == '"' && raw[i+2] == '"' {
				return raw[open:i], raw[i+3:], true, true, nil
			}
		}
		return "", "", false, true, nil
	}
	escaped := false
	for i := open; i < len(raw); i++ {
		c := raw[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == '"' {
			return raw[open:i], raw[i+1:], true, false, nil
		}
	}
	return "", "", false, false, nil
}

func hasListClose(raw string) bool {
	inString := false
	escaped := false
	quote := byte(0)
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if quote == '"' && c == '\\' {
				escaped = true
				continue
			}
			if c == quote {
				inString = false
			}
			continue
		}
		switch c {
		case '"', '\'':
			inString = true
			quote = c
		case ']':
			return true
		}
	}
	return false
}

func listCloseIndex(raw string) int {
	inString := false
	escaped := false
	quote := byte(0)
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if quote == '"' && c == '\\' {
				escaped = true
				continue
			}
			if c == quote {
				inString = false
			}
			continue
		}
		switch c {
		case '"', '\'':
			inString = true
			quote = c
		case ']':
			return i
		}
	}
	return -1
}

func unescapeBasic(raw string) (string, error) {
	var b strings.Builder
	b.Grow(len(raw))
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		i++
		if i >= len(raw) {
			return "", fmt.Errorf("invalid escape")
		}
		switch raw[i] {
		case 'b':
			b.WriteByte('\b')
		case 't':
			b.WriteByte('\t')
		case 'n':
			b.WriteByte('\n')
		case 'f':
			b.WriteByte('\f')
		case 'r':
			b.WriteByte('\r')
		case '"':
			b.WriteByte('"')
		case '\\':
			b.WriteByte('\\')
		case 'u':
			if i+4 >= len(raw) {
				return "", fmt.Errorf("invalid unicode escape")
			}
			v, err := strconv.ParseInt(raw[i+1:i+5], 16, 32)
			if err != nil {
				return "", fmt.Errorf("invalid unicode escape")
			}
			b.WriteRune(rune(v))
			i += 4
		case 'U':
			if i+8 >= len(raw) {
				return "", fmt.Errorf("invalid unicode escape")
			}
			v, err := strconv.ParseInt(raw[i+1:i+9], 16, 32)
			if err != nil {
				return "", fmt.Errorf("invalid unicode escape")
			}
			b.WriteRune(rune(v))
			i += 8
		case '\n':
			for i+1 < len(raw) && (raw[i+1] == ' ' || raw[i+1] == '\t') {
				i++
			}
		default:
			return "", fmt.Errorf("invalid escape")
		}
	}
	return b.String(), nil
}
