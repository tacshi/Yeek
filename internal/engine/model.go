package engine

import (
	"encoding/json/v2"
	"fmt"
	"maps"
	"strings"
	"time"
	"uuid"
)

type Object = map[string]any

func str(m Object, k string) string { v, _ := m[k].(string); return v }
func obj(m Object, k string) Object {
	v, _ := m[k].(map[string]any)
	if v == nil {
		return Object{}
	}
	return v
}
func array(m Object, k string) []any {
	v, _ := m[k].([]any)
	if v == nil {
		return []any{}
	}
	return v
}
func boolean(m Object, k string) bool { v, _ := m[k].(bool); return v }
func number(m Object, k string) float64 {
	switch v := m[k].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0
}
func enabled(m Object) bool { v, ok := m["enabled"].(bool); return !ok || v }
func objects(v []any) []Object {
	out := make([]Object, 0, len(v))
	for _, x := range v {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}
func jsonString(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
func clone(m Object) Object { var v Object; _ = json.Unmarshal([]byte(jsonString(m)), &v); return v }
func now() string           { return time.Now().UTC().Format("2006-01-02T15:04:05.000000000") }
func newID(kind string) string {
	return prefixes[kind] + strings.ReplaceAll(uuid.New().String(), "-", "")
}

var prefixes = map[string]string{
	"oauth_token":            "ot_",
	"import_source_resource": "ir_",
	"workspace":              "wk_", "workspace_meta": "wm_", "environment": "ev_", "folder": "fl_", "http_request": "rq_", "http_response": "rs_", "http_response_event": "re_", "graphql_introspection": "gi_", "grpc_request": "gr_", "grpc_connection": "gc_", "grpc_event": "ge_", "websocket_request": "wr_", "websocket_connection": "wc_", "websocket_event": "we_", "cookie_jar": "cj_", "key_value": "kv_", "plugin": "pl_", "sync_state": "sy_", "import_source": "is_", "settings": "st_",
}

func defaultModel(kind string) (Object, error) {
	if _, ok := prefixes[kind]; !ok {
		return nil, fmt.Errorf("unknown model %q", kind)
	}
	m := Object{"model": kind, "id": "", "createdAt": now(), "updatedAt": now()}
	inherit := func() {
		m["workspaceId"] = ""
		m["folderId"] = nil
		m["name"] = ""
		m["description"] = ""
		m["sortPriority"] = 0
		m["authenticationType"] = nil
		m["authentication"] = Object{}
		m["headers"] = []any{}
		for _, k := range []string{"settingSendCookies", "settingStoreCookies", "settingValidateCertificates", "settingFollowRedirects"} {
			m[k] = Object{"enabled": false, "value": true}
		}
		m["settingRequestTimeout"] = Object{"enabled": false, "value": 0}
		m["settingRequestMessageSize"] = Object{"enabled": false, "value": 64 * 1024 * 1024}
		m["settingHttpVersion"] = Object{"enabled": false, "value": "auto"}
	}
	switch kind {
	case "settings":
		maps.Copy(m, Object{"id": "default", "appearance": "system", "clientCertificates": []any{}, "coloredMethods": false, "editorFont": nil, "editorFontSize": 12, "editorKeymap": "default", "editorSoftWrap": true, "hideWindowControls": false, "useNativeTitlebar": false, "interfaceFont": nil, "interfaceFontSize": 14, "interfaceScale": 1, "openWorkspaceNewWindow": nil, "proxy": nil, "themeDark": "yaak-dark", "themeLight": "yaak-light", "updateChannel": "stable", "hideLicenseBadge": true, "promptFeedback": false, "autoupdate": false, "autoDownloadUpdates": false, "checkNotifications": false, "hotkeys": Object{}})
	case "workspace":
		maps.Copy(m, Object{"name": "My Workspace", "description": "", "authentication": Object{}, "authenticationType": nil, "headers": []any{}, "encryptionKeyChallenge": nil, "settingValidateCertificates": true, "settingFollowRedirects": true, "settingRequestTimeout": 0, "settingRequestMessageSize": 64 * 1024 * 1024, "settingDnsOverrides": []any{}, "settingSendCookies": true, "settingStoreCookies": true, "settingHttpVersion": "auto"})
	case "folder":
		inherit()
		m["name"] = "New Folder"
	case "http_request":
		inherit()
		maps.Copy(m, Object{"method": "GET", "url": "", "body": Object{}, "bodyType": nil, "urlParameters": []any{}})
	case "grpc_request":
		inherit()
		maps.Copy(m, Object{"url": "", "metadata": []any{}, "message": "{}", "method": nil, "service": nil})
	case "websocket_request":
		inherit()
		maps.Copy(m, Object{"url": "", "message": "", "urlParameters": []any{}})
	case "environment":
		maps.Copy(m, Object{"workspaceId": "", "name": "New Environment", "public": true, "parentModel": "workspace", "parentId": nil, "variables": []any{}, "color": nil, "sortPriority": 0})
	case "cookie_jar":
		maps.Copy(m, Object{"workspaceId": "", "name": "Default Jar", "cookies": []any{}})
	case "workspace_meta":
		maps.Copy(m, Object{"workspaceId": "", "encryptionKey": nil, "settingSyncDir": nil})
	case "key_value":
		maps.Copy(m, Object{"namespace": "global", "key": "", "value": "null"})
	case "plugin":
		maps.Copy(m, Object{"directory": "", "enabled": true, "url": nil, "checkedAt": nil, "source": "filesystem"})
	case "http_response":
		maps.Copy(m, Object{"workspaceId": "", "requestId": "", "contentLength": nil, "contentLengthCompressed": nil, "elapsed": 0, "elapsedHeaders": 0, "elapsedDns": 0, "error": nil, "headers": []any{}, "remoteAddr": nil, "requestContentLength": nil, "requestHeaders": []any{}, "status": 0, "statusReason": nil, "state": "initialized", "url": "", "version": nil})
	case "http_response_event":
		maps.Copy(m, Object{"workspaceId": "", "responseId": "", "event": Object{}})
	case "websocket_connection":
		maps.Copy(m, Object{"workspaceId": "", "requestId": "", "elapsed": 0, "error": nil, "headers": []any{}, "state": "initialized", "status": 0, "url": ""})
	case "websocket_event":
		maps.Copy(m, Object{"workspaceId": "", "requestId": "", "connectionId": "", "isServer": false, "message": []any{}, "messageType": "text"})
	case "grpc_connection":
		maps.Copy(m, Object{"workspaceId": "", "requestId": "", "elapsed": 0, "error": nil, "method": "", "service": "", "status": 0, "state": "initialized", "trailers": Object{}, "url": ""})
	case "grpc_event":
		maps.Copy(m, Object{"workspaceId": "", "requestId": "", "connectionId": "", "content": "", "error": nil, "eventType": "info", "metadata": Object{}, "status": nil})
	case "graphql_introspection":
		maps.Copy(m, Object{"workspaceId": "", "requestId": "", "content": nil})
	case "import_source":
		maps.Copy(m, Object{"workspaceId": "", "importer": "", "origin": "", "originLabel": "", "lastImportedAt": now()})
	case "oauth_token":
		maps.Copy(m, Object{"workspaceId": "", "parentId": nil, "encrypted": ""})
	case "import_source_resource":
		maps.Copy(m, Object{"workspaceId": "", "importSourceId": "", "sourceKey": "", "modelType": "", "modelId": nil, "contentHash": nil})
	case "sync_state":
		maps.Copy(m, Object{"workspaceId": "", "flushedAt": now(), "modelId": "", "checksum": "", "relPath": "", "syncDir": ""})
	}
	return m, nil
}
