package desktop

import (
	"runtime"
	"testing"

	"github.com/egoist/mygo/yeekui"
)

func TestAgentsSettingsShowTheCLISetup(t *testing.T) {
	a, _ := cookieApp(t)
	a.prompt("settings", "Settings", "", "")
	tt := ui.NewTester(a.View, 1360, 860)
	if err := tt.Click("Agents"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Enable MCP server") || tt.HasText("Add the MCP server") || tt.HasText("Codex") {
		t.Fatal("the setup shows only once agents are allowed", tt.Texts())
	}
	if err := tt.Click("MCP server enabled"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !b(a.settings, "mcpServerEnabled") {
		t.Fatal("the checkbox did not turn the server on")
	}
	cliSetup := "claude mcp add -s user yeek -- " + shellWord(executable()) + " mcp"
	for _, want := range []string{"Connect AI coding agents like Claude Code and Cursor to Yeek.", "Codex", "Add the MCP server", cliSetup} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %v", want, tt.Texts())
		}
	}
	for _, gone := range []string{"Token", "Through the app", "Run in a terminal."} {
		if tt.HasText(gone) {
			t.Fatalf("unexpected %q: %v", gone, tt.Texts())
		}
	}
	if runtime.GOOS == "darwin" && !tt.HasText("Install the "+commandName()+" command") && !tt.HasText("The "+commandName()+" command is installed at "+a.command.status.link+".") {
		t.Fatal(tt.Texts())
	}
	if err := tt.Click("Cursor"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.agentClient() != "cursor" || !tt.HasText("Add to ~/.cursor/mcp.json.") {
		t.Fatal(tt.Texts())
	}
}

func TestCLIAgentSetup(t *testing.T) {
	quoted := func(s string) string { return "'" + s + "'" }
	if runtime.GOOS == "windows" {
		quoted = func(s string) string { return `"` + s + `"` }
	}
	exe := "/Applications/My Yeek.app/Contents/MacOS/Yeek"
	args := []string{"--data-dir", "/tmp/yeek data", "mcp"}
	for client, want := range map[string]agentSetup{
		"claude_code": {"", `claude mcp add -s user yeek -- ` + quoted("/Applications/My Yeek.app/Contents/MacOS/Yeek") + ` --data-dir ` + quoted("/tmp/yeek data") + ` mcp`, true},
		"codex":       {"", `codex mcp add yeek -- yeek mcp`, true},
		"cursor":      {"Add to ~/.cursor/mcp.json.", "{\n  \"mcpServers\": {\n    \"yeek\": {\n      \"command\": \"yeek\",\n      \"args\": [\"mcp\"]\n    }\n  }\n}", false},
		"vscode":      {"Add to your user mcp.json (MCP: Open User Configuration).", "{\n  \"servers\": {\n    \"yeek\": {\n      \"type\": \"stdio\",\n      \"command\": \"yeek\",\n      \"args\": [\"mcp\"]\n    }\n  }\n}", false},
		"other":       {"For clients that read an mcpServers config, like Claude Desktop and Windsurf.", "{\n  \"mcpServers\": {\n    \"yeek\": {\n      \"command\": \"yeek\",\n      \"args\": [\"mcp\"]\n    }\n  }\n}", false},
	} {
		command, commandArgs := "yeek", []string{"mcp"}
		if client == "claude_code" {
			command, commandArgs = exe, args
		}
		if got := cliAgentSetup(client, command, commandArgs); got != want {
			t.Errorf("%s:\n%+v\nwant\n%+v", client, got, want)
		}
	}
}

func TestShellWord(t *testing.T) {
	posix := map[string]string{"yeek": "yeek", "/a b/c": "'/a b/c'", "it's": `'it'\''s'`}
	windows := map[string]string{"yeek": "yeek", `C:\Program Files\Yeek\Yeek.exe`: `"C:\Program Files\Yeek\Yeek.exe"`, `C:\Yeek\Yeek.exe`: `C:\Yeek\Yeek.exe`}
	cases := posix
	if runtime.GOOS == "windows" {
		cases = windows
	}
	for in, want := range cases {
		if got := shellWord(in); got != want {
			t.Errorf("shellWord(%q) = %q, want %q", in, got, want)
		}
	}
}
