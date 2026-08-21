package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/config"
)

// New-mail toasts, driven by the same bar tick as the unread indicator so the
// Imbox is fetched once per interval. `hey omarchy bar-status --notify` diffs
// the unseen postings against a fingerprint file and sends at most one toast
// per tick via omarchy-notification-send — sparse notices, never a firehose.
//
// The app-name matters: omarchy's default `omarchy-action` deliberately pops
// through Do Not Disturb, so the toast identifies as HEY and
// omarchy-toggle-notification-silencing is honored for free.

const omarchyNotifyAppName = "HEY"

// omarchyPollState fingerprints the unseen Imbox postings a previous tick saw.
// Seen maps posting id to its visible entry count, so a new reply on a known
// thread (count grew) toasts like a new thread does. ToastID is the daemon's
// id for our last toast; passing it back with -r replaces the on-screen toast
// instead of stacking a new one each tick. Identity records which server and
// account the fingerprints belong to: after `hey accounts use` or a base URL
// change the state reseeds silently instead of toasting the other account's
// backlog.
type omarchyPollState struct {
	Identity string           `json:"identity,omitempty"`
	Seen     map[string]int32 `json:"seen"`
	ToastID  int              `json:"toast_id,omitempty"`
	ToastAt  int64            `json:"toast_at,omitempty"` // unix seconds the toast was sent
}

// toastReplaceWindow bounds how long a cached toast id is reused. Notification
// ids are daemon-local, not stable identities: after a reboot or a shell
// restart the same number may belong to another application's notification,
// and -r would overwrite that instead of replacing ours. Replacement only ever
// matters for back-to-back ticks, so a short window loses nothing.
const toastReplaceWindow = 10 * time.Minute

// replaceableToastID returns the cached toast id when it is recent enough to
// trust, and 0 otherwise.
func (s omarchyPollState) replaceableToastID(now time.Time) int {
	if s.ToastID <= 0 || now.Sub(time.Unix(s.ToastAt, 0)) > toastReplaceWindow {
		return 0
	}
	return s.ToastID
}

// omarchyPollIdentity names who the poll runs as: the server (spelled the way
// auth.Manager keys credentials, without a trailing slash), the account
// filter, and the signed-in user's id from the identity endpoint. Keying on
// the user rather than on credentials is what makes every way of becoming
// someone else — login, logout, HEY_TOKEN set, changed or unset — reseed
// silently on the next tick, while token refreshes and rotations change
// nothing. When the identity cannot be fetched the tick skips notifying and
// leaves the fingerprints untouched, exactly as a failed Imbox fetch does.
func omarchyPollIdentity(ctx context.Context) (string, bool) {
	identity, err := rootSDK.Identity().GetIdentity(ctx)
	if err != nil || identity == nil || identity.Id == 0 {
		return "", false
	}
	return pollIdentity(cfg.BaseURL, cfg.AccountID, strconv.FormatInt(identity.Id, 10)), true
}

func pollIdentity(baseURL, account, userID string) string {
	identity := strings.TrimRight(baseURL, "/") + " " + account
	if userID == "" {
		return identity
	}
	return identity + " user:" + userID
}

func omarchyPollStatePath() string {
	return filepath.Join(config.StateDir(), "omarchy-poll.json")
}

func omarchyPollLockPath() string {
	return omarchyPollStatePath() + ".lock"
}

// omarchyPollStateUsable reports whether the state directory resolves to an
// absolute path. config.StateDir is empty without HOME and relative with a
// relative XDG_STATE_HOME; either would put the fingerprints — and their
// deletion — in the working directory, next to whatever file happens to share
// the name.
func omarchyPollStateUsable() bool {
	return filepath.IsAbs(config.StateDir())
}

var errPollStateDir = errors.New("no absolute state directory: set HOME or XDG_STATE_HOME")

// removeOmarchyPollState forgets the fingerprints. A poll that is not
// notifying calls this on every run, so turning toasts on — by any route: hey
// setup omarchy --notify, the plugin's own toggle, omarchy bar set — always
// starts from a silent seed instead of toasting whatever accumulated while
// they were off. It takes the poll lock, so a notifying poll on another
// monitor that is mid-diff finishes writing before the file goes, and it
// leaves the lock sidecar alone: unlinking an inode another poller holds would
// let the next poll lock a fresh one and the two diff at once.
func removeOmarchyPollState() error {
	if !omarchyPollStateUsable() {
		return errPollStateDir
	}
	var err error
	withOmarchyPollLock(func() { _, err = removeFileIfPresent(omarchyPollStatePath()) })
	return err
}

// loadOmarchyPollState returns the saved state and whether a state file
// existed. No file means first run: seed the fingerprints, toast nothing —
// never toast the backlog.
func loadOmarchyPollState() (omarchyPollState, bool) {
	state := omarchyPollState{Seen: map[string]int32{}}
	data, err := os.ReadFile(omarchyPollStatePath())
	if err != nil {
		return state, false
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return omarchyPollState{Seen: map[string]int32{}}, false
	}
	if state.Seen == nil {
		state.Seen = map[string]int32{}
	}
	return state, true
}

func saveOmarchyPollState(state omarchyPollState) error {
	if !omarchyPollStateUsable() {
		return errPollStateDir
	}
	return writeJSONFile(omarchyPollStatePath(), state)
}

// notifyNewMail diffs the unseen postings against the fingerprint file and
// sends at most one toast. Errors are swallowed: the bar tick must never turn
// into an error message, and a failed send retries on the next tick because
// the undelivered postings keep their previous fingerprints. complete reports
// whether unseen is the whole unseen Imbox: HEY sorts unseen postings first,
// so a page with any seen posting (or none at all) proves completeness, while
// an all-unseen page may be truncated and then pruning must wait — a thread
// pushed off the page would otherwise toast again when it comes back.
func notifyNewMail(env omarchyEnv, identity string, unseen []generated.Posting, complete bool) {
	previous, existed := loadOmarchyPollState()
	if previous.Identity != identity {
		// Another server or account's fingerprints: reseed silently.
		previous, existed = omarchyPollState{Seen: map[string]int32{}}, false
	}

	next := omarchyPollState{Identity: identity, Seen: make(map[string]int32, len(unseen)), ToastID: previous.ToastID, ToastAt: previous.ToastAt}
	var fresh []generated.Posting
	for _, posting := range unseen {
		id := strconv.FormatInt(posting.Id, 10)
		next.Seen[id] = posting.VisibleEntryCount
		known, seenBefore := previous.Seen[id]
		if posting.Muted || (seenBefore && posting.VisibleEntryCount <= known) {
			continue
		}
		fresh = append(fresh, posting)
	}
	if !complete {
		for id, count := range previous.Seen {
			if _, onPage := next.Seen[id]; !onPage {
				next.Seen[id] = count
			}
		}
	}

	if !existed {
		// A seed must be the whole unseen set: persisting a snapshot that lost a
		// page would leave every thread beyond it unknown, to be toasted as
		// backlog the moment it surfaces. Leave no state and seed next tick.
		if complete {
			_ = saveOmarchyPollState(next)
		}
		return
	}
	if len(fresh) == 0 {
		_ = saveOmarchyPollState(next)
		return
	}
	// Persist before delivering: a toast whose fingerprints could not be saved
	// would come back every tick, so an unsaveable state means no toast at all.
	if err := saveOmarchyPollState(next); err != nil {
		return
	}
	now := time.Now()
	if id, err := sendMailToast(env, fresh, previous.replaceableToastID(now)); err == nil {
		next.ToastID, next.ToastAt = id, now.Unix()
	} else {
		// Undelivered: restore the fresh postings' previous fingerprints so
		// they still diff as new next tick.
		for _, posting := range fresh {
			id := strconv.FormatInt(posting.Id, 10)
			if known, ok := previous.Seen[id]; ok {
				next.Seen[id] = known
			} else {
				delete(next.Seen, id)
			}
		}
	}
	_ = saveOmarchyPollState(next)
}

func sendMailToast(env omarchyEnv, fresh []generated.Posting, replaceID int) (int, error) {
	headline, description := composeMailToast(fresh)
	args := []string{
		"--glyph", omarchyBarGlyph,
		"--app-name", omarchyNotifyAppName,
		"-u", "low",
		"--exec", omarchyFocusCommand,
		notificationText(headline),
	}
	if description != "" {
		args = append(args, notificationText(description))
	}
	if replaceID > 0 {
		args = append(args, "-r", strconv.Itoa(replaceID))
	}
	args = append(args, "-p")
	out, err := env.runOutput("omarchy-notification-send", args...)
	if err != nil {
		return replaceID, err
	}
	if id, err := strconv.Atoi(strings.TrimSpace(out)); err == nil && id > 0 {
		return id, nil
	}
	return replaceID, nil
}

// notificationText keeps mail-derived text from being read as an option:
// omarchy-notification-send and notify-send both parse a leading dash, and a
// subject or summary can start with one. A word joiner is invisible on screen
// but makes the argument a plain positional.
func notificationText(text string) string {
	if strings.HasPrefix(text, "-") {
		return "\u2060" + text
	}
	return text
}

// composeMailToast turns the fresh postings into one headline and description:
// `Sender — Subject` for a single thread, a count with the first few senders
// for more.
func composeMailToast(fresh []generated.Posting) (string, string) {
	if len(fresh) == 1 {
		posting := fresh[0]
		description := posting.Summary
		if description == postingSubject(posting) {
			description = "" // Summary already stood in for a missing subject
		}
		return postingSender(posting) + " — " + postingSubject(posting), description
	}
	senders := make([]string, 0, 3)
	for _, posting := range fresh {
		if len(senders) == 3 {
			senders = append(senders, "…")
			break
		}
		senders = append(senders, postingSender(posting))
	}
	return fmt.Sprintf("%d new in Imbox", len(fresh)), strings.Join(senders, ", ")
}

func postingSender(posting generated.Posting) string {
	switch {
	case posting.AlternativeSenderName != "":
		return posting.AlternativeSenderName
	case posting.Creator.Name != "":
		return posting.Creator.Name
	default:
		return posting.Creator.EmailAddress
	}
}

func postingSubject(posting generated.Posting) string {
	if posting.Name != "" {
		return posting.Name
	}
	return posting.Summary
}
