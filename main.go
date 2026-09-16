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
	"github.com/railadeividas/awxtui/internal/ui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "awxtui: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		configPath = flag.String("config", config.DefaultPath(), "path to the config file")
		instance   = flag.String("instance", "", "name of the configured instance to use")
		list       = flag.Bool("list", false, "list configured instances and exit")
		readOnly   = flag.Bool("read-only", false, "refuse every request that would change AWX")
	)
	flag.Usage = usage
	flag.Parse()

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

	token, err := inst.ResolveToken()
	if err != nil {
		return err
	}

	client := awx.New(inst.URL, token, inst.Insecure)
	if inst.ReadOnly || *readOnly {
		client = client.ReadOnly()
	}

	p := tea.NewProgram(
		ui.New(client, ui.WithInstance(inst.Name)),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	_, err = p.Run()
	return err
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
