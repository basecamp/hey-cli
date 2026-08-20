package harness

import "testing"

// withCleanRegistry empties the registry for a test and restores the real
// claude/codex registrations from init() afterwards.
func withCleanRegistry(t *testing.T) {
	t.Helper()
	registryMu.Lock()
	saved := registry
	registry = nil
	registryMu.Unlock()
	t.Cleanup(func() {
		registryMu.Lock()
		registry = saved
		registryMu.Unlock()
	})
}

func TestRegisterAndFindAgent(t *testing.T) {
	withCleanRegistry(t)

	RegisterAgent(AgentInfo{Name: "Test Agent", ID: "test", Detect: func() bool { return true }})

	found := FindAgent("test")
	if found == nil {
		t.Fatal("FindAgent returned nil")
	}
	if found.Name != "Test Agent" || found.ID != "test" {
		t.Errorf("found = %+v", found)
	}
	if FindAgent("nonexistent") != nil {
		t.Error("FindAgent should return nil for unknown IDs")
	}
}

func TestDetectedAgents(t *testing.T) {
	withCleanRegistry(t)

	RegisterAgent(AgentInfo{ID: "yes", Detect: func() bool { return true }})
	RegisterAgent(AgentInfo{ID: "no", Detect: func() bool { return false }})
	RegisterAgent(AgentInfo{ID: "also-yes", Detect: func() bool { return true }})

	detected := DetectedAgents()
	if len(detected) != 2 || detected[0].ID != "yes" || detected[1].ID != "also-yes" {
		t.Errorf("detected = %+v", detected)
	}
}

func TestAllAgentsReturnsEveryRegistration(t *testing.T) {
	withCleanRegistry(t)

	RegisterAgent(AgentInfo{ID: "a"})
	RegisterAgent(AgentInfo{ID: "b"})

	if all := AllAgents(); len(all) != 2 {
		t.Errorf("AllAgents = %+v", all)
	}
}

func TestRegisterAgentPanicsOnBadIDs(t *testing.T) {
	withCleanRegistry(t)

	assertPanics := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s should panic", name)
			}
		}()
		fn()
	}

	assertPanics("empty ID", func() { RegisterAgent(AgentInfo{Name: "Bad Agent"}) })
	RegisterAgent(AgentInfo{ID: "dup", Name: "First"})
	assertPanics("duplicate ID", func() { RegisterAgent(AgentInfo{ID: "dup", Name: "Second"}) })
}

func TestDefaultRegistryHasClaudeAndCodex(t *testing.T) {
	if FindAgent("claude") == nil {
		t.Error("claude agent not registered")
	}
	if FindAgent("codex") == nil {
		t.Error("codex agent not registered")
	}
}
