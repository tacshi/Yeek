package desktop

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
	"uuid"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

type multipartPartEditor struct {
	requestID, rowID, filename, contentType, value, error string
	file                                                  bool
}

func (a *App) formatRequestBody(d *Draft) {
	if a.deferUntilInputs(func() { a.formatRequestBody(d) }) {
		return
	}
	text, label := d.Body, "Request body"
	var formatted string
	var err error
	if d.BodyType == "graphql" {
		text, label = d.Query, "GraphQL query"
		formatted, err = engine.FormatGraphQL(text)
	} else {
		value := text
		if !b(o(d.Model, "body"), "sendJsonComments") {
			value = engine.FixJSONBody(value)
		}
		var parsed any
		if err = json.Unmarshal([]byte(value), &parsed); err == nil {
			data, marshalErr := json.Marshal(parsed, jsontext.WithIndent("  "))
			formatted, err = string(data), marshalErr
		}
	}
	if err != nil {
		a.errorMessage = err.Error()
		return
	}
	if formatted != text {
		a.editorDocument(label).state.Replace(0, utf8.RuneCountInString(text), formatted)
	}
}

func (a *App) openMultipartPart(rowID string) {
	if a.deferUntilInputs(func() { a.openMultipartPart(rowID) }) {
		return
	}
	d := a.drafts[a.active]
	if d == nil {
		return
	}
	for _, row := range d.Form {
		if row.ID == rowID {
			a.partEditor = &multipartPartEditor{requestID: d.ID, rowID: row.ID, filename: row.Filename, contentType: row.ContentType, value: row.Value, file: row.FileMode}
			a.prompt("multipart_part", "Multipart Field", "", "")
			return
		}
	}
}
func (a *App) saveMultipartPart() {
	if a.deferUntilInputs(a.saveMultipartPart) {
		return
	}
	state := a.partEditor
	if state == nil {
		return
	}
	contentType := strings.TrimSpace(state.contentType)
	if strings.Contains(contentType, "${[") || strings.Contains(contentType, "{{") {
		contentType = ""
	}
	if err := engine.ValidateMultipartMetadata(state.filename, contentType); err != nil {
		state.error = err.Error()
		return
	}
	d := a.drafts[state.requestID]
	if d == nil {
		state.error = "The request was closed. Reopen it to edit this field."
		return
	}
	for i := range d.Form {
		if d.Form[i].ID == state.rowID {
			if d.Form[i].FileMode != state.file {
				state.error = "The field type changed. Reopen its options."
				return
			}
			d.Form[i].Filename, d.Form[i].ContentType = state.filename, strings.TrimSpace(state.contentType)
			if !state.file {
				d.Form[i].Value = state.value
			}
			d.Dirty = true
			a.save(d)
			a.dialogOpen = false
			return
		}
	}
	state.error = "The field was removed."
}
func (a *App) multipartPartDialog(c *ui.Context, p colors) {
	state := a.partEditor
	if state == nil {
		return
	}
	ui.Column(c).Padding(20).Gap(14).Children(func() {
		if state.file {
			ui.Text(c, "Filename").FontSize(12).TextColor(p.muted)
			a.templateInput(c, p, &state.filename, state.rowID+":filename", "Multipart filename", "Use source filename", false).FillWidth()
		}
		ui.Text(c, "Content-Type").FontSize(12).TextColor(p.muted)
		a.templateInput(c, p, &state.contentType, state.rowID+":contentType", "Multipart Content-Type", "Automatic", false).FillWidth()
		if !state.file {
			ui.Text(c, "Value").FontSize(12).TextColor(p.muted)
			language := "text"
			if strings.Contains(state.contentType, "json") {
				language = "json"
			} else if strings.Contains(state.contentType, "xml") {
				language = "xml"
			}
			ui.Box(c).Height(180).Children(func() { a.nativeEditor(c, p, &state.value, "Multipart value", language, false) })
		}
		if state.error != "" {
			ui.Text(c, state.error).TextColor(p.red).MaxLines(3)
		}
		ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
			if ui.Button(c, "Cancel").Clicked() {
				a.dialogOpen = false
			}
			if ui.PrimaryButton(c, "Save Field").Clicked() || c.Shortcut(ui.Cmd, ui.KeyEnter) {
				a.saveMultipartPart()
			}
		})
	})
}
func (a *App) customMethodDialog(c *ui.Context, p colors) {
	ui.Column(c).Padding(20).Gap(14).Children(func() {
		input := ui.TextInput(c, &a.dialogValue).Label("Custom HTTP method").Placeholder("PROPFIND").Font("monospace").FillWidth().AutoFocus()
		if a.methodError != "" {
			ui.Text(c, a.methodError).TextColor(p.red).MaxLines(2)
		}
		ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
			if ui.Button(c, "Cancel").Clicked() {
				a.dialogOpen = false
			}
			if ui.PrimaryButton(c, "Save Method").Disabled(strings.TrimSpace(a.dialogValue) == "").Clicked() || input.Submitted() || c.Shortcut(ui.Cmd, ui.KeyEnter) {
				a.saveCustomMethod()
			}
		})
	})
}
func (a *App) saveCustomMethod() {
	if a.deferUntilInputs(a.saveCustomMethod) {
		return
	}
	method := strings.TrimSpace(a.dialogValue)
	if err := engine.ValidateHTTPMethod(method); err != nil {
		a.methodError = err.Error()
		return
	}
	if d := a.drafts[a.dialogID]; d != nil {
		d.Method = method
		d.Dirty = true
		a.save(d)
	}
	a.dialogOpen = false
}
func (a *App) changeBodyType(d *Draft, kind string) {
	if a.deferUntilInputs(func() { a.changeBodyType(d, kind) }) {
		return
	}
	body := engine.ConvertRequestBody(d.bodyObject(), d.BodyType, kind)
	if kind == "graphql" || kind == "multipart/form-data" || d.BodyType == "" && strings.EqualFold(d.Method, "GET") && kind != "" && kind != "binary" {
		d.Method = "POST"
	}
	d.Model["body"] = body
	d.Body, d.Query, d.Variables, d.FilePath = s(body, "text"), s(body, "query"), s(body, "variables"), s(body, "filePath")
	d.OperationName, d.OperationExplicit = s(body, "operationName"), body["operationName"] != nil
	d.Form = kvRows(body, "form")
	if kind != "binary" {
		d.Headers = slices.DeleteFunc(d.Headers, func(h KV) bool { return strings.EqualFold(h.Name, "Content-Type") })
		contentType := kind
		if kind == "graphql" {
			contentType = "application/json"
		}
		if contentType != "" {
			d.Headers = append(d.Headers, KV{ID: uuid.New().String(), Name: "Content-Type", Value: contentType, Enabled: true})
		}
	}
	d.BodyType, d.Dirty = kind, true
}
func (d *Draft) bodyObject() engine.Object {
	if d.BodyType == "" {
		return engine.Object{}
	}
	body := deepCopy(o(d.Model, "body"))
	keep := map[string]bool{}
	switch d.BodyType {
	case "graphql":
		if d.Query != "" || body["query"] != nil || body["text"] == nil {
			body["query"], body["variables"] = d.Query, d.Variables
			keep["query"], keep["variables"], keep["operationName"] = true, true, true
			if d.OperationExplicit || d.OperationName != "" {
				body["operationName"] = d.OperationName
			} else {
				delete(body, "operationName")
			}
		} else {
			keep["text"] = true
			body["text"] = d.Body
		}
	case "application/x-www-form-urlencoded", "multipart/form-data":
		form := rowObjects(d.Form)
		if len(form) > 0 || body["form"] != nil || body["text"] == nil {
			keep["form"] = true
			body["form"] = form
		} else {
			keep["text"] = true
			body["text"] = d.Body
		}
	case "binary":
		if d.FilePath != "" || body["filePath"] != nil || body["text"] == nil {
			keep["filePath"] = true
			body["filePath"] = d.FilePath
		} else {
			keep["text"] = true
			body["text"] = d.Body
		}
	default:
		keep["text"] = true
		body["text"] = d.Body
	}
	for _, key := range []string{"text", "query", "variables", "operationName", "form", "filePath"} {
		if !keep[key] {
			delete(body, key)
		}
	}
	return body
}
func (a *App) changeFormKind(rows *[]KV, id string, file bool, dirty *bool) {
	if a.deferUntilInputs(func() { a.changeFormKind(rows, id, file, dirty) }) {
		return
	}
	for i := range *rows {
		if (*rows)[i].ID == id {
			(*rows)[i].FileMode = file
			if !file {
				(*rows)[i].File, (*rows)[i].Filename = "", ""
			}
			*dirty = true
		}
	}
}
func (a *App) binaryBodyEditor(c *ui.Context, p colors, d *Draft) {
	ui.Column(c).Padding(14).Gap(12).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			if a.templateInput(c, p, &d.FilePath, d.ID+":bodyFile", "Request body file", "File path", false).Grow(1).MinWidth(0).Changed() {
				d.Dirty = true
			}
			if ui.Button(c, "Choose File…").Clicked() {
				a.chooseBodyFile(d)
			}
			if d.FilePath != "" && smallIconButton(c, "close", "Clear body file").Clicked() {
				a.deferUntilInputs(func() { d.FilePath = ""; d.Dirty = true })
			}
		})
		if d.FilePath == "" || strings.Contains(d.FilePath, "${[") || strings.Contains(d.FilePath, "{{") {
			return
		}
		contentType := engine.GuessContentType(d.FilePath)
		if a.binaryMIMEIgnored(d) || slices.ContainsFunc(d.Headers, func(h KV) bool {
			return h.Enabled && strings.EqualFold(h.Name, "Content-Type") && h.Value == contentType
		}) {
			return
		}
		ui.Column(c).Padding(12).Gap(10).Border(1, p.border).Radius(5).Children(func() {
			ui.Text(c, "Set Content-Type to "+contentType+"?").FontSize(12)
			ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
				if ui.Button(c, "Ignore").Clicked() {
					a.ignoreBinaryMIME(d)
				}
				if ui.PrimaryButton(c, "Set Header").Clicked() {
					a.setBinaryMIME(d)
				}
			})
		})
	})
}
func (a *App) binaryMIMEIgnored(d *Draft) bool {
	for _, m := range a.list("key_value") {
		if s(m, "namespace") == "no_sync" && s(m, "key") == "binary-mime:"+d.ID {
			var path string
			_ = json.Unmarshal([]byte(s(m, "value")), &path)
			return path == d.FilePath
		}
	}
	return false
}
func (a *App) ignoreBinaryMIME(d *Draft) {
	if a.deferUntilInputs(func() { a.ignoreBinaryMIME(d) }) {
		return
	}
	data, _ := json.Marshal(d.FilePath)
	model := engine.Object{"model": "key_value", "namespace": "no_sync", "key": "binary-mime:" + d.ID, "value": string(data)}
	a.run(func() (func(), error) {
		saved, err := a.Engine.Save(a.ctx, model)
		return func() { a.applyModel(saved) }, err
	})
}
func (a *App) setBinaryMIME(d *Draft) {
	if a.deferUntilInputs(func() { a.setBinaryMIME(d) }) {
		return
	}
	value := engine.GuessContentType(d.FilePath)
	first := true
	for i := range d.Headers {
		if strings.EqualFold(d.Headers[i].Name, "Content-Type") {
			d.Headers[i].Enabled = first
			if first {
				d.Headers[i].Value = value
				first = false
			}
		}
	}
	if first {
		d.Headers = append(d.Headers, KV{ID: uuid.New().String(), Name: "Content-Type", Value: value, Enabled: true})
	}
	d.Dirty = true
	a.save(d)
}
func (a *App) multipartValue(c *ui.Context, p colors, rows *[]KV, i int, dirty *bool) {
	row := &(*rows)[i]
	kind := "Text"
	if row.FileMode {
		kind = "File"
	}
	if ui.Select(c, &kind, []string{"Text", "File"}).Label(fmt.Sprintf("Field type %d", i+1)).Width(76).MinWidth(0).Changed() {
		a.changeFormKind(rows, row.ID, kind == "File", dirty)
	}
	value, label, placeholder := &row.Value, fmt.Sprintf("Name value %d", i+1), "Value"
	if row.FileMode {
		value, label, placeholder = &row.File, fmt.Sprintf("File path %d", i+1), "Choose a file"
	}
	if a.templateInput(c, p, value, row.ID+":value", label, placeholder, false).Grow(1).MinWidth(0).Height(32).Background(p.background).Border(1, p.border).Radius(4).Changed() {
		*dirty = true
	}
	if row.FileMode && smallIconButton(c, "folder", fmt.Sprintf("Choose file for field %d", i+1)).Clicked() {
		a.pickFormFile(rows, row.ID, dirty)
	} else if !row.FileMode {
		ui.Box(c).Width(28).Height(28)
	}
}
