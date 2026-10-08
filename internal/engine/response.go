package engine

import (
	"bufio"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"strings"

	"github.com/antchfx/xmlquery"
	"github.com/ohler55/ojg/jp"
)

type SSEEvent struct {
	ID, Event, Data string
	Retry           string
}

func ParseSSE(text string) []SSEEvent {
	result := []SSEEvent{}
	var event SSEEvent
	data := []string{}
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 4096), 8<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if len(data) > 0 {
				event.Data = strings.Join(data, "\n")
				if event.Event == "" {
					event.Event = "message"
				}
				result = append(result, event)
			}
			event = SSEEvent{ID: event.ID}
			data = nil
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		key, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch key {
		case "data":
			data = append(data, value)
		case "id":
			if !strings.ContainsRune(value, 0) {
				event.ID = value
			}
		case "event":
			event.Event = value
		case "retry":
			event.Retry = value
		}
	}
	return result
}
func FilterResponse(content, expression string) (string, error) {
	if strings.TrimSpace(expression) == "" {
		return content, nil
	}
	if strings.HasPrefix(strings.TrimSpace(content), "<") {
		doc, err := xmlquery.Parse(strings.NewReader(content))
		if err != nil {
			return "", err
		}
		nodes, err := xmlquery.QueryAll(doc, expression)
		if err != nil {
			return "", err
		}
		out := []string{}
		for _, node := range nodes {
			out = append(out, node.OutputXML(true))
		}
		return strings.Join(out, "\n"), nil
	}
	var value any
	if err := json.Unmarshal([]byte(content), &value); err != nil {
		return "", fmt.Errorf("response is not valid JSON or XML: %w", err)
	}
	path, err := jp.ParseString(expression)
	if err != nil {
		return "", err
	}
	values := path.Get(value)
	out, err := json.Marshal(values, jsontext.WithIndent("  "))
	return string(out), err
}
