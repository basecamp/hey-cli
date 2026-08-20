package tui

import (
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/fsnotify/fsnotify"
)

// themeChangedMsg reports that the Omarchy theme directory changed on disk.
type themeChangedMsg struct{}

// themeSettleDelay lets omarchy-theme-set finish its rm/mv swap and the template
// renders that follow before the theme is re-read, so one switch costs one restyle.
const themeSettleDelay = 250 * time.Millisecond

// watchThemeCmd blocks until something changes in dir, then reports it once. The
// handler re-arms it; a fresh watcher per cycle means a directory that was swapped
// out from under us is simply watched again by name. The theme directory inside is
// watched too when it exists: omarchy-theme-set renders templates before its
// atomic swap of the whole directory, but omarchy-theme-refresh re-renders files
// in place, which the non-recursive parent watch would never see. Returns nil
// when there is nothing to watch.
func watchThemeCmd(dir string) tea.Cmd {
	if dir == "" {
		return nil
	}
	return func() tea.Msg {
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			return nil
		}
		defer watcher.Close()
		if err := watcher.Add(dir); err != nil {
			return nil
		}
		_ = watcher.Add(filepath.Join(dir, "theme")) // absent mid-swap; the parent event re-arms us
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return nil
				}
				if event.Has(fsnotify.Create) || event.Has(fsnotify.Rename) || event.Has(fsnotify.Write) || event.Has(fsnotify.Remove) {
					time.Sleep(themeSettleDelay)
					return themeChangedMsg{}
				}
			case _, ok := <-watcher.Errors:
				if !ok {
					return nil
				}
			}
		}
	}
}
