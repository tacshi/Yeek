package desktop

import (
	"encoding/json/v2"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// pathSegment is one step of a JSON path: an object key or an array index.
type pathSegment struct {
	key     string
	index   int
	isIndex bool
}

// jsonOutline maps each token of a JSON document (keys, values, colons and
// brackets) to the member it belongs to, in rune offsets, so the path under
// the caret is a lookup. It tolerates malformed input by keeping what it read.
type jsonOutline struct {
	spans []jsonSpan
	nodes []jsonNode // nodes[0] is the root value
}
type jsonSpan struct{ from, to, node int }
type jsonNode struct {
	parent int
	seg    pathSegment
}

const maxJSONDepth = 512

func newJSONOutline(text string) *jsonOutline {
	o := &jsonOutline{nodes: []jsonNode{{parent: -1}}}
	s := &jsonScanner{runes: []rune(text), out: o}
	s.skip()
	if s.pos < len(s.runes) {
		s.value(0, 0)
	}
	return o
}

type jsonScanner struct {
	runes []rune
	pos   int
	out   *jsonOutline
	err   bool
}

func (s *jsonScanner) skip() {
	for s.pos < len(s.runes) && unicode.IsSpace(s.runes[s.pos]) {
		s.pos++
	}
}
func (s *jsonScanner) mark(from, node int) {
	s.out.spans = append(s.out.spans, jsonSpan{from, s.pos, node})
}
func (s *jsonScanner) child(parent int, seg pathSegment) int {
	s.out.nodes = append(s.out.nodes, jsonNode{parent, seg})
	return len(s.out.nodes) - 1
}

// value reads one value belonging to node.
func (s *jsonScanner) value(node, depth int) {
	if s.err || s.pos >= len(s.runes) || depth > maxJSONDepth {
		s.err = true
		return
	}
	start := s.pos
	switch s.runes[s.pos] {
	case '{':
		s.pos++
		s.mark(start, node)
		for !s.err {
			s.skip()
			if s.pos >= len(s.runes) {
				s.err = true
				return
			}
			if s.runes[s.pos] == '}' {
				s.pos++
				s.mark(s.pos-1, node)
				return
			}
			if s.runes[s.pos] == ',' {
				s.pos++
				continue
			}
			keyStart := s.pos
			raw, ok := s.str()
			if !ok {
				s.err = true
				return
			}
			key := raw
			if err := json.Unmarshal([]byte(raw), &key); err != nil {
				key = strings.Trim(raw, `"`)
			}
			member := s.child(node, pathSegment{key: key})
			s.mark(keyStart, member)
			s.skip()
			if s.pos >= len(s.runes) || s.runes[s.pos] != ':' {
				s.err = true
				return
			}
			s.pos++
			s.mark(s.pos-1, member)
			s.skip()
			s.value(member, depth+1)
		}
	case '[':
		s.pos++
		s.mark(start, node)
		index := 0
		for !s.err {
			s.skip()
			if s.pos >= len(s.runes) {
				s.err = true
				return
			}
			if s.runes[s.pos] == ']' {
				s.pos++
				s.mark(s.pos-1, node)
				return
			}
			if s.runes[s.pos] == ',' {
				s.pos++
				continue
			}
			s.value(s.child(node, pathSegment{index: index, isIndex: true}), depth+1)
			index++
		}
	case '"':
		if _, ok := s.str(); !ok {
			s.err = true
			return
		}
		s.mark(start, node)
	default:
		for s.pos < len(s.runes) && !strings.ContainsRune(",:]}", s.runes[s.pos]) && !unicode.IsSpace(s.runes[s.pos]) {
			s.pos++
		}
		if s.pos == start {
			s.err = true
			return
		}
		s.mark(start, node)
	}
}

// str reads a JSON string and returns its source text.
func (s *jsonScanner) str() (string, bool) {
	if s.pos >= len(s.runes) || s.runes[s.pos] != '"' {
		return "", false
	}
	start := s.pos
	s.pos++
	for s.pos < len(s.runes) {
		switch s.runes[s.pos] {
		case '\\':
			s.pos += 2
			continue
		case '"':
			s.pos++
			return string(s.runes[start:s.pos]), true
		case '\n':
			return "", false
		}
		s.pos++
	}
	return "", false
}

// pathAt returns the path of the member on the caret's line, following Yaak:
// the character before the caret picks the token, and a caret in whitespace
// or punctuation of a container uses the first token of its line. It is nil
// when the text is not JSON.
func (o *jsonOutline) pathAt(text string, caret int) []pathSegment {
	if len(o.spans) == 0 {
		return nil
	}
	node, ok := o.nodeAt(caret - 1)
	if !ok {
		runes := []rune(text)
		caret = max(0, min(caret, len(runes)))
		lineStart := caret
		for lineStart > 0 && runes[lineStart-1] != '\n' {
			lineStart--
		}
		first := lineStart
		for first < len(runes) && runes[first] != '\n' && unicode.IsSpace(runes[first]) {
			first++
		}
		if node, ok = o.nodeAt(first); !ok {
			node = 0
		}
	}
	path := []pathSegment{}
	for ; node > 0; node = o.nodes[node].parent {
		path = append(path, o.nodes[node].seg)
	}
	slices.Reverse(path)
	return path
}

// nodeAt finds the token covering rune offset pos.
func (o *jsonOutline) nodeAt(pos int) (int, bool) {
	if pos < 0 {
		return 0, false
	}
	i, _ := slices.BinarySearchFunc(o.spans, pos, func(s jsonSpan, pos int) int {
		switch {
		case s.to <= pos:
			return -1
		case s.from > pos:
			return 1
		}
		return 0
	})
	if i < len(o.spans) && o.spans[i].from <= pos && pos < o.spans[i].to {
		return o.spans[i].node, true
	}
	return 0, false
}

var bareJSONKey = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// jsonPathString writes the first count segments as JSONPath, choosing dot or
// quoted bracket form for each key as Yaak does; no segments is the root, "$".
func jsonPathString(path []pathSegment, count int) string {
	var b strings.Builder
	b.WriteString("$")
	for _, seg := range path[:min(count, len(path))] {
		switch {
		case seg.isIndex:
			b.WriteString("[" + strconv.Itoa(seg.index) + "]")
		case bareJSONKey.MatchString(seg.key) && seg.key != "$":
			b.WriteString("." + seg.key)
		default:
			quoted, _ := json.Marshal(seg.key)
			b.WriteString("[" + string(quoted) + "]")
		}
	}
	return b.String()
}

// parseJSONPath reads a plain JSONPath of keys and indexes, such as
// $.data[0]["a b"], and reports false for anything else (wildcards, filters).
func parseJSONPath(expr string) ([]pathSegment, bool) {
	expr = strings.TrimSpace(expr)
	if !strings.HasPrefix(expr, "$") {
		return nil, false
	}
	rest := expr[1:]
	path := []pathSegment{}
	for rest != "" {
		switch {
		case strings.HasPrefix(rest, "."):
			end := 1
			for end < len(rest) && rest[end] != '.' && rest[end] != '[' {
				end++
			}
			key := rest[1:end]
			if !bareJSONKey.MatchString(key) {
				return nil, false
			}
			path = append(path, pathSegment{key: key})
			rest = rest[end:]
		case strings.HasPrefix(rest, `["`):
			end := 2
			for end < len(rest) && (rest[end] != '"' || rest[end-1] == '\\') {
				end++
			}
			if end+1 >= len(rest) || rest[end+1] != ']' {
				return nil, false
			}
			var key string
			if json.Unmarshal([]byte(rest[1:end+1]), &key) != nil {
				return nil, false
			}
			path = append(path, pathSegment{key: key})
			rest = rest[end+2:]
		case strings.HasPrefix(rest, "["):
			end := strings.IndexByte(rest, ']')
			if end < 0 {
				return nil, false
			}
			index, err := strconv.Atoi(rest[1:end])
			if err != nil || index < 0 {
				return nil, false
			}
			path = append(path, pathSegment{index: index, isIndex: true})
			rest = rest[end+1:]
		default:
			return nil, false
		}
	}
	return path, true
}
