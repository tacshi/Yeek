package engine

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func IsFileFormField(field Object) bool {
	if str(field, "type") == "file" || boolean(field, "isFile") || str(field, "file") != "" {
		return true
	}
	_, file := field["file"].(string)
	return file && field["value"] == nil
}
func GuessContentType(path string) string {
	if contentType := mime.TypeByExtension(filepath.Ext(path)); contentType != "" {
		return contentType
	}
	return "application/octet-stream"
}
func ValidateHTTPMethod(method string) error {
	if method == "" {
		return errors.New("enter an HTTP method")
	}
	if _, err := http.NewRequest(method, "http://localhost", nil); err != nil {
		return errors.New("HTTP method must be a token without spaces or control characters")
	}
	return nil
}
func ValidateMultipartMetadata(filename, contentType string) error {
	if strings.ContainsAny(filename, "\r\n\x00") {
		return errors.New("filename cannot contain a line break or NUL")
	}
	if strings.ContainsAny(contentType, "\r\n\x00") {
		return errors.New("Content-Type cannot contain a line break or NUL")
	}
	if contentType != "" {
		if _, _, err := mime.ParseMediaType(contentType); err != nil {
			return errors.New("enter a valid Content-Type, such as application/json")
		}
	}
	return nil
}
func requestBody(m Object) ([]byte, string, error) {
	var buffer bytes.Buffer
	contentType, err := writeRequestBody(context.Background(), &buffer, m)
	return buffer.Bytes(), contentType, err
}
func openRequestFile(path string) (*os.File, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("choose a file before sending")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("open request file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("choose a regular file for the request body")
	}
	file, err := os.Open(path) // #nosec G304 -- the desktop user explicitly configures request upload paths.
	if err != nil {
		return nil, err
	}
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("request file is no longer a regular file")
	}
	return file, nil
}

type requestContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r requestContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
func writeRequestBody(ctx context.Context, out io.Writer, m Object) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	body, kind := obj(m, "body"), str(m, "bodyType")
	switch kind {
	case "":
		return "", nil
	case "application/x-www-form-urlencoded":
		values := url.Values{}
		for _, p := range objects(array(body, "form")) {
			if enabled(p) && str(p, "name") != "" {
				values.Add(str(p, "name"), str(p, "value"))
			}
		}
		_, err := io.WriteString(out, values.Encode())
		return kind, err
	case "multipart/form-data":
		return writeMultipart(ctx, out, m)
	case "binary":
		file, err := openRequestFile(str(body, "filePath"))
		if err != nil {
			return "", err
		}
		defer func() { _ = file.Close() }()
		_, err = io.Copy(out, requestContextReader{ctx, file})
		return "application/octet-stream", err
	case "graphql":
		if strings.EqualFold(str(m, "method"), "GET") {
			return "", nil
		}
		payload := Object{"query": str(body, "query")}
		if variables := str(body, "variables"); strings.TrimSpace(variables) != "" {
			var value any
			if err := json.Unmarshal([]byte(StripJSONComments(variables)), &value); err != nil {
				return "", fmt.Errorf("GraphQL variables: %w", err)
			}
			payload["variables"] = value
		}
		if op := str(body, "operationName"); op != "" {
			payload["operationName"] = op
		}
		return "application/json", json.MarshalWrite(out, payload)
	default:
		text := str(body, "text")
		if kind == "application/json" && !boolean(body, "sendJsonComments") {
			text = FixJSONBody(text)
		}
		_, err := io.WriteString(out, text)
		return kind, err
	}
}
func writeMultipart(ctx context.Context, out io.Writer, m Object) (string, error) {
	writer := multipart.NewWriter(out)
	for _, h := range objects(array(m, "headers")) {
		if !enabled(h) || !strings.EqualFold(str(h, "name"), "Content-Type") {
			continue
		}
		media, params, err := mime.ParseMediaType(str(h, "value"))
		if err != nil {
			return "", fmt.Errorf("multipart Content-Type: %w", err)
		}
		if media == "multipart/form-data" && params["boundary"] != "" {
			if err = writer.SetBoundary(params["boundary"]); err != nil {
				return "", fmt.Errorf("multipart boundary: %w", err)
			}
		}
		break
	}
	for _, field := range objects(array(obj(m, "body"), "form")) {
		if !enabled(field) || str(field, "name") == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		name, contentType, filename := str(field, "name"), str(field, "contentType"), str(field, "filename")
		if strings.ContainsAny(name, "\r\n\x00") {
			return "", errors.New("multipart field name cannot contain a line break or NUL")
		}
		if err := ValidateMultipartMetadata(filename, contentType); err != nil {
			return "", fmt.Errorf("%s: %w", name, err)
		}
		if IsFileFormField(field) {
			file, err := openRequestFile(str(field, "file"))
			if err != nil {
				return "", fmt.Errorf("%s: %w", name, err)
			}
			if filename == "" {
				filename = filepath.Base(str(field, "file"))
			}
			if contentType == "" {
				contentType = GuessContentType(str(field, "file"))
			}
			if err = ValidateMultipartMetadata(filename, contentType); err != nil {
				_ = file.Close()
				return "", fmt.Errorf("%s: %w", name, err)
			}
			part, err := writer.CreatePart(multipartPartHeaders(name, filename, contentType, true))
			if err == nil {
				_, err = io.Copy(part, requestContextReader{ctx, file})
			}
			_ = file.Close()
			if err != nil {
				return "", err
			}
		} else {
			part, err := writer.CreatePart(multipartPartHeaders(name, "", contentType, false))
			if err != nil {
				return "", err
			}
			if _, err = io.WriteString(part, str(field, "value")); err != nil {
				return "", err
			}
		}
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	return writer.FormDataContentType(), nil
}
func multipartPartHeaders(name, filename, contentType string, file bool) textproto.MIMEHeader {
	escape := strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace
	disposition := "form-data; name=\"" + escape(name) + "\""
	if file {
		disposition += "; filename=\"" + escape(filename) + "\""
	}
	headers := textproto.MIMEHeader{"Content-Disposition": {disposition}}
	if contentType != "" {
		headers.Set("Content-Type", contentType)
	}
	return headers
}

type preparedBody struct {
	body        io.ReadCloser
	reopen      func() (io.ReadCloser, error)
	size        int64
	contentType string
}

func (e *Engine) prepareRequestBody(ctx context.Context, responseID string, model Object) (preparedBody, error) {
	// A snapshot keeps redirects, authentication retries, and history on the same
	// bytes even if another program changes an upload file while it is sending.
	contentType := ""
	err := e.writeRequestSnapshot(ctx, responseID, func(writer io.Writer) error {
		var err error
		contentType, err = writeRequestBody(ctx, writer, model)
		return err
	})
	if err != nil {
		return preparedBody{}, err
	}
	result, err := e.openRequestSnapshot(responseID)
	result.contentType = contentType
	return result, err
}
func (e *Engine) writeRequestSnapshot(ctx context.Context, responseID string, write func(io.Writer) error) error {
	name := responseID + ".request.tmp"
	file, err := e.bodies.OpenFile(name, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = e.bodies.Remove(name) }()
	err = write(file)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return e.bodies.Rename(name, responseID+".request")
}
func (e *Engine) openRequestSnapshot(responseID string) (preparedBody, error) {
	name := responseID + ".request"
	info, err := e.bodies.Stat(name)
	if err != nil {
		return preparedBody{}, err
	}
	result := preparedBody{size: info.Size()}
	result.reopen = func() (io.ReadCloser, error) {
		if result.size == 0 {
			return http.NoBody, nil
		}
		return e.bodies.Open(name)
	}
	result.body, err = result.reopen()
	return result, err
}
func (e *Engine) snapshotModifiedRequest(ctx context.Context, responseID string, request *http.Request) error {
	var reader io.ReadCloser
	var err error
	if request.GetBody != nil {
		reader, err = request.GetBody()
	} else {
		reader = request.Body
	}
	if err != nil {
		return err
	}
	if reader == nil {
		reader = http.NoBody
	}
	defer func() { _ = reader.Close() }()
	if err = e.writeRequestSnapshot(ctx, responseID, func(writer io.Writer) error { _, err := io.Copy(writer, requestContextReader{ctx, reader}); return err }); err != nil {
		return err
	}
	if request.Body != nil {
		_ = request.Body.Close()
	}
	saved, err := e.openRequestSnapshot(responseID)
	if err != nil {
		return err
	}
	request.Body, request.GetBody, request.ContentLength = saved.body, saved.reopen, saved.size
	return nil
}
