package cli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"yeek/internal/engine"
	"yeek/internal/mcp"
)

type Options struct {
	Context context.Context
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
  mcp        Serve Yeek's MCP tools to AI coding agents, over stdio or with --http

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
	overHTTP := flags.Bool("http", false, "serve over HTTP on 127.0.0.1 instead of stdio, for agents that cannot run this command")
	port := flags.Int("port", mcp.DefaultPort, "port to listen on with --http")
	token := flags.String("token", "", "bearer token clients must send with --http (default: generated and printed)")
	flags.Usage = func() {
		_, _ = fmt.Fprintf(o.Err, "Usage: %s mcp [flags]\n\nServe Yeek's MCP tools over stdio, or over HTTP with --http. Every workspace is open to the agent.\n\nFlags:\n", o.Name)
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("mcp takes no arguments, got %q", flags.Arg(0))
	}
	set := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { set[f.Name] = true })
	for _, name := range []string{"port", "token"} {
		if set[name] && !*overHTTP {
			return fmt.Errorf("--%s needs --http", name)
		}
	}
	if *port < 0 || *port > 65535 {
		return fmt.Errorf("--port %d is not a port", *port)
	}
	e, err := engine.Open(o.Dir)
	if err != nil {
		return err
	}
	if settings, err := e.Store.Get(context.Background(), "default"); err != nil || settings["mcpServerEnabled"] != true {
		return errors.Join(errMCPOff, e.Close())
	}
	for _, flagged := range []struct{ id, model, name string }{{*environment, "environment", "environment"}, {*cookieJar, "cookie_jar", "cookie jar"}} {
		if flagged.id == "" {
			continue
		}
		if m, err := e.Store.Get(context.Background(), flagged.id); err != nil || m["model"] != flagged.model {
			return errors.Join(fmt.Errorf("no %s with ID %s", flagged.name, flagged.id), e.Close())
		}
	}
	if o.Secrets != nil {
		e.Secrets = o.Secrets(o.Dir)
	}
	base := o.Context
	if base == nil {
		base = context.Background()
	}
	ctx, stop := signal.NotifyContext(base, os.Interrupt, syscall.SIGTERM)
	defer stop()
	host := &Host{Engine: e, EnvironmentID: *environment, CookieJarID: *cookieJar, Log: o.Err}
	if *overHTTP {
		return errors.Join(serveHTTP(ctx, host, o, *port, *token), e.Close())
	}
	transport := &sdk.IOTransport{Reader: io.NopCloser(wellFormed(o.In, o.Err)), Writer: nopWriteCloser{o.Out}}
	err = mcp.NewServer(host, o.Version).Run(ctx, transport)
	if ctx.Err() != nil || errors.Is(err, io.EOF) {
		err = nil
	}
	return errors.Join(err, e.Close())
}

var errMCPOff = errors.New("the MCP server is turned off: turn on \"Enable MCP server\" in Yeek's Settings > Agents")

func serveHTTP(ctx context.Context, host mcp.Host, o Options, port int, token string) error {
	if token == "" {
		var raw [32]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return err
		}
		token = hex.EncodeToString(raw[:])
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	server, err := mcp.Serve(host, o.Version, addr, token)
	if err != nil {
		return fmt.Errorf("failed to start MCP server on %s: %w", addr, err)
	}
	_, _ = fmt.Fprintf(o.Err, "Yeek MCP server listening on http://%s%s\nToken: %s\nClients must send it as \"Authorization: Bearer <token>\". Press Ctrl+C to stop.\n", server.Addr, mcp.Path, token)
	<-ctx.Done()
	server.Stop()
	return nil
}

func wellFormed(in io.Reader, log io.Writer) io.Reader {
	r, w := io.Pipe()
	go func() {
		lines := bufio.NewReader(in)
		for {
			line, err := readLine(lines, sdk.DefaultMaxLineLength)
			if line = bytes.TrimSpace(line); len(line) > 0 || errors.Is(err, errLineTooLong) {
				if wellFormedMessage(line) {
					if _, err := w.Write(append(line, '\n')); err != nil {
						return
					}
				} else if log != nil {
					_, _ = fmt.Fprintln(log, "[mcp] ignored a malformed message")
				}
			}
			if err != nil && !errors.Is(err, errLineTooLong) {
				_ = w.CloseWithError(err)
				return
			}
		}
	}()
	return r
}

var errLineTooLong = errors.New("line too long")

func readLine(r *bufio.Reader, limit int) ([]byte, error) {
	var line []byte
	tooLong := false
	for {
		chunk, err := r.ReadSlice('\n')
		if !tooLong && len(line)+len(chunk) <= limit {
			line = append(line, chunk...)
		} else {
			tooLong, line = true, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if tooLong && err == nil {
			err = errLineTooLong
		}
		return line, err
	}
}

func wellFormedMessage(line []byte) bool {
	if len(line) == 0 {
		return false
	}
	if line[0] != '[' {
		_, err := jsonrpc.DecodeMessage(line)
		return err == nil
	}
	var batch []jsontext.Value
	if err := json.Unmarshal(line, &batch); err != nil || len(batch) == 0 {
		return false
	}
	for _, message := range batch {
		if _, err := jsonrpc.DecodeMessage(message); err != nil {
			return false
		}
	}
	return true
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
