// Command fhj-study-mcp is a read-only MCP server for Moodle.
//
// Transports:
//
//	fhj-study-mcp                          stdio (default; for Claude Code and other local clients)
//	fhj-study-mcp -http 127.0.0.1:8765     streamable HTTP at /mcp
//
// Configuration comes from the environment only:
//
//	MOODLE_URL           site URL, e.g. https://moodle.fh-joanneum.at
//	MOODLE_TOKEN         personal web service token (moodle_mobile_app)
//	MOODLE_DOWNLOAD_DIR  default download directory (default ~/Downloads/moodle)
//	MOODLE_LOG_LEVEL     debug | info | warn | error (default info)
//	FHJ_TIMETABLE        timetable search for fhj_timetable, e.g. dat26
//	FHJ_TIMETABLE_GROUPS comma-separated groups to show besides classes for
//	                     everyone, e.g. "Gruppe 2"
//	FHJ_TZ               time zone dates are shown in, e.g. Europe/Vienna
//	                     (default: this computer's zone)
//	MCP_HTTP_ADDR        same as -http
//	MCP_HTTP_TOKEN       bearer token required by the HTTP transport
//	                     (mandatory when listening on a non-loopback address)
//
// Logs go to stderr; with stdio, stdout belongs to the MCP protocol.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/armenbarseghyan/fhj-study-mcp/internal/cache"
	"github.com/armenbarseghyan/fhj-study-mcp/internal/moodle"
	"github.com/armenbarseghyan/fhj-study-mcp/internal/server"
	"github.com/armenbarseghyan/fhj-study-mcp/internal/study"
	"github.com/armenbarseghyan/fhj-study-mcp/internal/textfmt"
	"github.com/armenbarseghyan/fhj-study-mcp/internal/timetable"
	"github.com/armenbarseghyan/fhj-study-mcp/internal/tools"
)

var version = "dev"

func main() {
	if err := run(); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "fhj-study-mcp:", err)
		os.Exit(1)
	}
}

func run() error {
	httpAddr := flag.String("http", os.Getenv("MCP_HTTP_ADDR"), "serve streamable HTTP on this address instead of stdio, e.g. 127.0.0.1:8765")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Fprintln(os.Stderr, "fhj-study-mcp", version)
		return nil
	}

	token := os.Getenv("MOODLE_TOKEN")
	httpToken := os.Getenv("MCP_HTTP_TOKEN")
	logger := slog.New(moodle.NewRedactingHandler(
		slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel(os.Getenv("MOODLE_LOG_LEVEL"))}),
		token,
	))

	textfmt.SetZone(displayZone(os.Getenv("FHJ_TZ"), logger))

	deps := tools.Deps{Version: version, Logger: logger,
		Timetable:       timetable.New(timetable.Options{}),
		TimetableQuery:  strings.TrimSpace(os.Getenv("FHJ_TIMETABLE")),
		TimetableGroups: splitList(os.Getenv("FHJ_TIMETABLE_GROUPS")),
	}
	client, err := newClient(token, logger)
	if err != nil {
		// Start anyway: every tool reports the problem, which is far more
		// helpful than the client's "Connection closed".
		logger.Error("configuration", "err", err)
		deps.InitErr = err
	} else {
		deps.Service = study.New(cache.NewSource(client, nil), study.Options{
			DownloadDir: os.Getenv("MOODLE_DOWNLOAD_DIR"),
			Logger:      logger,
		})
	}
	srv := server.New(deps)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *httpAddr == "" {
		logger.Info("fhj-study-mcp started", "version", version, "transport", "stdio")
		return srv.Run(ctx, &mcp.StdioTransport{})
	}
	return serveHTTP(ctx, srv, *httpAddr, httpToken, logger)
}

// splitList splits a comma-separated value, dropping empty items.
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// displayZone is FHJ_TZ when set and valid, otherwise this computer's zone:
// in Graz that is Vienna time; abroad, the local time.
func displayZone(name string, logger *slog.Logger) *time.Location {
	if name == "" {
		return time.Local
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		logger.Warn("FHJ_TZ is not a known time zone; using this computer's zone", "value", name, "err", err)
		return time.Local
	}
	return loc
}

func serveHTTP(ctx context.Context, srv *mcp.Server, addr, token string, logger *slog.Logger) error {
	if err := server.CheckListenAddr(addr, token); err != nil {
		return err
	}
	hs := &http.Server{
		Addr:              addr,
		Handler:           server.HTTPHandler(srv, server.HTTPOptions{Token: token, Logger: logger}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- hs.ListenAndServe() }()
	logger.Info("fhj-study-mcp started", "version", version, "transport", "http",
		"url", "http://"+addr+server.MCPPath, "auth", token != "")

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return hs.Shutdown(shutdown)
	}
}

func newClient(token string, logger *slog.Logger) (*moodle.Client, error) {
	base := os.Getenv("MOODLE_URL")
	switch {
	case base == "" && token == "":
		return nil, errors.New("MOODLE_URL and MOODLE_TOKEN are not set")
	case base == "":
		return nil, errors.New("MOODLE_URL is not set")
	case token == "":
		return nil, errors.New("MOODLE_TOKEN is not set")
	}
	return moodle.New(moodle.Config{BaseURL: base, Token: token, Logger: logger})
}

func logLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
