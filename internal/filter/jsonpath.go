package filter

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/dlclark/regexp2/v2"
)

const (
	maxTraceDepth = 180
	maxSteps      = 20_000_000
)

var (
	sliceStep  = regexp.MustCompile(`^(-?[0-9]*):(-?[0-9]*):?([0-9]*)$`)
	nestedStep = regexp2.MustCompile(`@[^\n\r\u2028\u2029]?([^?]*)[\['](\??\([^\n\r\u2028\u2029]*?\))(?![^\n\r\u2028\u2029]\)\])[\]']`, regexp2.None)
)

func isLineTerminator(c rune) bool { return c == '\n' || c == '\r' || c == '\u2028' || c == '\u2029' }

func evaluateJSONPath(text, q string) (string, error) {
	root, err := parseJSON(text)
	if err != nil {
		return "", fmt.Errorf("Invalid JSON: %w", err) //nolint:staticcheck
	}
	if keys, ok := literalKeys(q); ok {
		v := fromDoc(root)
		for _, k := range keys {
			if v, ok = ownProperty(v, k); !ok {
				return stringifyResults(nil), nil
			}
		}
		return stringifyResults([]jsValue{v}), nil
	}
	values, err := newEngine(root).run(q, fromDoc(root))
	if err != nil {
		return "", fmt.Errorf("Invalid filter: %w", err) //nolint:staticcheck
	}
	return stringifyResults(values), nil
}

type pathElem struct {
	isNum bool
	s     string
	n     float64
}

func strElem(s string) pathElem { return pathElem{s: s} }

func (p pathElem) toJS() jsValue {
	if p.isNum {
		return jsNumberValue(p.n)
	}
	return jsStringValue(p.s)
}

func (p pathElem) text() string {
	if p.isNum {
		return numberToString(p.n)
	}
	return p.s
}

type pathNode struct {
	elem pathElem
	prev *pathNode
}

func pushPath(path *pathNode, elem pathElem) *pathNode { return &pathNode{elem: elem, prev: path} }

func pathString(path *pathNode) string {
	var elems []string
	for n := path; n != nil; n = n.prev {
		elems = append(elems, n.elem.text())
	}
	out := "$"
	for i := len(elems) - 2; i >= 0; i-- {
		elem := elems[i]
		if elem == "~" || elem == "^" || strings.HasPrefix(elem, "@") && strings.HasSuffix(elem, "()") {
			continue
		}
		if elem != "" && strings.Trim(elem, "0123456789*") == "" {
			out += "[" + elem + "]"
		} else {
			out += "['" + elem + "']"
		}
	}
	return out
}

type traceResult struct {
	path     *pathNode
	value    jsValue
	selector []loc
	selected bool
}

type traced struct {
	one  *traceResult
	many []*traceResult
}

func (t traced) found() bool { return t.one != nil || len(t.many) > 0 }

func addTraced(ret *[]*traceResult, t traced) {
	if t.one != nil {
		*ret = append(*ret, t.one)
	} else {
		*ret = append(*ret, t.many...)
	}
}

type filterPlan struct {
	nested bool
	key    string
	inner  loc
	script *compiledScript
}

type compiledScript struct {
	expr     *expr
	code     string
	usesPath bool
}

type regexKey struct {
	pattern string
	whole   bool
}

type engine struct {
	root              *jsonNode
	paths             map[string][]loc
	scripts           map[string]*compiledScript
	filters           map[string]*filterPlan
	rfcFilters        map[string]*logical
	regexes           map[regexKey]*jsRegex
	hasParentSelector bool
	depth, steps      int
}

func newEngine(root *jsonNode) *engine {
	return &engine{
		root:       root,
		paths:      map[string][]loc{},
		scripts:    map[string]*compiledScript{},
		filters:    map[string]*filterPlan{},
		rfcFilters: map[string]*logical{},
		regexes:    map[regexKey]*jsRegex{},
	}
}

func (e *engine) run(q string, start jsValue) ([]jsValue, error) {
	locs, err := e.path(q)
	if err != nil {
		return nil, err
	}
	if len(locs) > 1 && locs[0].kind == locStr && locs[0].s == "$" {
		locs = locs[1:]
	}
	t, err := e.trace(frame{expr: locs, val: start, path: pushPath(nil, strElem("$")), parent: null, parentProp: null})
	if err != nil {
		return nil, err
	}
	rets := t.many
	if t.one != nil {
		rets = []*traceResult{t.one}
	}
	values := []jsValue{}
	for _, r := range rets {
		if r != nil && !r.selected {
			values = append(values, r.value)
		}
	}
	return values, nil
}

func (e *engine) query(q query, current jsValue) ([]jsValue, error) {
	start := current
	if q.root {
		start = fromDoc(e.root)
	}
	return e.run(q.path, start)
}

func (e *engine) iRegexp(pattern string, whole bool) *jsRegex {
	key := regexKey{pattern, whole}
	re, ok := e.regexes[key]
	if !ok {
		re = iRegexp(pattern, whole)
		e.regexes[key] = re
	}
	return re
}

func (e *engine) path(q string) ([]loc, error) {
	if locs, ok := e.paths[q]; ok {
		return locs, nil
	}
	locs, err := parsePath(q)
	if err != nil {
		return nil, err
	}
	for _, l := range locs {
		if err := e.check(l); err != nil {
			return nil, err
		}
	}
	e.paths[q] = locs
	return locs, nil
}

func (e *engine) check(l loc) error {
	switch l.kind {
	case locStr, locPart:
		switch {
		case strings.HasPrefix(l.s, "?("):
			_, err := e.filter(l.s)
			return err
		case strings.HasPrefix(l.s, "("):
			_, err := e.script(l.s)
			return err
		case strings.Contains(l.s, ","):
			for _, part := range strings.Split(l.s, ",") {
				if err := e.check(loc{kind: locPart, s: part}); err != nil {
					return err
				}
			}
		}
	case locFilter:
		_, err := e.rfcFilter(l.s)
		return err
	}
	return nil
}

func (e *engine) script(code string) (*compiledScript, error) {
	if s, ok := e.scripts[code]; ok {
		return s, nil
	}
	x, err := parseScript(substituteScript(code))
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, code)
	}
	s := &compiledScript{expr: x, code: code, usesPath: strings.Contains(code, "@path")}
	e.scripts[code] = s
	return s, nil
}

func (e *engine) filter(l string) (*filterPlan, error) {
	if plan, ok := e.filters[l]; ok {
		return plan, nil
	}
	code := l
	if inner, ok := strings.CutPrefix(l, "?("); ok && !strings.ContainsFunc(l, isLineTerminator) {
		if inner, ok = strings.CutSuffix(inner, ")"); ok {
			code = inner
		}
	}
	m, err := nestedStep.FindStringMatch(code)
	if err != nil {
		return nil, err
	}
	var plan *filterPlan
	if m != nil {
		inner := loc{kind: locStr, s: m.GroupByNumber(2).String()}
		if err := e.check(inner); err != nil {
			return nil, err
		}
		plan = &filterPlan{nested: true, key: m.GroupByNumber(1).String(), inner: inner}
	} else {
		s, err := e.script(code)
		if err != nil {
			return nil, err
		}
		plan = &filterPlan{script: s}
	}
	e.filters[l] = plan
	return plan, nil
}

func (e *engine) rfcFilter(text string) (*logical, error) {
	if f, ok := e.rfcFilters[text]; ok {
		return f, nil
	}
	f, err := parseRFC(text)
	if err != nil {
		return nil, fmt.Errorf("%w: ?%s", err, text)
	}
	e.rfcFilters[text] = f
	return f, nil
}

func (e *engine) runScript(s *compiledScript, value, property jsValue, path func() string, parent, parentProp jsValue) (jsValue, error) {
	sc := &scope{value: value, property: property, parent: parent, parentProperty: parentProp}
	if s.usesPath {
		p := path()
		sc.path = &p
	}
	v, err := evalScript(s.expr, sc, e)
	if err != nil {
		return undefined, fmt.Errorf("%w: %s", err, s.code)
	}
	return v, nil
}

type frame struct {
	expr            []loc
	val             jsValue
	path            *pathNode
	parent          jsValue
	parentProp      jsValue
	literalPriority bool
}

func (f frame) rest() []loc {
	if len(f.expr) == 0 {
		return nil
	}
	return f.expr[1:]
}

func (f frame) own(key string) (jsValue, bool) {
	if !f.val.truthy() {
		return undefined, false
	}
	return ownProperty(f.val, key)
}

func (e *engine) trace(f frame) (traced, error) {
	e.depth++
	defer func() { e.depth-- }()
	if e.depth > maxTraceDepth {
		return traced{}, errors.New("the path goes too deep")
	}
	e.steps++
	if e.steps > maxSteps {
		return traced{}, errors.New("the path takes too many steps to evaluate")
	}
	return e.traceInner(f)
}

func (e *engine) child(f frame, child jsValue, elem pathElem, literalPriority bool, ret *[]*traceResult) error {
	next := frame{expr: f.rest(), val: child, path: pushPath(f.path, elem), parent: f.val, parentProp: elem.toJS(), literalPriority: literalPriority}
	t, err := e.trace(next)
	if err != nil {
		return err
	}
	addTraced(ret, t)
	return nil
}

func (e *engine) again(f frame, expr []loc, ret *[]*traceResult) error {
	f.expr, f.literalPriority = expr, false
	t, err := e.trace(f)
	if err != nil {
		return err
	}
	addTraced(ret, t)
	return nil
}

func withFirst(first loc, rest []loc) []loc { return append([]loc{first}, rest...) }

func (e *engine) traceInner(f frame) (traced, error) {
	if len(f.expr) == 0 {
		return traced{one: &traceResult{path: f.path, value: f.val}}, nil
	}
	l := f.expr[0]
	var ret []*traceResult
	switch l.kind {
	case locNames:
		childPriority := len(l.names) > 1 || !f.literalPriority
		for _, name := range l.names {
			if child, ok := f.own(name); ok {
				if err := e.child(f, child, strElem(name), childPriority, &ret); err != nil {
					return traced{}, err
				}
			}
		}
	case locFilter:
		filter, err := e.rfcFilter(l.s)
		if err != nil {
			return traced{}, err
		}
		for _, w := range walk(f.val) {
			ok, err := filter.test(w.value, e)
			if err != nil {
				return traced{}, err
			}
			if ok {
				if err := e.child(f, w.value, w.elem, false, &ret); err != nil {
					return traced{}, err
				}
			}
		}
	case locNum, locOther, locArrayKey:
		elem := strElem(locText(l))
		if l.kind == locNum {
			elem = pathElem{isNum: true, n: l.n}
		}
		child, ok := f.own(elem.text())
		switch {
		case ok:
			if err := e.child(f, child, elem, false, &ret); err != nil {
				return traced{}, err
			}
		case l.kind != locArrayKey:
			return traced{}, fmt.Errorf("%s is not a property name the path can follow", elem.text())
		}
	default:
		early, err := e.traceStr(f, l.s, l.kind == locStr, &ret)
		if err != nil {
			return traced{}, err
		}
		if early != nil {
			return *early, nil
		}
	}
	if e.hasParentSelector {
		var err error
		if ret, err = e.resolveParentSelectors(f, ret); err != nil {
			return traced{}, err
		}
	}
	return traced{many: ret}, nil
}

func (e *engine) resolveParentSelectors(f frame, ret []*traceResult) ([]*traceResult, error) {
	for t := 0; t < len(ret); t++ {
		r := ret[t]
		if r == nil || !r.selected {
			continue
		}
		next := f
		next.expr, next.path, next.literalPriority = r.selector, r.path, false
		result, err := e.trace(next)
		if err != nil {
			return nil, err
		}
		if result.one != nil {
			ret[t] = result.one
			continue
		}
		if len(result.many) == 0 {
			ret[t] = nil
			continue
		}
		ret[t] = result.many[0]
		rest := result.many[1:]
		ret = append(ret[:t+1], append(append([]*traceResult{}, rest...), ret[t+1:]...)...)
		t += len(rest)
	}
	return ret, nil
}

func (e *engine) traceStr(f frame, s string, rfcAdditions bool, ret *[]*traceResult) (*traced, error) {
	if child, ok := f.own(s); ok && f.literalPriority {
		return nil, e.child(f, child, strElem(s), false, ret)
	}
	switch {
	case s == "*":
		for _, w := range walk(f.val) {
			if err := e.child(f, w.value, w.elem, true, ret); err != nil {
				return nil, err
			}
		}
	case s == "..":
		if err := e.again(f, f.rest(), ret); err != nil {
			return nil, err
		}
		for _, w := range walk(f.val) {
			if w.value.kind != jsDoc && w.value.kind != jsNull {
				continue
			}
			next := f
			next.val, next.path, next.parent, next.parentProp, next.literalPriority = w.value, pushPath(f.path, w.elem), f.val, w.elem.toJS(), false
			t, err := e.trace(next)
			if err != nil {
				return nil, err
			}
			addTraced(ret, t)
		}
	case s == "^":
		e.hasParentSelector = true
		var parentPath *pathNode
		if f.path != nil {
			parentPath = f.path.prev
		}
		return &traced{one: &traceResult{path: parentPath, value: undefined, selector: f.rest(), selected: true}}, nil
	case s == "~":
		return &traced{one: &traceResult{path: pushPath(f.path, strElem("~")), value: f.parentProp}}, nil
	case s == "$":
		next := f
		next.expr, next.parent, next.parentProp, next.literalPriority = f.rest(), null, null, false
		t, err := e.trace(next)
		if err != nil {
			return nil, err
		}
		addTraced(ret, t)
	case sliceStep.MatchString(s):
		if f.val.isDocArray() {
			return nil, e.traceIndexes(f, sliceIndexes(s, len(f.val.doc.items)), ret)
		}
	case strings.HasPrefix(s, "?("):
		return nil, e.traceFilter(f, s, ret)
	case strings.HasPrefix(s, "("):
		script, err := e.script(s)
		if err != nil {
			return nil, err
		}
		property := undefined
		if f.path != nil {
			property = f.path.elem.toJS()
		}
		result, err := e.runScript(script, f.val, property, func() string { return pathString(f.path) }, f.parent, f.parentProp)
		if err != nil {
			return nil, err
		}
		var next loc
		switch {
		case result.kind == jsUndefined:
			next = loc{kind: locStr}
		case result.kind == jsString:
			next = loc{kind: locPart, s: result.s}
		case result.kind == jsNumber:
			next = loc{kind: locNum, n: result.n}
		case result.isDocArray() || result.kind == jsArrayKind:
			next = loc{kind: locArrayKey, s: result.toJSString()}
		default:
			next = loc{kind: locOther, s: result.toJSString()}
		}
		return nil, e.again(f, withFirst(next, f.rest()), ret)
	case strings.HasPrefix(s, "@"):
		t, err := valueTypeOf(s)
		if err != nil {
			return nil, err
		}
		if t.matches(f.val) {
			return &traced{one: &traceResult{path: f.path, value: f.val}}, nil
		}
	default:
		if key, ok := strings.CutPrefix(s, "`"); ok {
			if child, ok := f.own(key); ok {
				return nil, e.child(f, child, strElem(key), true, ret)
			}
		}
		if strings.Contains(s, ",") {
			for _, part := range strings.Split(s, ",") {
				if err := e.again(f, withFirst(loc{kind: locPart, s: part}, f.rest()), ret); err != nil {
					return nil, err
				}
			}
			return nil, nil
		}
		if child, ok := f.own(s); ok && !f.literalPriority {
			return nil, e.child(f, child, strElem(s), true, ret)
		}
		if rfcAdditions && f.val.isDocArray() {
			length := len(f.val.doc.items)
			if index, ok := negativeIndex(s, length); ok {
				return nil, e.traceIndexes(f, []float64{index}, ret)
			}
			if indexes, ok := reverseSliceIndexes(s, length); ok {
				return nil, e.traceIndexes(f, indexes, ret)
			}
		}
	}
	return nil, nil
}

func (e *engine) traceFilter(f frame, l string, ret *[]*traceResult) error {
	plan, err := e.filter(l)
	if err != nil {
		return err
	}
	for _, w := range walk(f.val) {
		if plan.nested {
			nested := w.value
			if plan.key != "" {
				if w.value.kind == jsNull {
					return fmt.Errorf("Cannot read properties of null (reading '%s')", plan.key) //nolint:staticcheck
				}
				nested, _ = ownProperty(w.value, plan.key)
			}
			next := f
			next.expr, next.val, next.literalPriority = []loc{plan.inner}, nested, false
			t, err := e.trace(next)
			if err != nil {
				return err
			}
			if t.found() {
				if err := e.child(f, w.value, w.elem, false, ret); err != nil {
					return err
				}
			}
			continue
		}
		at := func() string { return pathString(pushPath(f.path, w.elem)) }
		v, err := e.runScript(plan.script, w.value, w.elem.toJS(), at, f.parent, f.parentProp)
		if err != nil {
			return err
		}
		if v.truthy() {
			if err := e.child(f, w.value, w.elem, false, ret); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *engine) traceIndexes(f frame, indexes []float64, ret *[]*traceResult) error {
	for _, i := range indexes {
		if err := e.again(f, withFirst(loc{kind: locNum, n: i}, f.rest()), ret); err != nil {
			return err
		}
	}
	return nil
}

func locText(l loc) string {
	switch l.kind {
	case locNum:
		return numberToString(l.n)
	case locNames:
		return strings.Join(l.names, ",")
	}
	return l.s
}

type walked struct {
	elem  pathElem
	value jsValue
}

func walk(v jsValue) []walked {
	if v.kind != jsDoc {
		return nil
	}
	var out []walked
	if v.doc.kind == jsonArray {
		for i, item := range v.doc.items {
			out = append(out, walked{pathElem{isNum: true, n: float64(i)}, fromDoc(item)})
		}
		return out
	}
	for _, k := range v.doc.keys {
		out = append(out, walked{strElem(k), fromDoc(v.doc.values[k])})
	}
	return out
}

func sliceIndexes(l string, length int) []float64 {
	n := float64(length)
	parts := strings.Split(l, ":")
	orDefault := func(x, d float64) float64 {
		if x == 0 || math.IsNaN(x) {
			return d
		}
		return x
	}
	step, start, end := 1.0, 0.0, n
	if len(parts) > 2 && parts[2] != "" {
		step = orDefault(stringToNumber(parts[2]), 1)
	}
	if parts[0] != "" {
		start = orDefault(stringToNumber(parts[0]), 0)
	}
	if len(parts) > 1 && parts[1] != "" {
		end = stringToNumber(parts[1])
	}
	var indexes []float64
	if math.IsNaN(end) {
		return indexes
	}
	if start < 0 {
		start = math.Max(start+n, 0)
	} else {
		start = math.Min(start, n)
	}
	if end < 0 {
		end = math.Max(end+n, 0)
	} else {
		end = math.Min(end, n)
	}
	for i := start; i < end; i += step {
		indexes = append(indexes, i)
	}
	return indexes
}

func parseInt(s string) (int64, bool) {
	digits := strings.TrimPrefix(s, "-")
	if digits == "" || strings.Trim(digits, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		if strings.HasPrefix(s, "-") {
			return math.MinInt64, true
		}
		return math.MaxInt64, true
	}
	return n, true
}

func negativeIndex(l string, length int) (float64, bool) {
	n, ok := parseInt(l)
	if !ok || n >= 0 || strings.HasPrefix(l, "-0") {
		return 0, false
	}
	index := int64(length) + n
	return float64(index), index >= 0
}

func reverseSliceIndexes(l string, length int) ([]float64, bool) {
	parts := strings.Split(l, ":")
	if len(parts) != 3 {
		return nil, false
	}
	step, ok := parseInt(parts[2])
	if !ok || step >= 0 {
		return nil, false
	}
	bound := func(s string) (int64, bool, bool) {
		if s == "" {
			return 0, false, true
		}
		n, ok := parseInt(s)
		return n, true, ok
	}
	start, hasStart, ok1 := bound(parts[0])
	end, hasEnd, ok2 := bound(parts[1])
	if !ok1 || !ok2 {
		return nil, false
	}
	n := int64(length)
	normalize := func(i int64) int64 {
		if i >= 0 {
			return i
		}
		return n + i
	}
	clamp := func(i int64) int64 { return max(-1, min(i, n-1)) }
	if !hasStart {
		start = n - 1
	}
	upper := clamp(normalize(start))
	lower := int64(-1)
	if hasEnd {
		lower = clamp(normalize(end))
	}
	var indexes []float64
	for i := upper; lower < i; i += step {
		indexes = append(indexes, float64(i))
	}
	return indexes, true
}

type valueType uint8

const (
	typeScalar valueType = iota
	typeBoolean
	typeString
	typeUndefined
	typeFunction
	typeInteger
	typeNumber
	typeNonFinite
	typeObject
	typeArray
	typeNull
)

var valueTypeNames = []string{"scalar", "boolean", "string", "undefined", "function", "integer", "number", "nonFinite", "object", "array", "null"}

func valueTypeOf(l string) (valueType, error) {
	chars := []rune(l)
	name := ""
	if len(chars) > 3 {
		name = string(chars[1 : len(chars)-2])
	}
	for i, n := range valueTypeNames {
		if n == name {
			return valueType(i), nil // #nosec G115 -- one of eleven types.
		}
	}
	if name == "other" {
		return 0, errors.New("@other() needs an otherTypeCallback, which filters cannot supply")
	}
	return 0, fmt.Errorf("Unknown value type %s", name) //nolint:staticcheck
}

func (t valueType) matches(v jsValue) bool {
	finite := v.kind == jsNumber && !math.IsInf(v.n, 0) && !math.IsNaN(v.n)
	switch t {
	case typeScalar:
		return !v.truthy() || !v.isObject()
	case typeBoolean:
		return v.kind == jsBool
	case typeString:
		return v.kind == jsString
	case typeUndefined:
		return v.kind == jsUndefined
	case typeFunction:
		return v.kind == jsFunction
	case typeInteger:
		return finite && v.n == math.Trunc(v.n)
	case typeNumber:
		return finite
	case typeNonFinite:
		return v.kind == jsNumber && !finite
	case typeObject:
		return v.truthy() && v.typeOf() == "object"
	case typeArray:
		return v.isDocArray() || v.kind == jsArrayKind
	}
	return v.kind == jsNull
}
