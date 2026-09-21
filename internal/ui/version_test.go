package ui

import (
	"strings"
	"testing"

	"github.com/railadeividas/awxtui/internal/awx"
)

func TestHelpShowsVersion(t *testing.T) {
	m := New(awx.New("https://awx.example.test", "token", false), WithVersion("v1.2.3"))
	m.width = 120

	if got := stripANSI(m.helpModal()); !strings.Contains(got, "awxtui version v1.2.3") {
		t.Errorf("help = %q, want version", got)
	}
	if got := stripANSI(m.headerView()); strings.Contains(got, "version") {
		t.Errorf("header = %q, should not show version", got)
	}
}

func TestHelpDefaultsToDevelopmentVersion(t *testing.T) {
	m := New(awx.New("https://awx.example.test", "token", false))
	m.width = 120

	if got := stripANSI(m.helpModal()); !strings.Contains(got, "awxtui version dev") {
		t.Errorf("help = %q, want development version", got)
	}
}

func TestHeaderLabelsItsContext(t *testing.T) {
	m := New(awx.New("https://awx.example.test", "token", false),
		WithInstances([]InstanceInfo{{Name: "prod"}}, "prod"))
	m.width = 120
	m.user = "admin"

	got := strings.Join(strings.Fields(stripANSI(m.headerView())), " ")
	for _, want := range []string{"instance prod", "account admin@awx.example.test"} {
		if !strings.Contains(got, want) {
			t.Errorf("header = %q, missing %q", got, want)
		}
	}
}
