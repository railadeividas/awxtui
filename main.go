// Command awxtui is a terminal UI for AWX: browse job templates, launch them,
// and follow job output.
//
// It connects to the instance named by -instance, or to $AWX_URL and
// $AWX_TOKEN. Instances live in ~/.config/awxtui/config.yml:
//
//	default: prod
//	instances:
//	  prod:
//	    url: https://awx.example.com
//	    token_command: pass show awx/prod   # or token: <literal>
//	    read_only: true                     # refuse launches and cancels
//	  staging:
//	    url: https://awx-staging.example.com
//	    token: ...
//	    insecure: true                      # skip TLS verification
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
	"github.com/railadeividas/awxtui/internal/config"
	"github.com/railadeividas/awxtui/internal/state"
	"github.com/railadeividas/awxtui/internal/ui"
)

// version is replaced in release builds with:
//
//	go build -ldflags "-X main.version=v1.2.3" -o awxtui .
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "awxtui: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		configPath  = flag.String("config", config.DefaultPath(), "path to the config file")
		instance    = flag.String("instance", "", "name of the configured instance to use")
		list        = flag.Bool("list", false, "list configured instances and exit")
		showVersion = flag.Bool("version", false, "print the version and exit")
		readOnly    = flag.Bool("read-only", false, "refuse every request that would change AWX")
		statePath   = flag.String("state", state.DefaultPath(), "path to the file of pinned records")
	)
	flag.Usage = usage
	flag.Parse()
	if *showVersion {
		fmt.Println(versionString())
		return nil
	}

	file, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *list {
		return listInstances(file)
	}

	inst, err := config.Resolve(file, *instance, os.Getenv)
	if err != nil {
		return err
	}
	if file.InsecurePerms {
		fmt.Fprintf(os.Stderr,
			"awxtui: warning: %s holds a token but is readable by others; chmod 600 it\n", file.Path)
	}

	// build turns a resolved instance into a client, running its
	// token_command if it has one.
	build := func(i config.Instance) (*awx.Client, error) {
		token, err := i.ResolveToken()
		if err != nil {
			return nil, err
		}
		c := awx.New(i.URL, token, i.Insecure)
		if i.ReadOnly || *readOnly {
			c = c.ReadOnly()
		}
		return c, nil
	}

	client, err := build(inst)
	if err != nil {
		return err
	}

	// connector lets the UI switch instances without restarting. The active
	// instance may have come from the environment rather than the file, so it
	// is handled separately.
	connector := func(name string) (*awx.Client, error) {
		if name == inst.Name {
			return build(inst)
		}
		other, err := config.Resolve(file, name, os.Getenv)
		if err != nil {
			return nil, err
		}
		return build(other)
	}

	// Pins are a convenience, not a prerequisite: an unreadable state file
	// is worth a warning, not a refusal to start.
	store, err := state.Load(*statePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "awxtui: warning: %v; pins will not persist\n", err)
	}

	p := tea.NewProgram(
		ui.New(client,
			ui.WithVersion(version),
			ui.WithInstances(instanceList(file, inst), inst.Name),
			ui.WithConnector(connector),
			ui.WithStore(store),
		),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	_, err = p.Run()
	return err
}

func versionString() string { return "awxtui " + version }

// instanceList is everything the switcher can offer: the configured
// instances, plus the active one when it came from the environment.
func instanceList(f config.File, active config.Instance) []ui.InstanceInfo {
	var list []ui.InstanceInfo
	seen := false
	for _, name := range f.Names() {
		i := f.Instances[name]
		list = append(list, ui.InstanceInfo{Name: name, URL: i.URL, ReadOnly: i.ReadOnly})
		seen = seen || name == active.Name
	}
	if !seen {
		list = append([]ui.InstanceInfo{{
			Name: active.Name, URL: active.URL, ReadOnly: active.ReadOnly,
		}}, list...)
	}
	return list
}

func listInstances(f config.File) error {
	if len(f.Instances) == 0 {
		fmt.Printf("no instances configured in %s\n", f.Path)
		return nil
	}
	fmt.Printf("instances in %s:\n", f.Path)
	for _, name := range f.Names() {
		inst := f.Instances[name]
		marks := []string{}
		if name == f.Default {
			marks = append(marks, "default")
		}
		if inst.ReadOnly {
			marks = append(marks, "read-only")
		}
		if inst.Insecure {
			marks = append(marks, "insecure")
		}
		suffix := ""
		if len(marks) > 0 {
			suffix = "  (" + strings.Join(marks, ", ") + ")"
		}
		fmt.Printf("  %-16s %s%s\n", name, inst.URL, suffix)
	}
	return nil
}

func usage() {
	out := flag.CommandLine.Output()
	fmt.Fprintf(out, `awxtui - a terminal UI for AWX

Usage:
  awxtui [flags]

Flags:
`)
	flag.PrintDefaults()
	fmt.Fprintf(out, `
Environment:
  AWX_URL           base URL of the AWX instance, e.g. https://awx.example.com
  AWX_TOKEN         personal OAuth2 token
  AWX_INSECURE      set to 1 to skip TLS verification
  AWXTUI_INSTANCE   name of the configured instance to use
  AWXTUI_READONLY   set to 1 to refuse every request that would change AWX

Config file (%s):
  default: prod
  instances:
    prod:
      url: https://awx.example.com
      token_command: pass show awx/prod
      read_only: true
`, config.DefaultPath())
}
