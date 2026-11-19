package query

import (
	"strings"
	"unicode/utf8"
)

func FuzzyMatch(target, search string) bool {
	return fuzzyMatchIn(target, search, &fuzzyScratch{})
}

type fuzzyScratch struct {
	target []rune
	search []rune
	skips  []int
}

func fuzzyMatchIn(target, search string, scratch *fuzzyScratch) bool {
	if target == "" {
		return false
	}
	if search == "" {
		return true
	}
	quoted := strings.HasPrefix(search, `"`)
	if quoted {
		inner := search[1:]
		search = strings.TrimSuffix(inner, `"`)
	}
	if strings.Contains(target, search) {
		return true
	}
	if quoted || utf8.RuneCountInString(search) == 1 {
		return false
	}
	if isASCII(target) && isASCII(search) {
		return wordPrefixMatch([]byte(target), []byte(search), &scratch.skips)
	}
	scratch.target = append(scratch.target[:0], []rune(target)...)
	scratch.search = append(scratch.search[:0], []rune(search)...)
	return wordPrefixMatch(scratch.target, scratch.search, &scratch.skips)
}

type textUnit interface {
	~byte | ~rune
}

func isAlphaNum[T textUnit](c T) bool {
	r := rune(c)
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r >= 127
}

func isPunctuation[T textUnit](c T) bool {
	r := rune(c)
	return (r >= '!' && r <= '/') || (r >= ':' && r <= '@') || (r >= '[' && r <= '`') || (r >= '{' && r <= '~')
}

func wordPrefixMatch[T textUnit](target, search []T, skips *[]int) bool {
	targetSkips(target, skips)
	s := *skips
	for skip := range len(s) - 1 {
		if target[s[skip]] == search[0] && prefixMatch(skip, search, target, s) {
			return true
		}
	}
	return false
}

func targetSkips[T textUnit](target []T, skips *[]int) {
	s := (*skips)[:0]
	wasAlphaNum := false
	for i, c := range target {
		alphaNum := isAlphaNum(c)
		if (alphaNum && !wasAlphaNum) || isPunctuation(c) {
			s = append(s, i)
		}
		wasAlphaNum = alphaNum
	}
	*skips = append(s, len(target))
}

func prefixMatch[T textUnit](skip int, search, target []T, skips []int) bool {
	searchIdx := 0
	for ; skip < len(skips)-1; skip++ {
		targetIdx := skips[skip]
		end := skips[skip+1]
	run:
		for targetIdx < end && searchIdx < len(search) {
			t, s := target[targetIdx], search[searchIdx]
			switch {
			case t == s:
				targetIdx++
				searchIdx++
			case t == ' ':
				targetIdx++
			case s == ' ':
				searchIdx++
			default:
				break run
			}
		}
		if searchIdx == len(search) {
			return true
		}
	}
	return false
}
