package engine

import (
	"cmp"
	"crypto/hmac"
	"crypto/md5" // #nosec G501 -- implements user-selected compatibility hashing.
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- implements user-selected compatibility hashing.
	"crypto/sha256"
	"crypto/sha3"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"hash"
	"math"
	"math/big"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/antchfx/xmlquery"
	"github.com/ohler55/ojg/jp"
)

func extendedTemplateFunction(name string, args map[string]string) (string, error) {
	input := cmp.Or(args["input"], args["value"], args["text"])
	if strings.HasPrefix(name, "hash.") || strings.HasPrefix(name, "hmac.") {
		_, algorithm, _ := strings.Cut(name, ".")
		factories := map[string]func() hash.Hash{"md5": md5.New, "sha1": sha1.New, "sha224": sha256.New224, "sha256": sha256.New, "sha384": sha512.New384, "sha512": sha512.New, "sha3-256": func() hash.Hash { return sha3.New256() }, "sha3-512": func() hash.Hash { return sha3.New512() }}
		factory := factories[algorithm]
		if factory == nil {
			return "", fmt.Errorf("unknown hash algorithm %q", algorithm)
		}
		h := factory()
		if strings.HasPrefix(name, "hmac.") {
			h = hmac.New(factory, []byte(args["key"]))
		}
		_, _ = h.Write([]byte(input))
		data := h.Sum(nil)
		switch args["encoding"] {
		case "base64":
			return base64.StdEncoding.EncodeToString(data), nil
		case "base64url":
			return base64.RawURLEncoding.EncodeToString(data), nil
		default:
			return hex.EncodeToString(data), nil
		}
	}
	switch name {
	case "base64.encode":
		if args["encoding"] == "base64url" {
			return base64.RawURLEncoding.EncodeToString([]byte(input)), nil
		}
		return base64.StdEncoding.EncodeToString([]byte(input)), nil
	case "base64.decode":
		data, err := base64.StdEncoding.DecodeString(input)
		if err != nil {
			data, err = base64.RawURLEncoding.DecodeString(input)
		}
		return string(data), err
	case "url.encode":
		return strings.ReplaceAll(url.QueryEscape(input), "+", "%20"), nil
	case "url.decode":
		return url.PathUnescape(input)
	case "json.escape":
		data, err := json.Marshal(input)
		if err != nil {
			return "", err
		}
		return string(data[1 : len(data)-1]), nil
	case "json.minify":
		var value any
		if err := json.Unmarshal([]byte(input), &value); err != nil {
			return "", err
		}
		data, err := json.Marshal(value, json.Deterministic(true))
		return string(data), err
	case "json.jsonpath", "xml.xpath":
		return templatePath(input, cmp.Or(args["query"], args["path"]), args)
	case "regex.match", "regex.replace":
		pattern := args["regex"]
		flags := ""
		for _, flag := range []string{"i", "m", "s"} {
			if strings.Contains(args["flags"], flag) {
				flags += flag
			}
		}
		if flags != "" {
			pattern = "(?" + flags + ")" + pattern
		}
		expression, err := regexp.Compile(pattern)
		if err != nil {
			return "", err
		}
		if name == "regex.match" {
			matches := expression.FindStringSubmatch(input)
			if len(matches) > 1 {
				return matches[1], nil
			}
			if len(matches) == 1 {
				return matches[0], nil
			}
			return "", nil
		}
		replacement := strings.ReplaceAll(args["replacement"], "$&", "$0")
		if strings.Contains(args["flags"], "g") {
			return expression.ReplaceAllString(input, replacement), nil
		}
		indices := expression.FindStringSubmatchIndex(input)
		if indices == nil {
			return input, nil
		}
		expanded := expression.ExpandString(nil, replacement, input, indices)
		return input[:indices[0]] + string(expanded) + input[indices[1]:], nil
	case "random.range":
		minValue, err := strconv.ParseFloat(cmp.Or(args["min"], "0"), 64)
		if err != nil {
			return "", err
		}
		maxValue, err := strconv.ParseFloat(cmp.Or(args["max"], "1"), 64)
		if err != nil {
			return "", err
		}
		if maxValue < minValue {
			return "", errors.New("random maximum must be at least the minimum")
		}
		random, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 53))
		if err != nil {
			return "", err
		}
		value := minValue + float64(random.Int64())/float64(1<<53)*(maxValue-minValue)
		if args["decimals"] != "" {
			decimals, err := strconv.Atoi(args["decimals"])
			if err != nil {
				return "", err
			}
			factor := math.Pow10(max(0, min(15, decimals)))
			value = math.Round(value*factor) / factor
		}
		return strconv.FormatFloat(value, 'f', -1, 64), nil
	case "timestamp.unix", "timestamp.unixMillis", "timestamp.iso8601", "timestamp.format", "timestamp.offset":
		date := time.Now()
		if raw := args["date"]; raw != "" {
			var err error
			date, err = time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				date, err = time.Parse("2006-01-02", raw)
				if err != nil {
					return "", err
				}
			}
		}
		switch name {
		case "timestamp.unix":
			return strconv.FormatInt(date.Unix(), 10), nil
		case "timestamp.unixMillis":
			return strconv.FormatInt(date.UnixMilli(), 10), nil
		case "timestamp.format":
			return date.Format(dateLayout(args["format"])), nil
		case "timestamp.offset":
			ops := regexp.MustCompile(`([+-])\s*(\d+)\s*([yMdhms])`)
			for _, op := range ops.FindAllStringSubmatch(args["expression"], -1) {
				amount, _ := strconv.Atoi(op[2])
				if op[1] == "-" {
					amount = -amount
				}
				switch op[3] {
				case "y":
					date = date.AddDate(amount, 0, 0)
				case "M":
					date = date.AddDate(0, amount, 0)
				case "d":
					date = date.AddDate(0, 0, amount)
				case "h":
					date = date.Add(time.Duration(amount) * time.Hour)
				case "m":
					date = date.Add(time.Duration(amount) * time.Minute)
				case "s":
					date = date.Add(time.Duration(amount) * time.Second)
				}
			}
		}
		return date.UTC().Format("2006-01-02T15:04:05.000Z"), nil
	default:
		return "", fmt.Errorf("unknown template function %q", name)
	}
}
func templatePath(input, path string, args map[string]string) (string, error) {
	values := []any{}
	if strings.HasPrefix(strings.TrimSpace(input), "<") {
		doc, err := xmlquery.Parse(strings.NewReader(input))
		if err != nil {
			return "", err
		}
		nodes, err := xmlquery.QueryAll(doc, path)
		if err != nil {
			return "", err
		}
		for _, node := range nodes {
			values = append(values, node.InnerText())
		}
	} else {
		var data any
		if err := json.Unmarshal([]byte(input), &data); err != nil {
			return "", err
		}
		expression, err := jp.ParseString(path)
		if err != nil {
			return "", err
		}
		values = expression.Get(data)
	}
	asString := func(v any) string {
		if s, ok := v.(string); ok {
			return s
		}
		data, _ := json.Marshal(v, json.Deterministic(true))
		return string(data)
	}
	switch args["result"] {
	case "all":
		data, err := json.Marshal(values, json.Deterministic(true))
		if args["formatted"] == "true" {
			data, err = json.Marshal(values, json.Deterministic(true), jsontext.WithIndent("  "))
		}
		return string(data), err
	case "join":
		items := make([]string, len(values))
		for i, v := range values {
			items[i] = asString(v)
		}
		return strings.Join(items, args["join"]), nil
	default:
		if len(values) == 0 {
			return "", nil
		}
		return asString(values[0]), nil
	}
}
func dateLayout(format string) string {
	replacer := strings.NewReplacer("yyyy", "2006", "yy", "06", "MMMM", "January", "MMM", "Jan", "MM", "01", "dd", "02", "HH", "15", "hh", "03", "mm", "04", "ss", "05", "SSS", "000", "XXX", "Z07:00", "XX", "Z0700")
	return replacer.Replace(strings.ReplaceAll(format, "'", ""))
}
