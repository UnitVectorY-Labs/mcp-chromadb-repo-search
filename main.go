package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/UnitVectorY-Labs/mcp-chromadb-repo-search/internal/content"
)

var Version = "dev" // Set by the build system to the release version

var semverRe = regexp.MustCompile(`^\d+\.\d+\.\d+`)

func versionString() string {
	version := Version
	if semverRe.MatchString(version) && !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	return fmt.Sprintf("mcp-chromadb-repo-search version %s (%s, %s/%s)", version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

func userAgent() string {
	version := strings.TrimPrefix(Version, "v")
	if version == "" {
		version = "dev"
	}
	return "mcp-chromadb-repo-search/" + version
}

func main() {
	if Version == "dev" || Version == "" {
		if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			Version = bi.Main.Version
		}
	}

	flags := content.NewFlagSet(flag.CommandLine)
	flag.Parse()
	if flags.Version {
		fmt.Println(versionString())
		return
	}

	cfg, err := content.LoadConfig(flags, os.Environ())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	cfg.UserAgent = userAgent()
	if cfg.HTTPAddr != "" {
		// HTTP request events are newline-delimited JSON on stdout. Do not add
		// the standard logger's timestamp prefix, which would make them invalid JSON.
		log.SetOutput(os.Stdout)
		log.SetFlags(0)
	} else if cfg.Debug {
		log.SetOutput(os.Stderr)
		log.Println("Debug mode enabled.")
	} else {
		log.SetOutput(io.Discard)
	}

	srv := content.CreateMCPServer(cfg, versionString())
	if err := content.Serve(srv, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: %v\n", err)
		os.Exit(1)
	}
}
