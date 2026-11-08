package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"yeek/internal/engine"
	"yeek/internal/mcp"
)

type Options struct {
	Name    string
	Dir     string
	Version string
	Secrets func(dataDir string) engine.SecretStore
	In      io.Reader
	Out     io.Writer
	Err     io.Writer
}

func CommandName(app string) string {
	return strings.ToLower(strings.Join(strings.Fields(app), "-"))
}

func Invoked(args []string, name string) bool {
	if len(args) == 0 {
		return false
	}
	if base := filepath.Base(args[0]); base == name {
		exe, err := os.Executable()
		if err == nil {
			exe, err = filepath.EvalSymlinks(exe)
		}
		if err == nil && filepath.Base(exe) != base {
			return true
		}
	}
	for i := 1; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "-data-dir" || arg == "--data-dir":
			i++
		case strings.HasPrefix(arg, "-data-dir=") || strings.HasPrefix(arg, "--data-dir="):
		default:
			return slices.Contains([]string{"mcp", "help", "version", "-h", "-help", "--help", "-v", "--version"}, arg)
		}
	}
	return false
}

func Main(args []string, o Options) int {
	if err := run(args, o); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		_, _ = fmt.Fprintf(o.Err, "%s: %v\n", o.Name, err)
		return 1
	}
	return 0
}

func run(args []string, o Options) error {
	global := flag.NewFlagSet(o.Name, flag.ContinueOnError)
	global.SetOutput(o.Err)
	global.Usage = func() { usage(o.Out, o.Name) }
	dataDir := global.String("data-dir", "", "")
	var version bool
	global.BoolVar(&version, "v", false, "")
	global.BoolVar(&version, "version", false, "")
	if err := global.Parse(args); err != nil {
		return err
	}
	if *dataDir != "" {
		dir, err := filepath.Abs(*dataDir)
		if err != nil {
			return err
		}
		o.Dir = dir
	}
	args = global.Args()
	switch {
	case version:
		_, _ = fmt.Fprintf(o.Out, "%s %s\n", o.Name, o.Version)
		return nil
	case len(args) == 0:
		usage(o.Out, o.Name)
		return nil
	}
	switch args[0] {
	case "help":
		usage(o.Out, o.Name)
		return nil
	case "version":
		_, _ = fmt.Fprintf(o.Out, "%s %s\n", o.Name, o.Version)
		return nil
	case "mcp":
		return serveMCP(args[1:], o)
	}
	usage(o.Err, o.Name)
	return fmt.Errorf("unknown command %q", args[0])
}

func usage(w io.Writer, name string) {
	_, _ = fmt.Fprintf(w, `Usage: %[1]s <command>

Commands:
  mcp        Serve Yeek's MCP tools over stdio, for AI coding agents

Flags:
  --data-dir DIR  Yeek's data directory (default: the app's)
  -h, --help      Show this help
  -v, --version   Show the version

Run "%[1]s mcp --help" for the flags of mcp.
`, name)
}

func serveMCP(args []string, o Options) error {
	flags := flag.NewFlagSet(o.Name+" mcp", flag.ContinueOnError)
	flags.SetOutput(o.Err)
	environment := flags.String("environment", "", "environment ID to send requests with (default: the base environment)")
	cookieJar := flags.String("cookie-jar", "", "cookie jar ID to send requests with (default: the workspace's first)")
	flags.Usage = func() {
		_, _ = fmt.Fprintf(o.Err, "Usage: %s mcp [flags]\n\nServe Yeek's MCP tools over stdio. Every workspace is open to the agent.\n\nFlags:\n", o.Name)
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("mcp takes no arguments, got %q", flags.Arg(0))
	}
	e, err := engine.Open(o.Dir)
	if err != nil {
		return err
	}
	if settings, err := e.Store.Get(context.Background(), "default"); err != nil || settings["mcpServerEnabled"] != true {
		return errors.Join(errMCPOff, e.Close())
	}
	if o.Secrets != nil {
		e.Secrets = o.Secrets(o.Dir)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	host := &Host{Engine: e, EnvironmentID: *environment, CookieJarID: *cookieJar, Log: o.Err}
	transport := &sdk.IOTransport{Reader: io.NopCloser(o.In), Writer: nopWriteCloser{o.Out}}
	err = mcp.NewServer(host, o.Version).Run(ctx, transport)
	if ctx.Err() != nil || errors.Is(err, io.EOF) {
		err = nil
	}
	return errors.Join(err, e.Close())
}

var errMCPOff = errors.New("the MCP server is turned off: turn on \"Enable MCP server\" in Yeek's Settings > Agents")

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
