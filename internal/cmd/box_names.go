package cmd

import (
	"strings"

	"github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
)

// boxNames is what an unknown box is answered with: the one short spelling of each box
// HEY has. Every command that takes a box also takes its kind and its display name.
const boxNames = "imbox, feed, papertrail, setaside, replylater, or bubbleup"

// boxKindFor answers the kind HEY serves for a box, however it was spelled: a short name
// (feed, papertrail), the kind itself (feedbox, trailbox) or the display name (The Feed,
// Paper Trail), in any case, with spaces, hyphens and underscores ignored. A spelling it
// does not know comes back in the same folded form, so a box HEY lists under a kind or a
// name this does not know can still be matched against boxKindFor of what it lists.
func boxKindFor(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.NewReplacer(" ", "", "-", "", "_", "").Replace(name)

	switch name {
	case "feed", "thefeed":
		return hey.BoxKindFeed
	case "aside", "setaside":
		return hey.BoxKindSetAside
	case "later", "replylater":
		return hey.BoxKindLater
	case "trail", "papertrail":
		return hey.BoxKindTrail
	case "bubble", "bubbleup", "bubbled", "bubbledup":
		return hey.BoxKindBubbleUp
	}
	return name
}

// searchInValues is what HEY's advanced search reads for each box it can narrow to
// (Search::AdvancedQuery#box_id): the short name, not the kind.
var searchInValues = map[string]string{
	hey.BoxKindImbox: "imbox",
	hey.BoxKindFeed:  "feed",
	hey.BoxKindTrail: "papertrail",
}

// searchTrash is the one --in value that is not a box: HEY keeps trashed postings out of
// every box and searches them on their own.
const searchTrash = "trash"

// searchInValue turns any spelling of a box search can narrow to into the value HEY reads.
func searchInValue(name string) (string, error) {
	kind := boxKindFor(name)
	if kind == searchTrash {
		return searchTrash, nil
	}
	if value, ok := searchInValues[kind]; ok {
		return value, nil
	}
	return "", apierr.ErrUsageHint(
		"--in must be imbox, feed, papertrail, or trash",
		"A box's kind or name works too, such as feedbox or \"Paper Trail\". Search cannot narrow to Set Aside, Reply Later or Bubble Up.",
	)
}

// errBoxNotFound names the spellings a box can be given in, since a script that picked
// one up from another command's help only finds out it is wrong here.
func errBoxNotFound(name string) *apierr.Error {
	if boxKindFor(name) == searchTrash {
		return apierr.ErrNotFoundHint("box", name, "Trash is not a box. Search it with: hey search --in trash")
	}
	return apierr.ErrNotFoundHint("box", name, "Use "+boxNames+", a kind or name from hey box list, or a box ID")
}
