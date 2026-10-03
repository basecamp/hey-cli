package harness

import (
	"os"
	"path/filepath"
	"testing"
)

// Every shared-skill agent is one row of the same table, so every test here
// runs once per row: a behavior Codex has that Grok lacks is a bug in the
// table, not a difference between them.
func forEachSkillAgent(t *testing.T, test func(t *testing.T, agent SkillAgent)) {
	t.Helper()
	for _, agent := range SkillAgents() {
		t.Run(agent.ID, func(t *testing.T) { test(t, agent) })
	}
}

func TestSkillAgentsAreRegistered(t *testing.T) {
	forEachSkillAgent(t, func(t *testing.T, agent SkillAgent) {
		info := FindAgent(agent.ID)
		if info == nil {
			t.Fatalf("%s agent not registered", agent.ID)
		}
		if info.Name != agent.Name || info.Detect == nil || info.FindBinary == nil || info.Checks == nil || info.Diagnostics == nil {
			t.Errorf("registration incomplete: %+v", info)
		}
	})
}

func TestSkillAgentDetectByHomeDirectory(t *testing.T) {
	forEachSkillAgent(t, func(t *testing.T, agent SkillAgent) {
		home := tempHome(t)
		t.Setenv("PATH", t.TempDir())
		t.Setenv(agent.HomeEnv, "")

		if agent.Detect() {
			t.Errorf("no ~/%s and no binary should not detect %s", agent.HomeDir, agent.Name)
		}
		if err := os.MkdirAll(filepath.Join(home, agent.HomeDir), 0o755); err != nil {
			t.Fatal(err)
		}
		if !agent.Detect() {
			t.Errorf("~/%s directory should detect %s", agent.HomeDir, agent.Name)
		}
	})
}

func TestSkillAgentDetectByBinary(t *testing.T) {
	forEachSkillAgent(t, func(t *testing.T, agent SkillAgent) {
		tempHome(t)
		t.Setenv(agent.HomeEnv, "")
		bin := t.TempDir()
		stub := filepath.Join(bin, agent.Binary)
		if err := os.WriteFile(stub, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", bin)

		if !agent.Detect() {
			t.Errorf("%s executable on PATH should detect %s without a home directory", agent.Binary, agent.Name)
		}
		if got := agent.FindBinary(); got != stub {
			t.Errorf("FindBinary() = %q, want %q", got, stub)
		}
	})
}

// Off PATH, the binary is found where an installer leaves it: ~/.local/bin,
// or the bin directory of the agent's own home — a relocated one included.
func TestSkillAgentFindBinaryOffPath(t *testing.T) {
	forEachSkillAgent(t, func(t *testing.T, agent SkillAgent) {
		cases := map[string]func(t *testing.T, home string) string{
			"local bin": func(_ *testing.T, home string) string { return filepath.Join(home, ".local", "bin") },
			"home bin":  func(_ *testing.T, home string) string { return filepath.Join(home, agent.HomeDir, "bin") },
			"env home bin": func(t *testing.T, _ string) string {
				override := t.TempDir()
				t.Setenv(agent.HomeEnv, override)
				return filepath.Join(override, "bin")
			},
		}
		for name, binDir := range cases {
			t.Run(name, func(t *testing.T) {
				home := tempHome(t)
				t.Setenv("PATH", t.TempDir())
				t.Setenv(agent.HomeEnv, "")
				if got := agent.FindBinary(); got != "" {
					t.Fatalf("FindBinary() = %q before any install, want none", got)
				}
				stub := filepath.Join(binDir(t, home), agent.Binary)
				if err := os.MkdirAll(filepath.Dir(stub), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(stub, []byte("#!/bin/sh\n"), 0o755); err != nil {
					t.Fatal(err)
				}
				if got := agent.FindBinary(); got != stub {
					t.Errorf("FindBinary() = %q, want %q", got, stub)
				}
			})
		}
	})
}

func TestSkillAgentHomeHonorsEnvOverride(t *testing.T) {
	forEachSkillAgent(t, func(t *testing.T, agent SkillAgent) {
		home := tempHome(t)

		t.Setenv(agent.HomeEnv, "")
		if got, want := agent.Home(), filepath.Join(home, agent.HomeDir); got != want {
			t.Errorf("Home() = %q, want %q", got, want)
		}

		override := t.TempDir()
		t.Setenv(agent.HomeEnv, override)
		if got := agent.Home(); got != override {
			t.Errorf("Home() = %q, want %q", got, override)
		}
		if got, want := agent.LegacySkillPath(), filepath.Join(override, "skills", "hey", "SKILL.md"); got != want {
			t.Errorf("LegacySkillPath() = %q, want %q", got, want)
		}
	})
}

func TestSkillAgentCheckSkill(t *testing.T) {
	forEachSkillAgent(t, func(t *testing.T, agent SkillAgent) {
		home := tempHome(t)
		t.Setenv(agent.HomeEnv, "")
		setup := "hey setup " + agent.ID

		check := agent.CheckSkill()
		if check.Name != agent.Name+" Skill" || check.Status != "fail" || check.Hint != "Run: "+setup {
			t.Errorf("missing skill: %+v", check)
		}

		skillDir := filepath.Join(home, ".agents", "skills", "hey")
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# hey"), 0o644); err != nil {
			t.Fatal(err)
		}
		// Present but unmarked is somebody else's skill occupying the path —
		// never reported as a connected integration.
		if check := agent.CheckSkill(); check.Status != "fail" || check.Hint != "Move it aside, then run: "+setup {
			t.Errorf("unmanaged skill: %+v", check)
		}

		if err := os.WriteFile(filepath.Join(skillDir, SkillOwnershipMarker), []byte("hey-cli"), 0o644); err != nil {
			t.Fatal(err)
		}
		if check := agent.CheckSkill(); check.Status != "pass" {
			t.Errorf("managed skill: %+v", check)
		}

		// A managed copy in the agent's own skills directory is a duplicate
		// the agent would list twice.
		legacyDir := filepath.Join(home, agent.HomeDir, "skills", "hey")
		if err := os.MkdirAll(legacyDir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, content := range map[string]string{
			"SKILL.md":           "# legacy hey",
			SkillOwnershipMarker: "hey-cli",
		} {
			if err := os.WriteFile(filepath.Join(legacyDir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if check := agent.CheckSkill(); check.Status != "fail" || check.Hint != "Run: "+setup {
			t.Errorf("managed duplicate skill: %+v", check)
		}

		// A home that aliases ~/.agents makes the old and current paths
		// identical. That is one skill, not a duplicate.
		t.Setenv(agent.HomeEnv, filepath.Join(home, ".agents"))
		if check := agent.CheckSkill(); check.Status != "pass" {
			t.Errorf("aliased current skill: %+v", check)
		}
	})
}

func TestSkillAgentCheckSkillReportsMissingAgentSkillsHome(t *testing.T) {
	forEachSkillAgent(t, func(t *testing.T, agent SkillAgent) {
		t.Setenv("HOME", "")
		t.Setenv("USERPROFILE", "")
		check := agent.CheckSkill()
		if check.Status != "warn" || check.Message != "Cannot determine shared Agent Skills directory" {
			t.Errorf("check = %+v", check)
		}
	})
}
