// Package config resolves which AWX instance to connect to, from a config
// file, the environment, or both.
package config

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Instance is one AWX endpoint.
type Instance struct {
	// Name is the key it was configured under; "env" for one built from
	// environment variables.
	Name string `yaml:"-"`

	URL   string `yaml:"url"`
	Token string `yaml:"token"`
	// TokenCommand is run to obtain the token, so it need not be written to
	// disk in plain text, e.g. "pass show awx/prod".
	TokenCommand string `yaml:"token_command"`

	Insecure bool `yaml:"insecure"`
	// ReadOnly refuses every state-changing request. Set it for production.
	ReadOnly bool `yaml:"read_only"`
}

// File is the parsed config file.
type File struct {
	// Default names the instance to use when none is requested.
	Default   string              `yaml:"default"`
	Instances map[string]Instance `yaml:"instances"`

	// Path is where this was read from, empty when there was no file.
	Path string `yaml:"-"`
	// InsecurePerms is true when the file holds a literal token but is
	// readable by anyone but its owner.
	InsecurePerms bool `yaml:"-"`
}

// DefaultPath is ~/.config/awxtui/config.yml, honouring XDG_CONFIG_HOME.
func DefaultPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "awxtui", "config.yml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "awxtui", "config.yml")
}

// Load reads a config file. A missing file is not an error: the environment
// alone is a perfectly good configuration.
func Load(path string) (File, error) {
	var f File
	if path == "" {
		return f, nil
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return f, fmt.Errorf("%s: %w", path, err)
	}
	f.Path = path
	for name, inst := range f.Instances {
		inst.Name = name
		f.Instances[name] = inst
	}
	if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0o077 != 0 {
		for _, inst := range f.Instances {
			if strings.TrimSpace(inst.Token) != "" {
				f.InsecurePerms = true
				break
			}
		}
	}
	return f, nil
}

// Names lists the configured instances, sorted.
func (f File) Names() []string {
	names := make([]string, 0, len(f.Instances))
	for name := range f.Instances {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Resolve picks the instance to connect to, in this order:
//
//  1. the name explicitly requested (--instance)
//  2. $AWXTUI_INSTANCE
//  3. $AWX_URL and $AWX_TOKEN, which are an instance in their own right
//  4. the config file's default
//  5. the only configured instance, if there is exactly one
//
// An instance missing a token falls back to $AWX_TOKEN, so a config file can
// hold URLs while secrets stay in the environment.
func Resolve(f File, requested string, env func(string) string) (Instance, error) {
	pick := func(name string) (Instance, error) {
		inst, ok := f.Instances[name]
		if !ok {
			return Instance{}, fmt.Errorf("no instance named %q%s", name, available(f))
		}
		inst.Name = name
		return inst, nil
	}

	var inst Instance
	var err error
	switch {
	case strings.TrimSpace(requested) != "":
		inst, err = pick(strings.TrimSpace(requested))
	case strings.TrimSpace(env("AWXTUI_INSTANCE")) != "":
		inst, err = pick(strings.TrimSpace(env("AWXTUI_INSTANCE")))
	case env("AWX_URL") != "" && env("AWX_TOKEN") != "":
		inst = Instance{Name: "env", URL: env("AWX_URL"), Token: env("AWX_TOKEN")}
	case strings.TrimSpace(f.Default) != "":
		inst, err = pick(strings.TrimSpace(f.Default))
	case len(f.Instances) == 1:
		inst, err = pick(f.Names()[0])
	default:
		return Instance{}, fmt.Errorf("no AWX instance configured: set AWX_URL and AWX_TOKEN, "+
			"or add one to %s%s", displayPath(f), available(f))
	}
	if err != nil {
		return Instance{}, err
	}

	if inst.URL == "" {
		inst.URL = env("AWX_URL")
	}
	if inst.Token == "" && inst.TokenCommand == "" {
		inst.Token = env("AWX_TOKEN")
	}
	if !inst.Insecure {
		inst.Insecure = truthy(env("AWX_INSECURE"))
	}
	if !inst.ReadOnly {
		inst.ReadOnly = truthy(env("AWXTUI_READONLY"))
	}

	if strings.TrimSpace(inst.URL) == "" {
		return Instance{}, fmt.Errorf("instance %q has no url", inst.Name)
	}
	if strings.TrimSpace(inst.Token) == "" && strings.TrimSpace(inst.TokenCommand) == "" {
		return Instance{}, fmt.Errorf("instance %q has no token or token_command "+
			"(and AWX_TOKEN is not set)", inst.Name)
	}
	return inst, nil
}

func available(f File) string {
	if len(f.Instances) == 0 {
		return ""
	}
	return fmt.Sprintf("; configured: %s", strings.Join(f.Names(), ", "))
}

func displayPath(f File) string {
	if f.Path != "" {
		return f.Path
	}
	if p := DefaultPath(); p != "" {
		return p
	}
	return "the config file"
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// ResolveToken returns the instance's token, running token_command when one is
// configured so secrets need not be stored on disk.
func (i Instance) ResolveToken() (string, error) {
	if cmd := strings.TrimSpace(i.TokenCommand); cmd != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "sh", "-c", cmd).Output()
		if err != nil {
			var stderr string
			if ee, ok := err.(*exec.ExitError); ok {
				stderr = strings.TrimSpace(string(ee.Stderr))
			}
			return "", fmt.Errorf("token_command for %q failed: %w: %s", i.Name, err, stderr)
		}
		token := unquote(strings.TrimSpace(string(out)))
		if token == "" {
			return "", fmt.Errorf("token_command for %q produced no output", i.Name)
		}
		return token, nil
	}
	return unquote(strings.TrimSpace(i.Token)), nil
}

// unquote strips surrounding quotes, which is what you get when a token is
// read straight out of a shell env file such as `export AWX_TOKEN="abc"`.
func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
