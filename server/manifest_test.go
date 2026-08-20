// Package main tests. Asserts ../manifest.yaml matches the spec's exact
// contract via a plain YAML parse (the SDK's manifest package is internal to
// the kandev module and not importable from a plugin module).
//
// This is load-bearing: plugin-pack/Install validate structure only, so
// wrong-but-valid values (a lowered min_kandev_version, dropped secret
// markers, a missing default, a second undeclared action) install cleanly and
// would go undetected without this test. `version` is shape-checked, NOT
// exact: the release workflow sed-bumps manifest.yaml + Makefile and then runs
// make test on the tag.
package main

import (
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type manifestAction struct {
	Key          string `yaml:"key"`
	Scope        string `yaml:"scope"`
	MaxBodyBytes int    `yaml:"max_body_bytes"`
}

type manifestConfigProperty struct {
	Type        string `yaml:"type"`
	Format      string `yaml:"format"`
	Secret      bool   `yaml:"secret"`
	Default     any    `yaml:"default"`
	Minimum     any    `yaml:"minimum"`
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
}

type manifest struct {
	ID               string   `yaml:"id"`
	APIVersion       int      `yaml:"api_version"`
	Version          string   `yaml:"version"`
	DisplayName      string   `yaml:"display_name"`
	Description      string   `yaml:"description"`
	Author           string   `yaml:"author"`
	Categories       []string `yaml:"categories"`
	RepoURL          string   `yaml:"repo_url"`
	MinKandevVersion string   `yaml:"min_kandev_version"`
	Runtime          struct {
		Type        string            `yaml:"type"`
		Executables map[string]string `yaml:"executables"`
	} `yaml:"runtime"`
	UI struct {
		Bundle string `yaml:"bundle"`
	} `yaml:"ui"`
	Actions []manifestAction `yaml:"actions"`
	Config  struct {
		Type       string                            `yaml:"type"`
		Properties map[string]manifestConfigProperty `yaml:"properties"`
	} `yaml:"config_schema"`
	// Absence of webhooks/capabilities is asserted via nil-ness; `any` keeps
	// the parse from failing if the manifest were ever to gain them.
	Webhooks     []any `yaml:"webhooks"`
	Capabilities any   `yaml:"capabilities"`
}

func loadManifest(t *testing.T) manifest {
	t.Helper()
	raw, err := os.ReadFile("../manifest.yaml")
	require.NoError(t, err, "manifest.yaml must exist next to the repo root")
	var m manifest
	require.NoError(t, yaml.Unmarshal(raw, &m), "manifest.yaml must be valid YAML")
	return m
}

func TestManifest_IdentityContract(t *testing.T) {
	m := loadManifest(t)

	require.Equal(t, "kandev-deepseek-credits", m.ID)
	require.Equal(t, "DeepSeek Credits", m.DisplayName, "pinned: the panel's Settings-path copy and task-07 navigation reference it")
	require.Equal(t, "DeepSeek API balance and remaining credits in the session top bar.", m.Description,
		"exact spec string; a divergent-but-non-empty description is precisely the wrong-but-valid value this test exists to catch")
	require.Equal(t, "kandev", m.Author, "NOT the template's your-name-here")
	require.Equal(t, "https://github.com/kdlbs/kandev-plugin-deepseek-credits", m.RepoURL,
		"the host renders repo_url as the Repo link on the installed-plugin list")
	require.Equal(t, 1, m.APIVersion)
	require.Regexp(t, regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`), m.Version, "SemVer shape, not an exact value: the release workflow bumps it and re-runs tests on the tag")
	require.Equal(t, "0.88.0", m.MinKandevVersion,
		"first released host version with authenticated plugin actions (PR #2117); a floor regression compiles clean at the pinned SDK and would go undetected without this assertion")
	require.Equal(t, []string{"analytics"}, m.Categories, "NOT the template's tools default")
}

func TestManifest_RuntimeAndUI(t *testing.T) {
	m := loadManifest(t)

	require.Equal(t, "binary", m.Runtime.Type)
	require.Equal(t, map[string]string{
		"linux-amd64":   "server/plugin-linux-amd64",
		"linux-arm64":   "server/plugin-linux-arm64",
		"darwin-amd64":  "server/plugin-darwin-amd64",
		"darwin-arm64":  "server/plugin-darwin-arm64",
		"windows-amd64": "server/plugin-windows-amd64.exe",
	}, m.Runtime.Executables)
	require.Equal(t, "/ui/bundle.js", m.UI.Bundle)
}

func TestManifest_ActionsContract(t *testing.T) {
	m := loadManifest(t)

	require.Len(t, m.Actions, 1, "a second undeclared action must fail this test")
	a := m.Actions[0]
	require.Equal(t, "balance.get", a.Key)
	require.Equal(t, "workspace", a.Scope, "the current manifest field name; resource_scope is the pre-release legacy spelling")
	require.Equal(t, 1024, a.MaxBodyBytes)
}

func TestManifest_ConfigSchemaContract(t *testing.T) {
	m := loadManifest(t)

	require.Equal(t, "object", m.Config.Type)
	require.Len(t, m.Config.Properties, 3)

	apiKey, ok := m.Config.Properties["api_key"]
	require.True(t, ok, "api_key property must exist")
	require.Equal(t, "string", apiKey.Type)
	require.True(t, apiKey.Secret, "secret: true is the ONLY marker that vault-stores the key; dropping it stores it in cleartext in <id>.config.yml and GetMaskedConfig returns it unmasked to the settings form")
	require.Equal(t, "password", apiKey.Format, "format: password renders the masked input; dropping it exposes the raw value to the settings form")
	require.Equal(t, "DeepSeek API key", apiKey.Title)
	require.NotEmpty(t, apiKey.Description)

	poll, ok := m.Config.Properties["poll_minutes"]
	require.True(t, ok)
	require.Equal(t, "number", poll.Type)
	require.Equal(t, 5, poll.Default)
	require.NotNil(t, poll.Minimum, "minimum: 1 is declarative only; the runtime floor clamp is the enforcement point")
	require.Equal(t, "Refresh interval (min)", poll.Title)
	require.NotEmpty(t, poll.Description)

	warn, ok := m.Config.Properties["warn_below"]
	require.True(t, ok)
	require.Equal(t, "number", warn.Type)
	require.Equal(t, 10, warn.Default)
	require.Equal(t, "Warn threshold", warn.Title)
	require.NotEmpty(t, warn.Description)
}

func TestManifest_NoWebhooksNoCapabilities(t *testing.T) {
	m := loadManifest(t)

	require.Nil(t, m.Webhooks, "no webhooks: balance data must never be reachable over an unauthenticated route")
	require.Nil(t, m.Capabilities, "no capabilities: no events, no state, no data reads")
}
