package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/tder311/agent-tui/internal/cleanup"
	"github.com/tder311/agent-tui/internal/config"
)

func cleanupApp(t *testing.T) appModel {
	t.Helper()
	m := appModel{width: 120, height: 40, ready: true, cfg: config.Default(), nav: newNavTreeModel(navWidth)}
	m.layout()
	m.cleanup = &cleanupModel{loading: true}
	next, _ := m.Update(CleanupScanMsg{Items: []cleanup.Candidate{
		{Kind: cleanup.KindFinishedAgent, ID: "aaaaaaa1", Title: "e2e run", State: "done", Bytes: 2 << 20, Updated: time.Now().Add(-9 * 24 * time.Hour)},
		{Kind: cleanup.KindJobScratch, ID: "aaaaaaa2", Title: "wind", State: "done", Bytes: 7 << 30, Updated: time.Now(), Default: true},
		{Kind: cleanup.KindProjectStub, ID: "-Users-x-gone", Bytes: 800, Default: true},
	}})
	return next.(appModel)
}

func press(t *testing.T, m appModel, keys ...string) appModel {
	t.Helper()
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case " ":
			msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		next, _ := m.Update(msg)
		m = next.(appModel)
	}
	return m
}

func TestCleanupPreselectsDefaultsAndRenders(t *testing.T) {
	m := cleanupApp(t)
	if got := m.cleanup.selected; got[0] || !got[1] || !got[2] {
		t.Fatalf("preselection = %v, want [false true true]", got)
	}
	v := m.View()
	for _, want := range []string{"Cleanup", "Finished agents", "Job scratch dirs", "Dead transcript stubs", "aaaaaaa1", "-Users-x-gone", "7.0 GB", "2 of 3 selected"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q", want)
		}
	}
}

func TestCleanupToggleAndConfirmGate(t *testing.T) {
	m := cleanupApp(t)
	m = press(t, m, " ")
	if !m.cleanup.selected[0] {
		t.Fatal("space should select the cursor row")
	}
	m = press(t, m, "a")
	for i, s := range m.cleanup.selected {
		if s {
			t.Fatalf("a with everything selected should clear all, row %d still set", i)
		}
	}
	m = press(t, m, "enter")
	if m.cleanup.confirming {
		t.Fatal("enter with nothing selected must not ask to confirm")
	}
	m = press(t, m, "a", "enter")
	if !m.cleanup.confirming || !strings.Contains(m.View(), "Delete 3 item(s)") {
		t.Fatal("enter with a selection should show the delete confirmation")
	}
	m = press(t, m, "n")
	if m.cleanup.confirming || m.cleanup.running {
		t.Fatal("n should cancel without running")
	}
	m = press(t, m, "esc")
	if m.cleanup != nil {
		t.Fatal("esc should close the cleanup screen")
	}
}

func TestCleanupDoneShowsSummaryAndRescans(t *testing.T) {
	m := cleanupApp(t)
	m.cleanup.running = true
	next, cmd := m.Update(CleanupDoneMsg{Results: []cleanup.Result{
		{Candidate: cleanup.Candidate{Bytes: 1024}},
		{Candidate: cleanup.Candidate{ID: "x"}, Err: errFake},
	}})
	m = next.(appModel)
	if cmd == nil || !m.cleanup.loading || m.cleanup.running {
		t.Fatal("finishing a cleanup should trigger a rescan")
	}
	v := m.View()
	if !strings.Contains(v, "1 cleaned, 1.0 KB freed, 1 failed") || !strings.Contains(v, errFake.Error()) {
		t.Errorf("summary/failure not rendered:\n%s", v)
	}
}
