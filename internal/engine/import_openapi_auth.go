package engine

import (
	"cmp"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"
)

func (s *openAPIImport) authentication(value any) openAPIAuth {
	result := openAPIAuth{values: Object{}}
	requirements, declared := value.([]any)
	if !declared {
		return result
	}
	result.kind = "none"
	if len(requirements) == 0 {
		return result
	}
	for _, requirement := range objects(requirements) {
		if len(requirement) == 0 {
			return result
		}
		variables, authVariables := maps.Clone(s.variables), maps.Clone(s.authVariables)
		if auth, ok := s.securityRequirement(requirement); ok {
			return auth
		}
		s.variables, s.authVariables = variables, authVariables
	}
	warning := "A security requirement could not be mapped. Configure authentication for those operations before sending."
	if !slices.Contains(s.warnings, warning) {
		s.warnings = append(s.warnings, warning)
	}
	return result
}
func (s *openAPIImport) securityRequirement(requirement Object) (openAPIAuth, bool) {
	schemes := maps.Clone(obj(obj(s.root, "components"), "securitySchemes"))
	maps.Copy(schemes, obj(s.root, "securityDefinitions"))
	result := openAPIAuth{values: Object{}}
	primary := false
	for _, name := range slices.Sorted(maps.Keys(requirement)) {
		scheme := importRef(s.root, obj(schemes, name))
		kind, protocol := str(scheme, "type"), strings.ToLower(str(scheme, "scheme"))
		if kind == "apiKey" {
			key, location := cmp.Or(str(scheme, "name"), name), str(scheme, "in")
			if !slices.Contains([]string{"query", "header", "cookie"}, location) {
				return result, false
			}
			value := s.authVariable(name, "key")
			if location == "cookie" {
				value = key + "=" + value
				key, location = "Cookie", "header"
			}
			if len(requirement) == 1 {
				result.kind, result.values = "apikey", Object{"key": key, "value": value, "location": location}
				primary = true
			} else {
				row := Object{"name": key, "value": value, "enabled": true}
				if location == "query" {
					result.parameters = append(result.parameters, row)
				} else {
					result.headers = append(result.headers, row)
				}
			}
			continue
		}
		if kind == "mutualTLS" {
			warning := "Configure a client certificate for security scheme “" + name + "”."
			if !slices.Contains(s.warnings, warning) {
				s.warnings = append(s.warnings, warning)
			}
			continue
		}
		if primary {
			return result, false
		}
		switch {
		case kind == "basic" || kind == "http" && protocol == "basic":
			result.kind = "basic"
			result.values = Object{"username": s.authVariable(name, "username"), "password": s.authVariable(name, "password")}
		case kind == "http" && protocol == "bearer" || kind == "openIdConnect":
			result.kind = "bearer"
			result.values = Object{"prefix": "Bearer", "token": s.authVariable(name, "token")}
		case kind == "oauth2":
			values := s.oauthScheme(name, scheme, requirement[name])
			if values == nil {
				return result, false
			}
			result.kind, result.values = "oauth2", values
		default:
			return result, false
		}
		primary = true
	}
	if !primary {
		result.kind = "none"
	}
	return result, true
}
func openAPISlug(name string) string {
	var result strings.Builder
	var previous rune
	for _, r := range name {
		if r >= 'A' && r <= 'Z' {
			if previous >= 'a' && previous <= 'z' || previous >= '0' && previous <= '9' {
				result.WriteByte('_')
			}
			result.WriteRune(r + ('a' - 'A'))
		} else if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			result.WriteRune(r)
		} else if previous != '_' {
			result.WriteByte('_')
		}
		previous = r
	}
	return cmp.Or(strings.Trim(result.String(), "_"), "security")
}
func (s *openAPIImport) authVariable(scheme, field string) string {
	identity := scheme + "\n" + field
	name := s.authVariables[identity]
	if name == "" {
		base := "auth_" + openAPISlug(scheme) + "_" + field
		name = base
		for suffix := 2; ; suffix++ {
			if _, exists := s.variables[name]; !exists {
				break
			}
			name = fmt.Sprintf("%s_%d", base, suffix)
		}
		s.authVariables[identity] = name
		s.variables[name] = ""
	}
	return "${[ " + name + " ]}"
}
func (s *openAPIImport) oauthScheme(name string, scheme Object, scopes any) Object {
	type flow struct {
		grant string
		data  Object
	}
	flows := obj(scheme, "flows")
	candidates := []flow{{"authorization_code", obj(flows, "authorizationCode")}, {"client_credentials", obj(flows, "clientCredentials")}, {"password", obj(flows, "password")}, {"implicit", obj(flows, "implicit")}}
	if kind := str(scheme, "flow"); kind != "" {
		grant := map[string]string{"accessCode": "authorization_code", "application": "client_credentials", "password": "password", "implicit": "implicit"}[kind]
		if grant == "" {
			return nil
		}
		candidates = append([]flow{{grant, scheme}}, candidates...)
	}
	for _, candidate := range candidates {
		authorization := s.oauthURL(str(candidate.data, "authorizationUrl"))
		token := s.oauthURL(str(candidate.data, "tokenUrl"))
		if authorization == "" && token == "" {
			continue
		}
		values := Object{"grantType": candidate.grant, "clientId": s.authVariable(name, "client_id"), "headerPrefix": "Bearer"}
		if candidate.grant != "implicit" {
			values["clientSecret"] = s.authVariable(name, "client_secret")
			values["accessTokenUrl"] = token
		}
		if candidate.grant == "authorization_code" || candidate.grant == "implicit" {
			values["authorizationUrl"] = authorization
			values["redirectUri"] = s.authVariable(name, "redirect_uri")
		}
		if candidate.grant == "password" {
			values["username"], values["password"] = "", ""
		}
		if scopes, ok := scopes.([]any); ok {
			text := []string{}
			for _, scope := range scopes {
				if value, ok := scope.(string); ok {
					text = append(text, value)
				}
			}
			if len(text) > 0 {
				values["scope"] = strings.Join(text, " ")
			}
		}
		return values
	}
	return nil
}
func (s *openAPIImport) oauthURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.IsAbs() || strings.HasPrefix(raw, "//") {
		return raw
	}
	if len(s.servers) > 1 {
		if strings.HasPrefix(raw, "/") {
			return "${[ baseUrlOrigin ]}" + raw
		}
		return "${[ baseUrl ]}/" + raw
	}
	base, err := url.Parse(s.baseURL + "/")
	if err == nil && base.IsAbs() {
		return base.ResolveReference(u).String()
	}
	if !strings.HasPrefix(raw, "/") {
		raw = strings.TrimRight(s.baseURL, "/") + "/" + raw
	}
	return "${[ baseUrlOrigin ]}" + raw
}
