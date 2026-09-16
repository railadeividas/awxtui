// Command awxtui is a small terminal UI for AWX: browse job templates,
// launch them, and follow job output.
//
// Configuration comes from the environment:
//
//	AWX_URL           base URL of the AWX instance, e.g. https://awx.example.com
//	AWX_TOKEN         personal OAuth2 token
//	AWX_INSECURE      set to 1/true to skip TLS verification
//	AWXTUI_READONLY   set to 1/true to refuse every state-changing request,
//	                  so nothing can be launched or cancelled by accident
package main

import (
	"fmt"
	"os"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/railadeividas/awxtui/internal/awx"
	"github.com/railadeividas/awxtui/internal/ui"
)

func main() {
	url, token := os.Getenv("AWX_URL"), os.Getenv("AWX_TOKEN")
	var missing []string
	if url == "" {
		missing = append(missing, "AWX_URL")
	}
	if token == "" {
		missing = append(missing, "AWX_TOKEN")
	}
	if len(missing) > 0 {
		for _, v := range missing {
			fmt.Fprintf(os.Stderr, "awxtui: %s is not set\n", v)
		}
		fmt.Fprintln(os.Stderr, "\n  export AWX_URL=https://awx.example.com\n  export AWX_TOKEN=<personal access token>")
		os.Exit(2)
	}

	insecure, _ := strconv.ParseBool(os.Getenv("AWX_INSECURE"))
	client := awx.New(url, token, insecure)
	if readOnly, _ := strconv.ParseBool(os.Getenv("AWXTUI_READONLY")); readOnly {
		client = client.ReadOnly()
	}

	p := tea.NewProgram(ui.New(client), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "awxtui: %v\n", err)
		os.Exit(1)
	}
}
