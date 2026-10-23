package engine

import (
	"bufio"
	"strings"

	"yeek/internal/filter"
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
	contentType := "application/json"
	if strings.HasPrefix(strings.TrimSpace(content), "<") {
		contentType = "application/xml"
	}
	return FilterBody(content, expression, contentType)
}

func FilterBody(content, expression, contentType string) (string, error) {
	if strings.TrimSpace(expression) == "" {
		return content, nil
	}
	return filter.Filter(filter.LanguageFor(contentType), content, expression)
}
