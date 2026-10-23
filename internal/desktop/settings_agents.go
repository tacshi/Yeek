package desktop

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/mcp"
)

// agentClients are Yaak's AGENT_CLIENTS, the agents Connect an agent sets up.
var agentClients = []struct{ label, value string }{
	{"Claude Code", "claude_code"}, {"Codex", "codex"}, {"Cursor", "cursor"}, {"VS Code", "vscode"}, {"Other", "other"},
}

// agentSetup is where an agent's setup goes, as a sentence, and the
// snippet to put there.
type agentSetup struct{ where, snippet string }

// appAgentSetup is Yaak's appSetup: an agent's setup for the app's MCP
// server at url, which needs token as a bearer token.
func appAgentSetup(client, url, token string) agentSetup {
	authorization := "Bearer " + token
	switch client {
	case "claude_code":
		return agentSetup{"Run in a terminal.", fmt.Sprintf(`claude mcp add --transport http yeek %s --header "Authorization: %s"`, url, authorization)}
	case "codex":
		quoted := func(s string) string { data, _ := json.Marshal(s); return string(data) }
		return agentSetup{"Add to ~/.codex/config.toml.", strings.Join([]string{
			"[mcp_servers.yeek]",
			"url = " + quoted(url),
			`http_headers = { "Authorization" = ` + quoted(authorization) + " }",
		}, "\n")}
	case "cursor":
		return agentSetup{"Add to ~/.cursor/mcp.json.", setupJSON(map[string]any{"mcpServers": map[string]any{"yeek": map[string]any{"url": url, "headers": map[string]any{"Authorization": authorization}}}})}
	case "vscode":
		return agentSetup{"Add to your user mcp.json (MCP: Open User Configuration).", setupJSON(map[string]any{"servers": map[string]any{"yeek": orderedSetup{{"type", "http"}, {"url", url}, {"headers", map[string]any{"Authorization": authorization}}}}})}
	}
	return agentSetup{"For clients that read an mcpServers config, through the mcp-remote bridge.", setupJSON(map[string]any{"mcpServers": map[string]any{"yeek": orderedSetup{
		{"command", "npx"},
		{"args", []string{"-y", "mcp-remote", url, "--header", "Authorization: " + authorization}},
	}}})}
}

// orderedSetup is a JSON object that keeps its members in the order given,
// as the snippets JavaScript writes do.
type orderedSetup []struct {
	key   string
	value any
}

func (o orderedSetup) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteByte('{')
	for i, member := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		key, _ := json.Marshal(member.key)
		value, err := json.Marshal(member.value)
		if err != nil {
			return nil, err
		}
		b.Write(key)
		b.WriteByte(':')
		b.Write(value)
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}

var plainArray = regexp.MustCompile(`\[\n\s*([^\[\]{}]*?)\n\s*\]`)

// setupJSON is Yaak's json(): pretty JSON with arrays of plain values kept
// on one line.
func setupJSON(value any) string {
	data, _ := json.MarshalIndent(value, "", "  ")
	return plainArray.ReplaceAllStringFunc(string(data), func(m string) string {
		items := plainArray.FindStringSubmatch(m)[1]
		return "[" + strings.Join(regexp.MustCompile(`,\n\s*`).Split(items, -1), ", ") + "]"
	})
}

// agentClient is the agent last picked in Connect an agent, Yaak's
// agents_setup_client key value.
func (a *App) agentClient() string {
	for _, kv := range a.list("key_value") {
		if s(kv, "namespace") == "global" && s(kv, "key") == "agents_setup_client" {
			var client string
			_ = json.Unmarshal([]byte(s(kv, "value")), &client)
			for _, c := range agentClients {
				if c.value == client {
					return client
				}
			}
		}
	}
	return "claude_code"
}

func (a *App) setAgentClient(client string) {
	value, _ := json.Marshal(client)
	model := map[string]any{"model": "key_value", "namespace": "global", "key": "agents_setup_client", "value": string(value)}
	for _, kv := range a.list("key_value") {
		if s(kv, "namespace") == "global" && s(kv, "key") == "agents_setup_client" {
			model["id"] = kv["id"]
		}
	}
	a.saveModel(model)
}

// describedSection is Yaak's SettingsSection with a description under its
// title.
func describedSection(c *ui.Context, p colors, title, description string, rows func()) {
	ui.Column(c).Key(title).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Gap(4).Padding(0, 0, 8, 0).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
			ui.Text(c, title).FontSize(13).TextColor(p.muted)
			ui.Text(c, description).FontSize(12).TextColor(p.subtle).MaxLines(3)
		})
		rows()
	})
}

// settingsAgents is Yaak's Agents settings. Its Yaak CLI section and the
// setup through the CLI are left out: Yeek has no CLI.
func (a *App) settingsAgents(c *ui.Context, p colors) {
	settingsHeading(c, p, "Agents", "Connect AI coding agents like Claude Code and Cursor to Yeek, so they can read, edit, and send your requests.")
	url := "http://" + mcp.DefaultAddr + mcp.Path
	enabled := b(a.settings, "mcpServerEnabled")
	token := mcpToken(a.list("key_value"))
	describedSection(c, p, "MCP server in the app", "For agents that connect over HTTP, or to act on the workspaces open in Yeek.", func() {
		settingRow(c, p, "Enable MCP server", "Serve Yeek's tools at "+url+" while Yeek is open.", nil, func() {
			on := enabled
			if ui.Checkbox(c, &on, "").Label("MCP server enabled").Changed() {
				a.saveSetting("mcpServerEnabled", on)
			}
		})
		if enabled && token != "" {
			settingRow(c, p, "Token", "Agents send this as a bearer token.", nil, func() { settingValue(c, p, token, nil) })
		}
	})
	describedSection(c, p, "Connect an agent", "Pick your agent or editor to see how to add Yeek's MCP tools to it.", func() {
		client := a.agentClient()
		ui.Row(c).FillWidth().Padding(14, 0).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
			selected := 0
			labels := make([]string, len(agentClients))
			for i, option := range agentClients {
				labels[i] = option.label
				if option.value == client {
					selected = i
				}
			}
			if ui.Segmented(c, &selected, labels...).Label("Agent").Changed() {
				a.setAgentClient(agentClients[selected].value)
			}
			ui.Spacer(c)
		})
		if !enabled || token == "" {
			settingRow(c, p, "Through the app", "Turn on the MCP server above to connect this way.", nil, func() {})
			return
		}
		setup := appAgentSetup(client, url, token)
		ui.Column(c).Key("Through the app").FillWidth().Padding(14, 0).Gap(4).Children(func() {
			ui.Text(c, "Through the app").FontSize(13)
			ui.Text(c, setup.where).FontSize(12).TextColor(p.muted)
			ui.Row(c).FillWidth().Margin(8, 0, 0, 0).Padding(8, 8, 8, 10).Gap(8).Radius(6).Border(1, p.border).AlignItems(ui.Start).Children(func() {
				ui.Text(c, setup.snippet).Font("monospace").FontSize(12).Wrap().Selectable().Grow(1).MinWidth(0)
				if smallIconButton(c, "copy", "Copy setup").Border(1, p.border).Clicked() {
					c.WriteClipboard(setup.snippet)
				}
			})
		})
	})
}
