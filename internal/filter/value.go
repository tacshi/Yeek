package filter

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// The slice of JavaScript values and coercions the safe evaluator needs:
// Yaak's crates/yaak-filter/src/jsonpath/value.rs.

type jsKind uint8

const (
	jsUndefined jsKind = iota
	jsNull
	jsBool
	jsNumber
	jsString
	// jsDoc is an array or object of the document, compared by identity.
	jsDoc
	jsArrayKind
	jsRegexKind
	jsFunction
)

type jsValue struct {
	kind jsKind
	b    bool
	n    float64
	s    string
	doc  *jsonNode
	arr  *jsArray
	re   *jsRegex
	fn   *boundMethod
}

type jsArray struct {
	items []jsValue
	// match is set on the array String.prototype.match returns.
	match *matchInfo
}

type matchInfo struct {
	index float64
	input string
}

type boundMethod struct {
	receiver jsValue
	method   method
}

type method uint8

const (
	stringIncludes method = iota
	stringStartsWith
	stringEndsWith
	stringIndexOf
	stringLastIndexOf
	stringToLowerCase
	stringToUpperCase
	stringTrim
	stringTrimStart
	stringTrimEnd
	stringSlice
	stringSubstring
	stringSplit
	stringMatch
	stringToString
	arrayIncludes
	arrayIndexOf
	arrayJoin
	arrayToString
	numberToStringMethod
	numberToFixed
	regexTest
)

type namedMethod struct {
	name   string
	method method
}

var (
	stringMethods = []namedMethod{
		{"includes", stringIncludes}, {"startsWith", stringStartsWith}, {"endsWith", stringEndsWith},
		{"indexOf", stringIndexOf}, {"lastIndexOf", stringLastIndexOf}, {"toLowerCase", stringToLowerCase},
		{"toUpperCase", stringToUpperCase}, {"trim", stringTrim}, {"trimStart", stringTrimStart},
		{"trimEnd", stringTrimEnd}, {"slice", stringSlice}, {"substring", stringSubstring},
		{"split", stringSplit}, {"match", stringMatch}, {"toString", stringToString},
	}
	arrayMethods  = []namedMethod{{"includes", arrayIncludes}, {"indexOf", arrayIndexOf}, {"join", arrayJoin}, {"toString", arrayToString}}
	numberMethods = []namedMethod{{"toString", numberToStringMethod}, {"toFixed", numberToFixed}}
	regexMethods  = []namedMethod{{"test", regexTest}}
)

func (m method) name() string {
	for _, table := range [][]namedMethod{stringMethods, arrayMethods, numberMethods, regexMethods} {
		for _, entry := range table {
			if entry.method == m {
				return entry.name
			}
		}
	}
	return ""
}

// isAllowedMethodName reports whether a call may use the method; anything
// else is rejected before evaluation.
func isAllowedMethodName(name string) bool {
	for _, table := range [][]namedMethod{stringMethods, arrayMethods, numberMethods, regexMethods} {
		for _, entry := range table {
			if entry.name == name {
				return true
			}
		}
	}
	return false
}

var (
	stringProto = []string{
		"anchor", "at", "big", "blink", "bold", "charAt", "charCodeAt", "codePointAt", "concat",
		"fontcolor", "fontsize", "fixed", "isWellFormed", "italics", "link", "localeCompare",
		"matchAll", "normalize", "padEnd", "padStart", "repeat", "replace", "replaceAll", "search",
		"small", "strike", "sub", "substr", "sup", "toWellFormed", "trimLeft", "trimRight",
		"toLocaleLowerCase", "toLocaleUpperCase", "valueOf",
	}
	arrayProto = []string{
		"at", "concat", "copyWithin", "fill", "find", "findIndex", "findLast", "findLastIndex",
		"lastIndexOf", "pop", "push", "reverse", "shift", "unshift", "slice", "sort", "splice",
		"keys", "entries", "values", "forEach", "filter", "flat", "flatMap", "map", "every", "some",
		"reduce", "reduceRight", "toReversed", "toSorted", "toSpliced", "with", "toLocaleString",
	}
	numberProto   = []string{"toExponential", "toPrecision", "valueOf", "toLocaleString"}
	booleanProto  = []string{"toString", "valueOf"}
	regexProto    = []string{"exec", "compile", "toString"}
	functionProto = []string{"length", "name", "arguments", "caller", "apply", "bind", "call", "toString"}
	objectProto   = []string{"hasOwnProperty", "isPrototypeOf", "propertyIsEnumerable", "toString", "valueOf", "toLocaleString"}
	// blocked members are refused off a prototype, as jsonpath-plus's safe
	// evaluator does.
	blocked = []string{"constructor", "__proto__", "__defineGetter__", "__defineSetter__", "__lookupGetter__", "__lookupSetter__"}
)

func isJSWhitespace(c rune) bool {
	switch c {
	case '\t', '\n', '\v', '\f', '\r', ' ', '\u00A0', '\u1680', '\u2028', '\u2029', '\u202F', '\u205F', '\u3000', '\uFEFF':
		return true
	}
	return c >= '\u2000' && c <= '\u200A'
}

var (
	undefined = jsValue{kind: jsUndefined}
	null      = jsValue{kind: jsNull}
)

func jsBoolValue(b bool) jsValue      { return jsValue{kind: jsBool, b: b} }
func jsNumberValue(n float64) jsValue { return jsValue{kind: jsNumber, n: n} }
func jsStringValue(s string) jsValue  { return jsValue{kind: jsString, s: s} }
func jsArrayValue(items []jsValue) jsValue {
	return jsValue{kind: jsArrayKind, arr: &jsArray{items: items}}
}

func fromDoc(node *jsonNode) jsValue {
	switch node.kind {
	case jsonNull:
		return null
	case jsonBool:
		return jsBoolValue(node.b)
	case jsonNumber:
		return jsNumberValue(node.n)
	case jsonString:
		return jsStringValue(node.s)
	}
	return jsValue{kind: jsDoc, doc: node}
}

func (v jsValue) isDocArray() bool  { return v.kind == jsDoc && v.doc.kind == jsonArray }
func (v jsValue) isDocObject() bool { return v.kind == jsDoc && v.doc.kind == jsonObject }

func (v jsValue) truthy() bool {
	switch v.kind {
	case jsUndefined, jsNull:
		return false
	case jsBool:
		return v.b
	case jsNumber:
		return v.n != 0 && !math.IsNaN(v.n)
	case jsString:
		return v.s != ""
	}
	return true
}

func (v jsValue) isObject() bool {
	switch v.kind {
	case jsDoc, jsArrayKind, jsRegexKind, jsFunction:
		return true
	}
	return false
}

func (v jsValue) typeOf() string {
	switch v.kind {
	case jsUndefined:
		return "undefined"
	case jsBool:
		return "boolean"
	case jsNumber:
		return "number"
	case jsString:
		return "string"
	case jsFunction:
		return "function"
	}
	return "object"
}

// toPrimitive is ToPrimitive: every object here stringifies, whatever the hint.
func (v jsValue) toPrimitive() jsValue {
	if v.isObject() {
		return jsStringValue(v.toJSString())
	}
	return v
}

func (v jsValue) toNumber() float64 {
	switch v.kind {
	case jsUndefined:
		return math.NaN()
	case jsNull:
		return 0
	case jsBool:
		if v.b {
			return 1
		}
		return 0
	case jsNumber:
		return v.n
	case jsString:
		return stringToNumber(v.s)
	}
	return stringToNumber(v.toJSString())
}

func (v jsValue) toJSString() string {
	switch v.kind {
	case jsUndefined:
		return "undefined"
	case jsNull:
		return "null"
	case jsBool:
		return strconv.FormatBool(v.b)
	case jsNumber:
		return numberToString(v.n)
	case jsString:
		return v.s
	case jsDoc:
		if v.doc.kind == jsonArray {
			return joinDoc(v.doc.items, ",")
		}
		return "[object Object]"
	case jsArrayKind:
		return joinValues(v.arr.items, ",")
	case jsRegexKind:
		return "/" + v.re.source + "/" + v.re.flags
	}
	return "function () { [native code] }"
}

func (v jsValue) toInt32() int32 { return int32(v.toUint32()) } // #nosec G115 -- ToInt32 wraps, as JavaScript does.

func (v jsValue) toUint32() uint32 {
	n := v.toNumber()
	if math.IsInf(n, 0) || math.IsNaN(n) {
		return 0
	}
	m := math.Mod(math.Trunc(n), 4294967296)
	if m < 0 {
		m += 4294967296
	}
	return uint32(m)
}

// toInteger is ToIntegerOrInfinity.
func (v jsValue) toInteger() float64 {
	n := v.toNumber()
	if math.IsNaN(n) {
		return 0
	}
	return math.Trunc(n) + 0
}

func (v jsValue) sameObject(o jsValue) bool {
	if v.kind != o.kind {
		return false
	}
	switch v.kind {
	case jsDoc:
		return v.doc == o.doc
	case jsArrayKind:
		return v.arr == o.arr
	case jsRegexKind:
		return v.re == o.re
	case jsFunction:
		return v.fn == o.fn
	}
	return false
}

func (v jsValue) strictEq(o jsValue) bool {
	switch {
	case v.kind == jsUndefined && o.kind == jsUndefined, v.kind == jsNull && o.kind == jsNull:
		return true
	case v.kind == jsBool && o.kind == jsBool:
		return v.b == o.b
	case v.kind == jsNumber && o.kind == jsNumber:
		return v.n == o.n
	case v.kind == jsString && o.kind == jsString:
		return v.s == o.s
	}
	return v.sameObject(o)
}

// sameValueZero is SameValueZero, for Array.prototype.includes.
func (v jsValue) sameValueZero(o jsValue) bool {
	if v.kind == jsNumber && o.kind == jsNumber && math.IsNaN(v.n) && math.IsNaN(o.n) {
		return true
	}
	return v.strictEq(o)
}

func (v jsValue) nullish() bool { return v.kind == jsUndefined || v.kind == jsNull }

func (v jsValue) looseEq(o jsValue) bool {
	switch {
	case v.nullish() && o.nullish():
		return true
	case v.nullish() || o.nullish():
		return false
	case v.kind == jsNumber && o.kind == jsString:
		return v.n == stringToNumber(o.s)
	case v.kind == jsString && o.kind == jsNumber:
		return stringToNumber(v.s) == o.n
	case v.kind == jsBool:
		return jsNumberValue(v.toNumber()).looseEq(o)
	case o.kind == jsBool:
		return v.looseEq(jsNumberValue(o.toNumber()))
	case v.isObject() && !o.isObject():
		return v.toPrimitive().looseEq(o)
	case !v.isObject() && o.isObject():
		return v.looseEq(o.toPrimitive())
	}
	return v.strictEq(o)
}

// lessThan is IsLessThan; ok is false for JavaScript's undefined, from NaN.
func (v jsValue) lessThan(o jsValue) (less, ok bool) {
	a, b := v.toPrimitive(), o.toPrimitive()
	if a.kind == jsString && b.kind == jsString {
		return slices.Compare(utf16.Encode([]rune(a.s)), utf16.Encode([]rune(b.s))) < 0, true
	}
	x, y := a.toNumber(), b.toNumber()
	if math.IsNaN(x) || math.IsNaN(y) {
		return false, false
	}
	return x < y, true
}

func (v jsValue) add(o jsValue) jsValue {
	a, b := v.toPrimitive(), o.toPrimitive()
	if a.kind == jsString || b.kind == jsString {
		return jsStringValue(a.toJSString() + b.toJSString())
	}
	return jsNumberValue(a.toNumber() + b.toNumber())
}

func joinDoc(items []*jsonNode, separator string) string {
	parts := make([]string, len(items))
	for i, item := range items {
		if item.kind != jsonNull {
			parts[i] = fromDoc(item).toJSString()
		}
	}
	return strings.Join(parts, separator)
}

func joinValues(items []jsValue, separator string) string {
	parts := make([]string, len(items))
	for i, item := range items {
		if !item.nullish() {
			parts[i] = item.toJSString()
		}
	}
	return strings.Join(parts, separator)
}

// stringToNumber is StringToNumber.
func stringToNumber(s string) float64 {
	s = strings.TrimFunc(s, isJSWhitespace)
	if s == "" {
		return 0
	}
	for _, p := range []struct {
		prefix string
		radix  int
	}{{"0x", 16}, {"0X", 16}, {"0o", 8}, {"0O", 8}, {"0b", 2}, {"0B", 2}} {
		digits, ok := strings.CutPrefix(s, p.prefix)
		if !ok {
			continue
		}
		if digits == "" {
			return math.NaN()
		}
		n := 0.0
		for _, c := range digits {
			d := digitValue(c)
			if d < 0 || d >= p.radix {
				return math.NaN()
			}
			n = n*float64(p.radix) + float64(d)
		}
		return n
	}
	unsigned := s
	if s[0] == '+' || s[0] == '-' {
		unsigned = s[1:]
	}
	if unsigned == "Infinity" {
		if strings.HasPrefix(s, "-") {
			return math.Inf(-1)
		}
		return math.Inf(1)
	}
	i := 0
	count := func() int {
		start := i
		for i < len(unsigned) && unsigned[i] >= '0' && unsigned[i] <= '9' {
			i++
		}
		return i - start
	}
	intDigits := count()
	fracDigits := 0
	if i < len(unsigned) && unsigned[i] == '.' {
		i++
		fracDigits = count()
	}
	if intDigits+fracDigits == 0 {
		return math.NaN()
	}
	if i < len(unsigned) && (unsigned[i] == 'e' || unsigned[i] == 'E') {
		i++
		if i < len(unsigned) && (unsigned[i] == '+' || unsigned[i] == '-') {
			i++
		}
		if count() == 0 {
			return math.NaN()
		}
	}
	if i != len(unsigned) {
		return math.NaN()
	}
	return parseFloat(s)
}

func digitValue(c rune) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'Z':
		return int(c-'A') + 10
	}
	return -1
}

func toUTF16(s string) []uint16 { return utf16.Encode([]rune(s)) }

// ownProperty is an own property, as Object.hasOwn sees it.
func ownProperty(v jsValue, key string) (jsValue, bool) {
	switch v.kind {
	case jsDoc:
		if v.doc.kind == jsonObject {
			child, ok := v.doc.values[key]
			if !ok {
				return undefined, false
			}
			return fromDoc(child), true
		}
		if key == "length" {
			return jsNumberValue(float64(len(v.doc.items))), true
		}
		if i, ok := arrayIndex(key); ok && int(i) < len(v.doc.items) {
			return fromDoc(v.doc.items[i]), true
		}
	case jsArrayKind:
		if key == "length" {
			return jsNumberValue(float64(len(v.arr.items))), true
		}
		if info := v.arr.match; info != nil {
			switch key {
			case "index":
				return jsNumberValue(info.index), true
			case "input":
				return jsStringValue(info.input), true
			case "groups":
				return undefined, true
			}
		}
		if i, ok := arrayIndex(key); ok && int(i) < len(v.arr.items) {
			return v.arr.items[i], true
		}
	case jsString:
		units := toUTF16(v.s)
		if key == "length" {
			return jsNumberValue(float64(len(units))), true
		}
		if i, ok := arrayIndex(key); ok && int(i) < len(units) {
			return jsStringValue(fromUTF16(units[i : i+1])), true
		}
	case jsRegexKind:
		switch key {
		case "lastIndex":
			return jsNumberValue(float64(v.re.lastIndex)), true
		case "source":
			return jsStringValue(v.re.source), true
		case "flags":
			flags := []rune(v.re.flags)
			slices.SortFunc(flags, func(a, b rune) int { return strings.IndexRune("dgimsuvy", a) - strings.IndexRune("dgimsuvy", b) })
			return jsStringValue(string(flags)), true
		}
		for i, name := range []string{"hasIndices", "global", "ignoreCase", "multiline", "dotAll", "unicode", "unicodeSets", "sticky"} {
			if name == key {
				return jsBoolValue(strings.IndexByte(v.re.flags, "dgimsuvy"[i]) >= 0), true
			}
		}
	}
	return undefined, false
}

// getProperty is object[key] in the safe evaluator: own properties, then
// the allowlisted methods; any other inherited member is refused rather
// than guessed at.
func getProperty(object jsValue, key string) (jsValue, error) {
	if object.nullish() {
		return undefined, fmt.Errorf("Cannot read properties of %s (reading '%s')", object.toJSString(), key) //nolint:staticcheck // JavaScript's message
	}
	if own, ok := ownProperty(object, key); ok {
		return own, nil
	}
	if slices.Contains(blocked, key) {
		return undefined, fmt.Errorf("Cannot read properties of %s (reading '%s')", object.toJSString(), key) //nolint:staticcheck // JavaScript's message
	}
	var methods []namedMethod
	var proto []string
	kind := "an object"
	switch {
	case object.kind == jsString:
		methods, proto, kind = stringMethods, stringProto, "a string"
	case object.isDocArray() || object.kind == jsArrayKind:
		methods, proto, kind = arrayMethods, arrayProto, "an array"
	case object.kind == jsNumber:
		methods, proto, kind = numberMethods, numberProto, "a number"
	case object.kind == jsRegexKind:
		methods, proto, kind = regexMethods, regexProto, "a regular expression"
	case object.kind == jsBool:
		proto, kind = booleanProto, "a boolean"
	case object.kind == jsFunction:
		proto, kind = functionProto, "a function"
	}
	for _, m := range methods {
		if m.name == key {
			return jsValue{kind: jsFunction, fn: &boundMethod{receiver: object, method: m.method}}, nil
		}
	}
	if slices.Contains(proto, key) || slices.Contains(objectProto, key) {
		return undefined, fmt.Errorf("%s is not supported on %s", key, kind)
	}
	return undefined, nil
}

func findUnits(haystack, needle []uint16, from int) int {
	if len(needle) == 0 {
		if from <= len(haystack) {
			return from
		}
		return -1
	}
	for i := from; i+len(needle) <= len(haystack); i++ {
		if slices.Equal(haystack[i:i+len(needle)], needle) {
			return i
		}
	}
	return -1
}

func clampIndex(n float64, length int) int { return int(math.Max(0, math.Min(n, float64(length)))) }

// relativeIndex is a relative start or end index, as slice and includes
// take them.
func relativeIndex(n float64, length int) int {
	if n < 0 {
		return clampIndex(float64(length)+n, length)
	}
	return clampIndex(n, length)
}

func arg(args []jsValue, i int) jsValue {
	if i < len(args) {
		return args[i]
	}
	return undefined
}

func callMethod(bound *boundMethod, args []jsValue) (jsValue, error) {
	receiver := bound.receiver
	switch bound.method {
	case arrayIncludes, arrayIndexOf, arrayJoin, arrayToString:
		var items []jsValue
		switch {
		case receiver.isDocArray():
			for _, item := range receiver.doc.items {
				items = append(items, fromDoc(item))
			}
		case receiver.kind == jsArrayKind:
			items = receiver.arr.items
		}
		return arrayMethod(bound.method, items, args), nil
	case numberToStringMethod:
		n := receiver.toNumber()
		radix := 10.0
		if r := arg(args, 0); r.kind != jsUndefined {
			radix = r.toInteger()
		}
		if radix < 2 || radix > 36 {
			return undefined, errors.New("toString() radix must be between 2 and 36")
		}
		return jsStringValue(numberToRadix(n, int(radix))), nil
	case numberToFixed:
		digits := arg(args, 0).toInteger()
		if digits < 0 || digits > 100 {
			return undefined, errors.New("toFixed() digits argument must be between 0 and 100")
		}
		return jsStringValue(toFixed(receiver.toNumber(), int(digits))), nil
	case regexTest:
		if receiver.kind != jsRegexKind {
			return jsBoolValue(false), nil
		}
		ok, err := receiver.re.test(arg(args, 0).toJSString())
		return jsBoolValue(ok), err
	}
	return stringMethod(bound.method, receiver.toJSString(), args)
}

var (
	lowerCaser = cases.Lower(language.Und)
	upperCaser = cases.Upper(language.Und)
)

func stringMethod(m method, s string, args []jsValue) (jsValue, error) {
	units := toUTF16(s)
	length := len(units)
	searchString := func(i int) ([]uint16, error) {
		if arg(args, i).kind == jsRegexKind {
			return nil, fmt.Errorf("First argument to String.prototype.%s must not be a regular expression", m.name()) //nolint:staticcheck // JavaScript's message
		}
		return toUTF16(arg(args, i).toJSString()), nil
	}
	switch m {
	case stringIncludes:
		needle, err := searchString(0)
		if err != nil {
			return undefined, err
		}
		return jsBoolValue(findUnits(units, needle, clampIndex(arg(args, 1).toInteger(), length)) >= 0), nil
	case stringStartsWith:
		needle, err := searchString(0)
		if err != nil {
			return undefined, err
		}
		from := clampIndex(arg(args, 1).toInteger(), length)
		rest := units[from:]
		return jsBoolValue(len(rest) >= len(needle) && slices.Equal(rest[:len(needle)], needle)), nil
	case stringEndsWith:
		needle, err := searchString(0)
		if err != nil {
			return undefined, err
		}
		end := length
		if e := arg(args, 1); e.kind != jsUndefined {
			end = clampIndex(e.toInteger(), length)
		}
		head := units[:end]
		return jsBoolValue(len(head) >= len(needle) && slices.Equal(head[len(head)-len(needle):], needle)), nil
	case stringIndexOf:
		needle := toUTF16(arg(args, 0).toJSString())
		return jsNumberValue(float64(findUnits(units, needle, clampIndex(arg(args, 1).toInteger(), length)))), nil
	case stringLastIndexOf:
		needle := toUTF16(arg(args, 0).toJSString())
		position := arg(args, 1).toNumber()
		start := length
		if !math.IsNaN(position) {
			start = clampIndex(math.Trunc(position), length)
		}
		found := -1
		if length >= len(needle) {
			for i := min(start, length-len(needle)); i >= 0; i-- {
				if slices.Equal(units[i:i+len(needle)], needle) {
					found = i
					break
				}
			}
		}
		return jsNumberValue(float64(found)), nil
	case stringToLowerCase:
		return jsStringValue(lowerCaser.String(s)), nil
	case stringToUpperCase:
		return jsStringValue(upperCaser.String(s)), nil
	case stringTrim:
		return jsStringValue(strings.TrimFunc(s, isJSWhitespace)), nil
	case stringTrimStart:
		return jsStringValue(strings.TrimLeftFunc(s, isJSWhitespace)), nil
	case stringTrimEnd:
		return jsStringValue(strings.TrimRightFunc(s, isJSWhitespace)), nil
	case stringSlice:
		start := relativeIndex(arg(args, 0).toInteger(), length)
		end := length
		if e := arg(args, 1); e.kind != jsUndefined {
			end = relativeIndex(e.toInteger(), length)
		}
		if start < end {
			return jsStringValue(fromUTF16(units[start:end])), nil
		}
		return jsStringValue(""), nil
	case stringSubstring:
		start := clampIndex(arg(args, 0).toInteger(), length)
		end := length
		if e := arg(args, 1); e.kind != jsUndefined {
			end = clampIndex(e.toInteger(), length)
		}
		return jsStringValue(fromUTF16(units[min(start, end):max(start, end)])), nil
	case stringSplit:
		return split(s, units, arg(args, 0), arg(args, 1))
	case stringMatch:
		var re *jsRegex
		var err error
		switch a := arg(args, 0); a.kind {
		case jsRegexKind:
			re = a.re
		case jsUndefined:
			re, err = newJSRegex("(?:)", "")
		default:
			re, err = newJSRegex(a.toJSString(), "")
		}
		if err != nil {
			return undefined, err
		}
		return re.matchString(s)
	case stringToString:
		return jsStringValue(s), nil
	}
	return undefined, nil
}

func split(s string, units []uint16, separator, limitArg jsValue) (jsValue, error) {
	limit := uint64(math.MaxUint32)
	if limitArg.kind != jsUndefined {
		limit = uint64(limitArg.toUint32())
	}
	if separator.kind == jsRegexKind {
		parts, err := separator.re.split(s, limit)
		return jsArrayValue(parts), err
	}
	if limit == 0 {
		return jsArrayValue(nil), nil
	}
	if separator.kind == jsUndefined {
		return jsArrayValue([]jsValue{jsStringValue(s)}), nil
	}
	sep := toUTF16(separator.toJSString())
	var parts []jsValue
	if len(sep) == 0 {
		for i := range units {
			parts = append(parts, jsStringValue(fromUTF16(units[i:i+1])))
		}
	} else {
		start := 0
		for i := findUnits(units, sep, start); i >= 0; i = findUnits(units, sep, start) {
			parts = append(parts, jsStringValue(fromUTF16(units[start:i])))
			start = i + len(sep)
		}
		parts = append(parts, jsStringValue(fromUTF16(units[start:])))
	}
	if uint64(len(parts)) > limit {
		parts = parts[:limit]
	}
	return jsArrayValue(parts), nil
}

func arrayMethod(m method, items []jsValue, args []jsValue) jsValue {
	length := len(items)
	switch m {
	case arrayIncludes:
		from := relativeIndex(arg(args, 1).toInteger(), length)
		return jsBoolValue(slices.ContainsFunc(items[from:], func(item jsValue) bool { return item.sameValueZero(arg(args, 0)) }))
	case arrayIndexOf:
		from := relativeIndex(arg(args, 1).toInteger(), length)
		found := slices.IndexFunc(items[from:], func(item jsValue) bool { return item.strictEq(arg(args, 0)) })
		if found < 0 {
			return jsNumberValue(-1)
		}
		return jsNumberValue(float64(found + from))
	case arrayJoin:
		separator := ","
		if sep := arg(args, 0); sep.kind != jsUndefined {
			separator = sep.toJSString()
		}
		return jsStringValue(joinValues(items, separator))
	case arrayToString:
		return jsStringValue(joinValues(items, ","))
	}
	return undefined
}

// numberToRadix is V8's DoubleToRadixCString, digit for digit.
func numberToRadix(value float64, radix int) string {
	if radix == 10 || math.IsInf(value, 0) || math.IsNaN(value) {
		return numberToString(value)
	}
	digitChar := func(d int) byte { return "0123456789abcdefghijklmnopqrstuvwxyz"[d] }
	nextDouble := func(x float64) float64 { return math.Float64frombits(math.Float64bits(x) + 1) }
	exponent := func(x float64) int {
		biased := int((math.Float64bits(x) >> 52) & 0x7FF)
		if biased == 0 {
			return -1074
		}
		return biased - 1075
	}
	r := float64(radix)
	negative := value < 0
	value = math.Abs(value)
	integer := math.Floor(value)
	fraction := value - integer
	delta := math.Max(0.5*(nextDouble(value)-value), nextDouble(0))
	var fractionDigits []int
	hasPoint := false
	if fraction >= delta {
		hasPoint = true
		for {
			fraction *= r
			delta *= r
			digit := int(fraction)
			fractionDigits = append(fractionDigits, digit)
			fraction -= float64(digit)
			if (fraction > 0.5 || fraction == 0.5 && digit&1 == 1) && fraction+delta > 1 {
				for {
					if len(fractionDigits) == 0 {
						integer++
						break
					}
					d := fractionDigits[len(fractionDigits)-1]
					fractionDigits = fractionDigits[:len(fractionDigits)-1]
					if d+1 < radix {
						fractionDigits = append(fractionDigits, d+1)
						break
					}
				}
				break
			}
			if fraction < delta {
				break
			}
		}
	}
	var integerDigits []byte
	for exponent(integer/r) > 0 {
		integer /= r
		integerDigits = append(integerDigits, '0')
	}
	for {
		remainder := math.Mod(integer, r)
		integerDigits = append(integerDigits, digitChar(int(remainder)))
		integer = (integer - remainder) / r
		if integer <= 0 {
			break
		}
	}
	var out strings.Builder
	if negative {
		out.WriteByte('-')
	}
	for i := len(integerDigits) - 1; i >= 0; i-- {
		out.WriteByte(integerDigits[i])
	}
	if hasPoint && len(fractionDigits) > 0 {
		out.WriteByte('.')
		for _, d := range fractionDigits {
			out.WriteByte(digitChar(d))
		}
	}
	return out.String()
}

// toFixed is Number.prototype.toFixed: the exact decimal value, ties away
// from zero.
func toFixed(x float64, digits int) string {
	if math.IsInf(x, 0) || math.IsNaN(x) || math.Abs(x) >= 1e21 {
		return numberToString(x)
	}
	exact := strconv.FormatFloat(math.Abs(x), 'f', 1100, 64)
	intPart, fracPart, _ := strings.Cut(exact, ".")
	kept := []byte(intPart + fracPart[:min(digits, len(fracPart))])
	if digits < len(fracPart) && fracPart[digits] >= '5' {
		i := len(kept)
		for {
			if i == 0 {
				kept = append([]byte{'1'}, kept...)
				break
			}
			i--
			if kept[i] == '9' {
				kept[i] = '0'
			} else {
				kept[i]++
				break
			}
		}
	}
	intLen := len(kept) - digits
	out := string(kept[:intLen])
	if digits > 0 {
		out += "." + string(kept[intLen:])
	}
	if x < 0 {
		return "-" + out
	}
	return out
}
