package mcpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

// SendState is send_draft's state on stdio (sending-safety D2.3, D4, D6; roadmap
// "Implementation requirements"): the per-draft send claims, each grant's caps and
// new-address membership, and each grant's exposure marker, in one JSON file, with the
// confirmation key beside it.
//
// Every read-check-write runs under an exclusive OS file lock on a separate lock file
// that is never renamed or replaced, held across the whole read, check, write and
// rename: two hey mcp processes can't both claim one draft or both spend the last of a
// cap. The new file is fsynced before the rename and its directory after it, under the
// lock, and a failed fsync fails the operation, so an acknowledged claim survives a
// crash. A lock that can't be taken within lockTimeout fails closed.
type SendState struct {
	dir string
	// lockTimeout bounds the wait for another process's update.
	lockTimeout time.Duration
	// fsync syncs a file or directory; a seam so a test can make it fail.
	fsync func(*os.File) error

	mu sync.Mutex
}

const (
	sendStateFile   = "mcp-send-state.json"
	sendStateLock   = "mcp-send-state.lock"
	confirmKeyFile  = "mcp-confirm.key"
	confirmKeyBytes = 32
)

// claimTTL is how long a claimed draft stays claimed: well past the confirmation's TTL
// and HEY's undo window (D2.3).
const claimTTL = 24 * time.Hour

// capWindow is the rolling window the per-grant caps count over (D4).
const capWindow = 24 * time.Hour

// OpenSendState opens (creating) the send state directory.
func OpenSendState(dir string) (*SendState, error) {
	if dir == "" {
		return nil, errors.New("no state directory for hey mcp's send safety state")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	return &SendState{dir: dir, lockTimeout: 5 * time.Second, fsync: (*os.File).Sync}, nil
}

// sendStateData is the state file's content.
type sendStateData struct {
	Version int `json:"version"`
	// Claims maps a claimed draft (claimKey) to when it was claimed, Unix seconds. A
	// claim is never released early (D2.3).
	Claims map[string]int64 `json:"claims"`
	// Grants holds each grant's caps and exposure, by grant key.
	Grants map[string]*grantState `json:"grants"`
}

type grantState struct {
	// Sends are when each confirmed send was claimed, Unix seconds.
	Sends []int64 `json:"sends,omitempty"`
	// NewAddresses are the new addresses sent to in the window, each with when it was
	// last sent to: a send is charged only for new addresses not already here (D4).
	NewAddresses map[string]int64 `json:"new_addresses,omitempty"`
	// ExposedUntil is when the exposure window ends, Unix seconds (D6).
	ExposedUntil int64 `json:"exposed_until,omitempty"`
	// ExposedBy names the read that last exposed the grant.
	ExposedBy string `json:"exposed_by,omitempty"`
}

// claimKey is a draft's claim: the HEY it lives on and its id, and nothing else, so
// every confirmation of that draft contends for one claim whatever its grant, content
// or schedule (D2.3).
func claimKey(origin string, draftID int64) string {
	return fmt.Sprintf("%s#%d", origin, draftID)
}

func (d *sendStateData) grant(key string) *grantState {
	g := d.Grants[key]
	if g == nil {
		g = &grantState{}
		d.Grants[key] = g
	}
	if g.NewAddresses == nil {
		g.NewAddresses = map[string]int64{}
	}
	return g
}

// prune drops what has aged out of every window.
func (d *sendStateData) prune(now time.Time) {
	for key, at := range d.Claims {
		if now.Sub(time.Unix(at, 0)) >= claimTTL {
			delete(d.Claims, key)
		}
	}
	for key, g := range d.Grants {
		g.Sends = slices.DeleteFunc(g.Sends, func(at int64) bool { return now.Sub(time.Unix(at, 0)) >= capWindow })
		for address, at := range g.NewAddresses {
			if now.Sub(time.Unix(at, 0)) >= capWindow {
				delete(g.NewAddresses, address)
			}
		}
		if g.ExposedUntil != 0 && !now.Before(time.Unix(g.ExposedUntil, 0)) {
			g.ExposedUntil, g.ExposedBy = 0, ""
		}
		if len(g.Sends) == 0 && len(g.NewAddresses) == 0 && g.ExposedUntil == 0 {
			delete(d.Grants, key)
		}
	}
}

// update runs fn on the state under the lock and writes the result durably. fn's error
// leaves the file as it was.
func (s *SendState) update(ctx context.Context, fn func(*sendStateData) error) error {
	return s.locked(ctx, func() error {
		data, err := s.read()
		if err != nil {
			return err
		}
		if err := fn(data); err != nil {
			return err
		}
		return s.write(data)
	})
}

// view runs fn on the state under the lock, writing nothing.
func (s *SendState) view(ctx context.Context, fn func(*sendStateData)) error {
	return s.locked(ctx, func() error {
		data, err := s.read()
		if err != nil {
			return err
		}
		fn(data)
		return nil
	})
}

func (s *SendState) locked(ctx context.Context, fn func() error) error {
	// A file lock belongs to an open file, not a goroutine, so the mutex is what makes
	// it exclusive inside this process.
	s.mu.Lock()
	defer s.mu.Unlock()

	lock := flock.New(filepath.Join(s.dir, sendStateLock))
	waiting, cancel := context.WithTimeout(ctx, s.lockTimeout)
	defer cancel()
	locked, err := lock.TryLockContext(waiting, 25*time.Millisecond)
	if err != nil {
		return fmt.Errorf("could not lock hey mcp's send state: %w", err)
	}
	if !locked {
		return errors.New("hey mcp's send state is locked by another process")
	}
	defer func() { _ = lock.Unlock() }()
	return fn()
}

func (s *SendState) read() (*sendStateData, error) {
	data := &sendStateData{Version: 1, Claims: map[string]int64{}, Grants: map[string]*grantState{}}
	raw, err := os.ReadFile(filepath.Join(s.dir, sendStateFile))
	if errors.Is(err, os.ErrNotExist) {
		return data, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read hey mcp's send state: %w", err)
	}
	if err := json.Unmarshal(raw, data); err != nil {
		// A state file that doesn't parse can't say what was claimed or charged.
		return nil, fmt.Errorf("hey mcp's send state %s is unreadable: %w", filepath.Join(s.dir, sendStateFile), err)
	}
	if data.Claims == nil {
		data.Claims = map[string]int64{}
	}
	if data.Grants == nil {
		data.Grants = map[string]*grantState{}
	}
	return data, nil
}

// write replaces the state file: a temporary file fsynced, renamed into place, and the
// directory fsynced. Any failure fails the update.
func (s *SendState) write(data *sendStateData) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, sendStateFile+".*.tmp")
	if err != nil {
		return fmt.Errorf("write hey mcp's send state: %w", err)
	}
	tmpPath := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tmpPath) //nolint:gosec // G703: path from os.CreateTemp
		}
	}()

	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write hey mcp's send state: %w", err)
	}
	if err := s.fsync(tmp); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsync hey mcp's send state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write hey mcp's send state: %w", err)
	}
	if err := os.Rename(tmpPath, filepath.Join(s.dir, sendStateFile)); err != nil { //nolint:gosec // G703: paths built from the state directory
		return fmt.Errorf("replace hey mcp's send state: %w", err)
	}
	keep = true
	return s.syncDir()
}

// syncDir fsyncs the state directory, so the rename itself is durable. Windows can't
// open a directory for syncing; NTFS journals the rename's metadata.
func (s *SendState) syncDir() error {
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(s.dir)
	if err != nil {
		return fmt.Errorf("fsync hey mcp's state directory: %w", err)
	}
	defer dir.Close()
	if err := s.fsync(dir); err != nil {
		return fmt.Errorf("fsync hey mcp's state directory: %w", err)
	}
	return nil
}

// ConfirmKey is the installation's confirmation key, generated once under the lock and
// kept beside the state, never regenerated per process (D4).
func (s *SendState) ConfirmKey(ctx context.Context) ([]byte, error) {
	var key []byte
	err := s.locked(ctx, func() error {
		path := filepath.Join(s.dir, confirmKeyFile)
		raw, err := os.ReadFile(path) //nolint:gosec // G304: path built from the state directory
		if err == nil {
			decoded, decodeErr := hex.DecodeString(string(raw))
			if decodeErr != nil || len(decoded) != confirmKeyBytes {
				return fmt.Errorf("hey mcp's confirmation key %s is malformed; remove it to make a new one", path)
			}
			key = decoded
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read hey mcp's confirmation key: %w", err)
		}

		fresh := make([]byte, confirmKeyBytes)
		if _, randErr := rand.Read(fresh); randErr != nil {
			return randErr
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // G304: path built from the state directory
		if err != nil {
			return fmt.Errorf("create hey mcp's confirmation key: %w", err)
		}
		if _, err := file.WriteString(hex.EncodeToString(fresh)); err != nil {
			_ = file.Close()
			return err
		}
		if err := s.fsync(file); err != nil {
			_ = file.Close()
			return fmt.Errorf("fsync hey mcp's confirmation key: %w", err)
		}
		if err := file.Close(); err != nil {
			return err
		}
		key = fresh
		return s.syncDir()
	})
	return key, err
}

// exposureWindow is how long an unscreened read tightens sends (D6).
const exposureWindow = 10 * time.Minute

// MarkExposed records that the grant read content that may be an unscreened sender's,
// restarting its exposure window. It runs before that content is returned.
func (s *SendState) MarkExposed(ctx context.Context, grantKey, by string, now time.Time) error {
	return s.update(ctx, func(d *sendStateData) error {
		d.prune(now)
		g := d.grant(grantKey)
		g.ExposedUntil = now.Add(exposureWindow).Unix()
		g.ExposedBy = by
		return nil
	})
}

// exposure reports whether the grant is exposed now, and by which read.
func (s *SendState) exposure(ctx context.Context, grantKey string, now time.Time) (bool, string, error) {
	var exposed bool
	var by string
	err := s.view(ctx, func(d *sendStateData) {
		d.prune(now)
		if g := d.Grants[grantKey]; g != nil && g.ExposedUntil != 0 {
			exposed, by = true, g.ExposedBy
		}
	})
	return exposed, by, err
}

// budget is what a grant has spent in the window: its sends, and the new addresses
// already charged.
func (s *SendState) budget(ctx context.Context, grantKey string, now time.Time) (int, map[string]bool, error) {
	var sends int
	charged := map[string]bool{}
	err := s.view(ctx, func(d *sendStateData) {
		d.prune(now)
		if g := d.Grants[grantKey]; g != nil {
			sends = len(g.Sends)
			for address := range g.NewAddresses {
				charged[address] = true
			}
		}
	})
	return sends, charged, err
}

// refusalError is a send the state refused: the reason is the in-band error.
type refusalError struct{ reason string }

func (r *refusalError) Error() string { return r.reason }

// claim takes a draft's send claim and charges the grant's caps, in one locked update,
// or refuses. It re-reads exposure there: a draft with a new recipient, or one going as
// it is rather than as the plain text the confirmation showed (unseen), is refused if
// an unscreened read landed since the policy was checked (D2, D6). Nothing is written
// when it refuses.
func (s *SendState) claim(ctx context.Context, origin string, draftID int64, grantKey string, newAddresses []string, unseen bool, now time.Time) error {
	return s.update(ctx, func(d *sendStateData) error {
		d.prune(now)
		key := claimKey(origin, draftID)
		if _, taken := d.Claims[key]; taken {
			return &refusalError{"this draft was already sent, or a send of it is in progress, through hey mcp in the last 24 hours, so it wasn't sent again. Check Sent in HEY. To send it anyway, send it from HEY"}
		}

		g := d.grant(grantKey)
		if g.ExposedUntil != 0 && len(newAddresses) > 0 {
			return &refusalError{fmt.Sprintf("not sent: this conversation read mail that may be from an unscreened sender (%s) in the last %d minutes, and the draft is addressed to someone you haven't emailed before (%s). Send it from HEY", g.ExposedBy, int(exposureWindow/time.Minute), newAddresses[0])}
		}
		if g.ExposedUntil != 0 && unseen {
			return &refusalError{fmt.Sprintf("not sent: this conversation read mail that may be from an unscreened sender (%s) in the last %d minutes, and the draft can't be fully reviewed in the confirmation. Send it from HEY", g.ExposedBy, int(exposureWindow/time.Minute))}
		}
		if len(g.Sends) >= maxSendsPerDay {
			return &refusalError{fmt.Sprintf("not sent: hey mcp sends at most %d messages a day on one sign-in, and this one has reached it. Send it from HEY", maxSendsPerDay)}
		}
		var charge []string
		for _, address := range newAddresses {
			if _, charged := g.NewAddresses[address]; !charged && !slices.Contains(charge, address) {
				charge = append(charge, address)
			}
		}
		if len(g.NewAddresses)+len(charge) > maxNewAddressesPerDay {
			return &refusalError{fmt.Sprintf("not sent: hey mcp sends to at most %d new addresses a day on one sign-in, and this draft would go past it. Send it from HEY", maxNewAddressesPerDay)}
		}

		d.Claims[key] = now.Unix()
		g.Sends = append(g.Sends, now.Unix())
		// Each new address counts once, and stays in the window for 24 hours after the
		// last send to it, so the window holds every new address sent to within it.
		for _, address := range newAddresses {
			g.NewAddresses[address] = now.Unix()
		}
		return nil
	})
}
