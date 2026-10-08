package desktop

import (
	"cmp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/egoist/mygo/yeekui"
	"golang.org/x/net/html"
)

func editorFolds(source, language string) []ui.CodeFold {
	if language == "xml" || language == "html" {
		return markupFolds(source)
	}
	if language == "yaml" || language == "python" {
		return indentationFolds(source)
	}
	lexer := lexers.Get(language)
	if lexer == nil {
		return nil
	}
	iterator, err := lexer.Tokenise(&chroma.TokeniseOptions{State: "root", EnsureLF: false}, source)
	if err != nil {
		return nil
	}
	type opening struct {
		char         rune
		offset, line int
	}
	stack := []opening{}
	folds := []ui.CodeFold{}
	offset, line := 0, 0
	for token := iterator(); token != chroma.EOF; token = iterator() {
		skip := token.Type.InSubCategory(chroma.LiteralString) || token.Type.InCategory(chroma.Comment)
		for _, r := range token.Value {
			if !skip {
				switch r {
				case '{', '[', '(':
					stack = append(stack, opening{r, offset, line})
				case '}', ']', ')':
					if len(stack) > 0 {
						last := stack[len(stack)-1]
						match := last.char == '{' && r == '}' || last.char == '[' && r == ']' || last.char == '(' && r == ')'
						if match {
							stack = stack[:len(stack)-1]
							if last.line < line {
								folds = append(folds, ui.CodeFold{Start: last.offset, End: offset + 1})
							}
						}
					}
				}
			}
			if r == '\n' {
				line++
			}
			offset++
		}
	}
	slices.SortFunc(folds, func(a, b ui.CodeFold) int {
		if a.Start == b.Start {
			return cmp.Compare(b.End, a.End)
		}
		return cmp.Compare(a.Start, b.Start)
	})
	return folds
}
func markupFolds(source string) []ui.CodeFold {
	type opening struct {
		name        string
		start, line int
	}
	stack := []opening{}
	folds := []ui.CodeFold{}
	tokenizer := html.NewTokenizer(strings.NewReader(source))
	offset, line := 0, 0
	for {
		kind := tokenizer.Next()
		if kind == html.ErrorToken {
			break
		}
		raw := string(tokenizer.Raw())
		end := offset + utf8.RuneCountInString(raw)
		endLine := line + strings.Count(raw, "\n")
		switch kind {
		case html.StartTagToken:
			token := tokenizer.Token()
			if !slices.Contains([]string{"area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr"}, token.Data) {
				stack = append(stack, opening{name: token.Data, start: end - 1, line: endLine})
			}
		case html.EndTagToken:
			token := tokenizer.Token()
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i].name == token.Data {
					open := stack[i]
					stack = stack[:i]
					if open.line < endLine {
						folds = append(folds, ui.CodeFold{Start: open.start, End: end})
					}
					break
				}
			}
		}
		offset, line = end, endLine
	}
	slices.SortFunc(folds, func(a, b ui.CodeFold) int { return cmp.Compare(a.Start, b.Start) })
	return folds
}
func indentationFolds(source string) []ui.CodeFold {
	lines := strings.Split(source, "\n")
	offsets := make([]int, len(lines))
	offset := 0
	for i, line := range lines {
		offsets[i] = offset
		offset += utf8.RuneCountInString(line) + 1
	}
	type level struct{ indent, line int }
	stack := []level{}
	folds := []ui.CodeFold{}
	last := -1
	closeLevel := func(open level) {
		if last > open.line {
			folds = append(folds, ui.CodeFold{Start: offsets[open.line] + utf8.RuneCountInString(lines[open.line]), End: offsets[last] + utf8.RuneCountInString(lines[last]), Placeholder: " …"})
		}
	}
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			closeLevel(stack[len(stack)-1])
			stack = stack[:len(stack)-1]
		}
		stack = append(stack, level{indent, i})
		last = i
	}
	for i := len(stack) - 1; i >= 0; i-- {
		closeLevel(stack[i])
	}
	slices.SortFunc(folds, func(a, b ui.CodeFold) int { return cmp.Compare(a.Start, b.Start) })
	return folds
}
