// Package plugin defines the API for Go extensions loaded by Yeek.
package plugin

import "context"

type Context struct {
	Context     context.Context
	WorkspaceID string
	RequestID   string
	GetValue    func(string) (string, error)
	SetValue    func(string, string) error
}
type Field struct {
	Name, Label, Default string
	Secret               bool
	Options              []string
}
type Template struct {
	Name, Description string
	Fields            []Field
	Run               func(Context, map[string]string) (string, error)
}
type Request struct {
	Method, URL string
	Headers     map[string][]string
	Body        []byte
}
type Response struct {
	Status  int
	Headers map[string][]string
	Body    []byte
}
type Authentication struct {
	Name, Label string
	Fields      []Field
	Apply       func(Context, map[string]string, *Request) error
}
type Action struct {
	Name, Label string
	Run         func(Context) error
}
type Theme struct {
	Name   string
	Dark   bool
	Colors map[string]string
}
type Definition struct {
	Name, Version, Description string
	Templates                  []Template
	Authentication             []Authentication
	Actions                    []Action
	Themes                     []Theme
	Import                     func(Context, string) ([]map[string]any, error)
	Filter                     func(Context, string, string) (string, error)
	BeforeSend                 func(Context, *Request) error
	AfterReceive               func(Context, *Response) error
}
