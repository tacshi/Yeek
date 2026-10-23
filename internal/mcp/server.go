package mcp

import (
	"context"
	jsonv1 "encoding/json"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"yeek/internal/engine"
)

// Tool inputs, mirroring the zod schemas of the @yaak/mcp-server plugin
// that Yaak's tools replace. Optional fields are pointers that omit when
// unset, so an input, marshaled, holds only what the caller gave, ready to
// lay over a model.

const (
	workspaceIDText = "Workspace ID (required if multiple workspaces are open)"
	bodyTypeText    = `Body type. Supported values: "binary", "graphql", "application/x-www-form-urlencoded", "multipart/form-data", or any text-based type (e.g., "application/json", "text/plain")`
	bodyText        = `Body content object. Structure varies by bodyType:
- "binary": { filePath: "/path/to/file" }
- "graphql": { query: "{ users { name } }", variables: "{\"id\": \"123\"}" }
- "application/x-www-form-urlencoded": { form: [{ name: "key", value: "val", enabled: true }] }
- "multipart/form-data": { form: [{ name: "field", value: "text", file: "/path/to/file", enabled: true }] }
- text-based (application/json, etc.): { text: "raw body content" }`
	authenticationTypeText = `Authentication type. Common values: "basic", "bearer", "oauth2", "apikey", "jwt", "awsv4", "oauth1", "ntlm", "none". Use null to inherit from parent.`
	authenticationText     = `Authentication configuration object. Structure varies by authenticationType:
- "basic": { username: "user", password: "pass" }
- "bearer": { token: "abc123", prefix: "Bearer" }
- "oauth2": { clientId: "...", clientSecret: "...", grantType: "authorization_code", authorizationUrl: "...", accessTokenUrl: "...", scope: "...", ... }
- "apikey": { location: "header" | "query", key: "X-API-Key", value: "..." }
- "jwt": { algorithm: "HS256", secret: "...", payload: "{ ... }" }
- "awsv4": { accessKeyId: "...", secretAccessKey: "...", service: "sts", region: "us-east-1", sessionToken: "..." }
- "none": {}`
)

// Pair is a header or URL parameter; enabled unless the caller says not.
type Pair struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	Enabled *bool  `json:"enabled,omitempty"`
}

type WorkspaceParams struct {
	WorkspaceID *string `json:"workspaceId,omitempty"`
}

type GetHTTPRequestParams struct {
	ID          string  `json:"id"`
	WorkspaceID *string `json:"workspaceId,omitempty"`
}

type SendHTTPRequestParams struct {
	ID            string  `json:"id"`
	EnvironmentID *string `json:"environmentId,omitempty"`
	WorkspaceID   *string `json:"workspaceId,omitempty"`
}

// requestFields are the fields of a request a caller may give.
type requestFields struct {
	Name               *string         `json:"name,omitempty"`
	Method             *string         `json:"method,omitempty"`
	FolderID           *string         `json:"folderId,omitempty"`
	Description        *string         `json:"description,omitempty"`
	Headers            *[]Pair         `json:"headers,omitempty"`
	URLParameters      *[]Pair         `json:"urlParameters,omitempty"`
	BodyType           *string         `json:"bodyType,omitempty"`
	Body               *map[string]any `json:"body,omitempty"`
	AuthenticationType *string         `json:"authenticationType,omitempty"`
	Authentication     *map[string]any `json:"authentication,omitempty"`
}

type CreateHTTPRequestParams struct {
	WorkspaceID *string `json:"workspaceId,omitempty"`
	URL         string  `json:"url"`
	requestFields
}

type UpdateHTTPRequestParams struct {
	ID          string  `json:"id"`
	WorkspaceID *string `json:"workspaceId,omitempty"`
	URL         *string `json:"url,omitempty"`
	requestFields
}

type DeleteParams struct {
	ID string `json:"id"`
}

type GetFolderParams struct {
	ID          string  `json:"id"`
	WorkspaceID *string `json:"workspaceId,omitempty"`
}

// folderFields are the fields of a folder a caller may give.
type folderFields struct {
	FolderID           *string         `json:"folderId,omitempty"`
	Description        *string         `json:"description,omitempty"`
	SortPriority       *float64        `json:"sortPriority,omitempty"`
	Headers            *[]Pair         `json:"headers,omitempty"`
	AuthenticationType *string         `json:"authenticationType,omitempty"`
	Authentication     *map[string]any `json:"authentication,omitempty"`
}

type CreateFolderParams struct {
	WorkspaceID *string `json:"workspaceId,omitempty"`
	Name        string  `json:"name"`
	folderFields
}

type UpdateFolderParams struct {
	ID          string  `json:"id"`
	WorkspaceID *string `json:"workspaceId,omitempty"`
	Name        *string `json:"name,omitempty"`
	folderFields
}

type ShowToastParams struct {
	Message string   `json:"message"`
	Icon    *string  `json:"icon,omitempty"`
	Color   *string  `json:"color,omitempty"`
	Timeout *float64 `json:"timeout,omitempty"`
}

var (
	toastIcons  = []string{"alert_triangle", "check", "check_circle", "chevron_down", "copy", "info", "pin", "search", "trash"}
	toastColors = []string{"primary", "secondary", "info", "success", "notice", "warning", "danger"}
)

// descriptions are the inputs' field descriptions, by field.
var descriptions = map[string]string{
	"workspaceId": workspaceIDText, "bodyType": bodyTypeText, "body": bodyText,
	"authenticationType": authenticationTypeText, "authentication": authenticationText,
	"folderId": "Parent folder ID", "description": "Request description", "headers": "Request headers",
	"urlParameters": "URL query parameters", "url": "Request URL", "method": "HTTP method",
	"name": "Request name", "message": "The message to display", "icon": "Icon name",
	"color": "Toast color", "timeout": "Timeout in milliseconds", "environmentId": "Environment ID for this send only; defaults to the active environment",
	"sortPriority": "Sort priority for ordering",
}

// schemaFor is T's input schema, with the descriptions given over the
// shared ones.
func schemaFor[T any](given map[string]string) *jsonschema.Schema {
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		panic(err)
	}
	for name, property := range schema.Properties {
		if text, ok := given[name]; ok {
			property.Description = text
		} else if text, ok := descriptions[name]; ok {
			property.Description = text
		}
	}
	switch any(*new(T)).(type) {
	case ShowToastParams:
		schema.Properties["icon"].Enum = anySlice(toastIcons)
		schema.Properties["color"].Enum = anySlice(toastColors)
	}
	return schema
}

func anySlice(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

// NewServer is Yeek's MCP server, its tools acting through host.
func NewServer(host Host, version string) *mcp.Server {
	s := &server{host: host}
	srv := mcp.NewServer(&mcp.Implementation{Name: "yeek-mcp-server", Version: version}, &mcp.ServerOptions{Logger: slog.New(slog.DiscardHandler)})
	tool := func(name, title, description string, schema *jsonschema.Schema) *mcp.Tool {
		return &mcp.Tool{Name: name, Title: title, Description: description, InputSchema: schema}
	}
	none := &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{}}
	mcp.AddTool(srv, tool("list_workspaces", "List Workspaces", "List all open workspaces in Yeek", none), s.listWorkspaces)
	mcp.AddTool(srv, tool("get_workspace_id", "Get Workspace ID", "Get the current workspace ID", none), s.getWorkspaceID)
	mcp.AddTool(srv, tool("get_environment_id", "Get Environment ID", "Get the current environment ID", none), s.getEnvironmentID)
	mcp.AddTool(srv, tool("list_http_requests", "List HTTP Requests", "List all HTTP requests in a workspace", schemaFor[WorkspaceParams](nil)), s.listHTTPRequests)
	mcp.AddTool(srv, tool("get_http_request", "Get HTTP Request", "Get details of a specific HTTP request by ID", schemaFor[GetHTTPRequestParams](map[string]string{"id": "The HTTP request ID"})), s.getHTTPRequest)
	mcp.AddTool(srv, tool("send_http_request", "Send HTTP Request", "Send an HTTP request and get the response", schemaFor[SendHTTPRequestParams](map[string]string{"id": "The HTTP request ID to send"})), s.sendHTTPRequest)
	mcp.AddTool(srv, tool("create_http_request", "Create HTTP Request", "Create a new HTTP request", schemaFor[CreateHTTPRequestParams](map[string]string{"name": "Request name (empty string to auto-generate from URL)", "method": "HTTP method (defaults to GET)"})), s.createHTTPRequest)
	mcp.AddTool(srv, tool("update_http_request", "Update HTTP Request", "Update an existing HTTP request", schemaFor[UpdateHTTPRequestParams](map[string]string{"id": "HTTP request ID to update"})), s.updateHTTPRequest)
	mcp.AddTool(srv, tool("delete_http_request", "Delete HTTP Request", "Delete an HTTP request by ID", schemaFor[DeleteParams](map[string]string{"id": "HTTP request ID to delete"})), s.deleteHTTPRequest)
	mcp.AddTool(srv, tool("list_folders", "List Folders", "List all folders in a workspace", schemaFor[WorkspaceParams](nil)), s.listFolders)
	mcp.AddTool(srv, tool("get_folder", "Get Folder", "Get details of a specific folder by ID", schemaFor[GetFolderParams](map[string]string{"id": "The folder ID"})), s.getFolder)
	folderText := map[string]string{"name": "Folder name", "folderId": "Parent folder ID (for nested folders)", "description": "Folder description", "headers": "Default headers to apply to requests in this folder"}
	mcp.AddTool(srv, tool("create_folder", "Create Folder", "Create a new folder in a workspace", schemaFor[CreateFolderParams](folderText)), s.createFolder)
	updateFolderText := maps.Clone(folderText)
	updateFolderText["id"] = "Folder ID to update"
	mcp.AddTool(srv, tool("update_folder", "Update Folder", "Update an existing folder", schemaFor[UpdateFolderParams](updateFolderText)), s.updateFolder)
	mcp.AddTool(srv, tool("delete_folder", "Delete Folder", "Delete a folder by ID", schemaFor[DeleteParams](map[string]string{"id": "Folder ID to delete"})), s.deleteFolder)
	mcp.AddTool(srv, tool("show_toast", "Show Toast", "Show a toast notification in Yeek", schemaFor[ShowToastParams](nil)), s.showToast)
	return srv
}

type server struct{ host Host }

type result = *mcp.CallToolResult

func text(s string) (result, any, error) {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}, nil, nil
}

// toJSON is serde_json's to_string_pretty of the value.
func toJSON(v any) (result, any, error) {
	data, err := json.Marshal(v, jsontext.WithIndent("  "), json.Deterministic(true), json.FormatNilMapAsNull(true))
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to serialize result: %w", err) //nolint:staticcheck // Yaak's message, shown as is
	}
	return text(string(data))
}

func (s *server) resolveWorkspace(id *string) (Workspace, error) {
	workspaces, err := s.host.ListWorkspaces()
	if err != nil {
		return Workspace{}, err
	}
	if id == nil {
		if len(workspaces) > 1 {
			var list []string
			for i, w := range workspaces {
				list = append(list, strconv.Itoa(i+1)+". "+w.Name+" (ID: "+w.ID+")")
			}
			return Workspace{}, errors.New("Multiple workspaces are open. Please specify which workspace to use.\n\n" + //nolint:staticcheck // Yaak's message, shown as is
				"Currently open workspaces:\n" + strings.Join(list, "\n") + "\n\n" +
				"You can use the list_workspaces tool to get workspace IDs, then use other tools " +
				"with the workspace context. For example, ask the user which workspace they want " +
				"to work with by presenting them with the numbered list above.")
		}
		if len(workspaces) == 0 {
			return Workspace{}, errors.New("No workspace is open") //nolint:staticcheck // Yaak's message, shown as is
		}
		return workspaces[0], nil
	}
	if i := slices.IndexFunc(workspaces, func(w Workspace) bool { return w.ID == *id }); i >= 0 {
		return workspaces[i], nil
	}
	var list []string
	for _, w := range workspaces {
		list = append(list, "- "+w.Name+" (ID: "+w.ID+")")
	}
	return Workspace{}, fmt.Errorf("Workspace with ID %q not found.\n\nAvailable workspaces:\n%s", *id, strings.Join(list, "\n")) //nolint:staticcheck // Yaak's message, shown as is
}

func (s *server) findFolder(workspace, id string) (engine.Object, error) {
	folders, err := s.host.ListFolders(workspace)
	if err != nil {
		return nil, err
	}
	for _, f := range folders {
		if f["id"] == id {
			return f, nil
		}
	}
	return nil, nil
}

// givenFields are the fields of an input the caller gave, by their model
// names, without the ones that pick what the tool acts on. It marshals with
// encoding/json v1, whose omitempty leaves out only unset pointers, so an
// empty list given (headers: []) still clears one.
func givenFields(input any) engine.Object {
	data, err := jsonv1.Marshal(input)
	if err != nil {
		return engine.Object{}
	}
	var fields engine.Object
	if json.Unmarshal(data, &fields) != nil {
		return engine.Object{}
	}
	delete(fields, "id")
	delete(fields, "workspaceId")
	for _, key := range []string{"headers", "urlParameters"} {
		pairs, _ := fields[key].([]any)
		for _, p := range pairs {
			if pair, ok := p.(map[string]any); ok && pair["enabled"] == nil {
				pair["enabled"] = true
			}
		}
	}
	return fields
}

// merge lays the given fields over existing, keeping its id.
func merge(existing, given engine.Object) engine.Object {
	out := maps.Clone(existing)
	maps.Copy(out, given)
	out["id"] = existing["id"]
	return out
}

func (s *server) listWorkspaces(context.Context, *mcp.CallToolRequest, struct{}) (result, any, error) {
	workspaces, err := s.host.ListWorkspaces()
	if err != nil {
		return nil, nil, err
	}
	if workspaces == nil {
		workspaces = []Workspace{}
	}
	return toJSON(workspaces)
}

func (s *server) getWorkspaceID(context.Context, *mcp.CallToolRequest, struct{}) (result, any, error) {
	w, err := s.resolveWorkspace(nil)
	if err != nil {
		return nil, nil, err
	}
	return text(w.ID)
}

func (s *server) getEnvironmentID(context.Context, *mcp.CallToolRequest, struct{}) (result, any, error) {
	w, err := s.resolveWorkspace(nil)
	if err != nil {
		return nil, nil, err
	}
	id, err := s.host.ActiveEnvironmentID(w.ID)
	if err != nil {
		return nil, nil, err
	}
	if id == "" {
		id = "No environment selected"
	}
	return text(id)
}

func (s *server) listHTTPRequests(_ context.Context, _ *mcp.CallToolRequest, p WorkspaceParams) (result, any, error) {
	w, err := s.resolveWorkspace(p.WorkspaceID)
	if err != nil {
		return nil, nil, err
	}
	requests, err := s.host.ListHTTPRequests(w.ID)
	if err != nil {
		return nil, nil, err
	}
	return toJSON(orEmpty(requests))
}

func (s *server) getHTTPRequest(_ context.Context, _ *mcp.CallToolRequest, p GetHTTPRequestParams) (result, any, error) {
	if _, err := s.resolveWorkspace(p.WorkspaceID); err != nil {
		return nil, nil, err
	}
	request, err := s.host.GetHTTPRequest(p.ID)
	if err != nil {
		return nil, nil, err
	}
	return toJSON(request)
}

func (s *server) sendHTTPRequest(ctx context.Context, _ *mcp.CallToolRequest, p SendHTTPRequestParams) (result, any, error) {
	w, err := s.resolveWorkspace(p.WorkspaceID)
	if err != nil {
		return nil, nil, err
	}
	request, err := s.host.GetHTTPRequest(p.ID)
	if err != nil {
		return nil, nil, err
	}
	if request == nil {
		return nil, nil, fmt.Errorf("HTTP request with ID %s not found", p.ID)
	}
	environment := ""
	if p.EnvironmentID != nil {
		environment = *p.EnvironmentID
	}
	response, err := s.host.SendHTTPRequest(ctx, w.ID, request, environment)
	if err != nil {
		return nil, nil, err
	}
	// Plugins never saw where the host keeps the body, so neither do MCP clients.
	response = maps.Clone(response)
	delete(response, "bodyPath")
	return toJSON(response)
}

func (s *server) createHTTPRequest(_ context.Context, _ *mcp.CallToolRequest, p CreateHTTPRequestParams) (result, any, error) {
	w, err := s.resolveWorkspace(p.WorkspaceID)
	if err != nil {
		return nil, nil, err
	}
	fields := engine.Object{"model": "http_request", "name": "", "method": "GET", "workspaceId": w.ID}
	maps.Copy(fields, givenFields(p))
	fields["id"] = ""
	request, err := s.host.UpsertHTTPRequest(fields)
	if err != nil {
		return nil, nil, err
	}
	return toJSON(request)
}

func (s *server) updateHTTPRequest(_ context.Context, _ *mcp.CallToolRequest, p UpdateHTTPRequestParams) (result, any, error) {
	if _, err := s.resolveWorkspace(p.WorkspaceID); err != nil {
		return nil, nil, err
	}
	existing, err := s.host.GetHTTPRequest(p.ID)
	if err != nil {
		return nil, nil, err
	}
	if existing == nil {
		return nil, nil, fmt.Errorf("HTTP request with ID %s not found", p.ID)
	}
	request, err := s.host.UpsertHTTPRequest(merge(existing, givenFields(p)))
	if err != nil {
		return nil, nil, err
	}
	return toJSON(request)
}

func (s *server) deleteHTTPRequest(_ context.Context, _ *mcp.CallToolRequest, p DeleteParams) (result, any, error) {
	request, err := s.host.DeleteHTTPRequest(p.ID)
	if err != nil {
		return nil, nil, err
	}
	return text(fmt.Sprintf("Deleted: %v (%v)", request["name"], request["id"]))
}

func (s *server) listFolders(_ context.Context, _ *mcp.CallToolRequest, p WorkspaceParams) (result, any, error) {
	w, err := s.resolveWorkspace(p.WorkspaceID)
	if err != nil {
		return nil, nil, err
	}
	folders, err := s.host.ListFolders(w.ID)
	if err != nil {
		return nil, nil, err
	}
	return toJSON(orEmpty(folders))
}

func (s *server) getFolder(_ context.Context, _ *mcp.CallToolRequest, p GetFolderParams) (result, any, error) {
	w, err := s.resolveWorkspace(p.WorkspaceID)
	if err != nil {
		return nil, nil, err
	}
	folder, err := s.findFolder(w.ID, p.ID)
	if err != nil {
		return nil, nil, err
	}
	return toJSON(folder)
}

func (s *server) createFolder(_ context.Context, _ *mcp.CallToolRequest, p CreateFolderParams) (result, any, error) {
	w, err := s.resolveWorkspace(p.WorkspaceID)
	if err != nil {
		return nil, nil, err
	}
	fields := givenFields(p)
	fields["model"], fields["workspaceId"], fields["id"] = "folder", w.ID, ""
	folder, err := s.host.UpsertFolder(fields)
	if err != nil {
		return nil, nil, err
	}
	return toJSON(folder)
}

func (s *server) updateFolder(_ context.Context, _ *mcp.CallToolRequest, p UpdateFolderParams) (result, any, error) {
	w, err := s.resolveWorkspace(p.WorkspaceID)
	if err != nil {
		return nil, nil, err
	}
	existing, err := s.findFolder(w.ID, p.ID)
	if err != nil {
		return nil, nil, err
	}
	if existing == nil {
		return nil, nil, fmt.Errorf("Folder with ID %s not found", p.ID) //nolint:staticcheck // Yaak's message, shown as is
	}
	folder, err := s.host.UpsertFolder(merge(existing, givenFields(p)))
	if err != nil {
		return nil, nil, err
	}
	return toJSON(folder)
}

func (s *server) deleteFolder(_ context.Context, _ *mcp.CallToolRequest, p DeleteParams) (result, any, error) {
	folder, err := s.host.DeleteFolder(p.ID)
	if err != nil {
		return nil, nil, err
	}
	return text(fmt.Sprintf("Deleted: %v (%v)", folder["name"], folder["id"]))
}

func (s *server) showToast(_ context.Context, _ *mcp.CallToolRequest, p ShowToastParams) (result, any, error) {
	toast := Toast{Message: p.Message, Timeout: 5 * time.Second}
	if p.Icon != nil {
		toast.Icon = *p.Icon
	}
	if p.Color != nil {
		toast.Color = *p.Color
	}
	if p.Timeout != nil {
		toast.Timeout = time.Duration(*p.Timeout) * time.Millisecond
	}
	if err := s.host.ShowToast(toast); err != nil {
		return nil, nil, err
	}
	return text("✓ Toast shown: \"" + p.Message + "\"")
}

func orEmpty(models []engine.Object) []engine.Object {
	if models == nil {
		return []engine.Object{}
	}
	return models
}
