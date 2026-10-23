package engine

import (
	"strconv"

	"yeek/locales"
)

type localizedError struct{ message func() string }

func (e *localizedError) Error() string { return e.message() }

type unknownFunctionError struct{ name string }

func (e unknownFunctionError) Error() string {
	return locales.UnknownTemplateFunctionName(strconv.Quote(e.name))
}
