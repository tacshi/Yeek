//go:build !darwin

package desktop

import "errors"

func commandState(string, string) commandStatus { return commandStatus{} }

func installCommand(string, string) error { return errors.ErrUnsupported }

func removeCommand(string, string) error { return errors.ErrUnsupported }
