package cmd

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
)

// hey watch --notify turns each batch of changes into at most one desktop
// notification. There is no state file: the first read of a box is its backlog
// and is never toasted, and what the notifier remembers — when each thread was
// last active, the id of its last toast — lives and dies with the watch.
//
// The toast goes through notify-send under the app-name HEY. The name matters
// on Omarchy: its own `omarchy-action` deliberately pops through Do Not Disturb,
// so identifying as HEY is what lets SUPER+CTRL+comma silence the toasts like
// any other app's. When Omarchy is present the toast also carries the shell's
// hints — a glyph for the notification, and an exec that focuses the TUI on
// click — which every other daemon ignores.

const (
	notifyAppName     = "HEY"
	notifySendTimeout = 10 * time.Second
	// toastReplaceWindow bounds how long a toast id is reused. Notification ids
	// are daemon-local, not stable identities: after a shell restart the same
	// number may belong to another application's notification, and -r would
	// overwrite that instead of replacing ours. Replacement only matters for
	// back-to-back batches, so a short window loses nothing.
	toastReplaceWindow = 10 * time.Minute
)

// desktopNotifier sends one notification per batch of watched changes.
type desktopNotifier struct {
	errOut   io.Writer
	send     func(args ...string) (string, error)
	omarchy  bool
	now      func() time.Time
	started  time.Time
	seeded   map[int64]bool
	activeAt map[int64]time.Time
	toastID  int
	toastAt  time.Time
}

// newDesktopNotifier finds notify-send, or says on stderr why the watch will
// run without notifications and returns nil.
func newDesktopNotifier(errOut io.Writer) *desktopNotifier {
	if _, err := exec.LookPath("notify-send"); err != nil {
		fmt.Fprintln(errOut, "notice: --notify needs notify-send (libnotify); watching without notifications")
		return nil
	}

	_, omarchyErr := exec.LookPath("omarchy-launch-or-focus-tui")

	return &desktopNotifier{
		errOut:   errOut,
		send:     runNotifySend,
		omarchy:  omarchyErr == nil,
		now:      time.Now,
		started:  time.Now(),
		seeded:   map[int64]bool{},
		activeAt: map[int64]time.Time{},
	}
}

func runNotifySend(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), notifySendTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "notify-send", args...).Output() //nolint:gosec // G204: a fixed command; mail text travels as positional arguments
	return string(out), err
}

// batch toasts the new mail among one read of a box's changes feed. It never
// fails the watch: a toast that could not be sent is a warning, and the next
// batch toasts again.
func (n *desktopNotifier) batch(box *watchedBox, added, updated []generated.Posting) {
	fresh := n.fresh(added, updated)

	// A box's first read is its catch-up: the server's cursor is the box's last
	// activity, not this moment, and --since moves it further back. What it
	// carries is the backlog — recorded, so later updates measure against it,
	// but never toasted.
	if !n.seeded[box.id] {
		n.seeded[box.id] = true
		return
	}
	if len(fresh) == 0 {
		return
	}

	headline, description := composeMailToast(box.name, fresh)
	out, err := n.send(n.arguments(headline, description)...)
	if err != nil {
		fmt.Fprintf(n.errOut, "warning: could not send a notification: %v\n", err)
		return
	}

	// -p prints the daemon's id for the toast, which is what -r replaces next time.
	if id, err := strconv.Atoi(strings.TrimSpace(out)); err == nil && id > 0 {
		n.toastID, n.toastAt = id, n.now()
	}
}

// fresh picks the postings worth a toast: unseen, not muted, and new — added
// outright, or updated with activity since this watch last saw the thread.
// active_at moves on new mail only, not when a thread is read, muted or moved,
// so a seen flip or a move into the box never toasts; a thread the watch has no
// record of is measured against its start, since anything active before that
// was already there. Every posting's activity is recorded, toasted or not.
func (n *desktopNotifier) fresh(added, updated []generated.Posting) []generated.Posting {
	var fresh []generated.Posting

	for _, posting := range added {
		n.activeAt[posting.Id] = posting.ActiveAt
		if wantsToast(posting) {
			fresh = append(fresh, posting)
		}
	}

	for _, posting := range updated {
		last, known := n.activeAt[posting.Id]
		if !known {
			last = n.started
		}
		n.activeAt[posting.Id] = posting.ActiveAt
		if wantsToast(posting) && posting.ActiveAt.After(last) {
			fresh = append(fresh, posting)
		}
	}

	return fresh
}

func wantsToast(posting generated.Posting) bool {
	return !posting.Seen && !posting.Muted
}

func (n *desktopNotifier) arguments(headline, description string) []string {
	args := []string{"-a", notifyAppName, "-u", "low", "-p"}
	if id := n.replaceableToastID(); id > 0 {
		args = append(args, "-r", strconv.Itoa(id))
	}
	if n.omarchy {
		args = append(args,
			"-h", "string:omarchy-glyph:"+omarchyBarGlyph,
			"-h", "string:omarchy-exec:"+omarchyFocusCommand)
	}

	args = append(args, notificationText(headline))
	if description != "" {
		args = append(args, notificationText(description))
	}

	return args
}

// replaceableToastID is the last toast's id while it is recent enough to trust,
// and 0 otherwise.
func (n *desktopNotifier) replaceableToastID() int {
	if n.toastID <= 0 || n.now().Sub(n.toastAt) > toastReplaceWindow {
		return 0
	}

	return n.toastID
}

// notificationText keeps mail-derived text from being read as an option:
// notify-send parses a leading dash wherever it appears, and a subject or
// summary can start with one. A word joiner is invisible on screen but makes
// the argument a plain positional.
func notificationText(text string) string {
	if strings.HasPrefix(text, "-") {
		return "\u2060" + text
	}

	return text
}

// composeMailToast turns the fresh postings into one headline and description:
// `Sender — Subject` for a single thread, a count with the first few senders
// for more.
func composeMailToast(boxName string, fresh []generated.Posting) (string, string) {
	if len(fresh) == 1 {
		posting := fresh[0]
		description := posting.Summary
		if description == postingSubject(posting) {
			description = "" // Summary already stood in for a missing subject
		}
		return postingSender(posting) + " — " + postingSubject(posting), description
	}

	senders := make([]string, 0, 4)
	for _, posting := range fresh {
		if len(senders) == 3 {
			senders = append(senders, "…")
			break
		}
		senders = append(senders, postingSender(posting))
	}

	return fmt.Sprintf("%d new in %s", len(fresh), boxName), strings.Join(senders, ", ")
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
