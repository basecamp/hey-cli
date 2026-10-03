package cmd

import (
	"context"
	"strings"

	"github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/terminal"
)

// boxNames is what hey box answers an unknown box with: the one short spelling of each box
// HEY has. Every command that takes a box also takes its kind and its display name, but
// not every command takes every box: see moveBoxNames and searchInValues.
const boxNames = "imbox, feed, papertrail, setaside, replylater, or bubbleup"

// moveBoxNames is boxNames less Bubble Up, which is not a move destination.
const moveBoxNames = "imbox, feed, papertrail, setaside, or replylater"

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

// resolveSearchIn turns any spelling of a box search can narrow to into the value HEY
// reads. A spelling boxKindFor does not know is looked up in the box list, by the name or
// kind HEY serves, the way hey box and hey move find a box: a renamed Imbox is still the
// Imbox. That list is read only for such a name, and a box found there is searched only
// if its kind is one search can narrow to.
func resolveSearchIn(ctx context.Context, name string) (string, error) {
	kind := boxKindFor(name)
	if kind == searchTrash {
		return searchTrash, nil
	}
	if value, ok := searchInValues[kind]; ok {
		return value, nil
	}
	switch kind {
	case hey.BoxKindSetAside, hey.BoxKindLater, hey.BoxKindBubbleUp:
		return "", errSearchIn(searchInHint)
	}

	boxes, err := sdk.Boxes().List(ctx)
	if err != nil {
		return "", apierr.FromSDK(err)
	}
	if boxes != nil {
		for _, box := range *boxes {
			if boxKindFor(box.Kind) != kind && boxKindFor(box.Name) != kind {
				continue
			}
			if value, ok := searchInValues[boxKindFor(box.Kind)]; ok {
				return value, nil
			}
			return "", errSearchIn(terminal.SanitizeLine(box.Name) + " is a box search cannot narrow to. " + searchInHint)
		}
	}
	return "", errSearchIn(searchInHint)
}

const searchInHint = "A box's kind or name works too, such as feedbox, \"Paper Trail\" or a name you gave one of those boxes. Search cannot narrow to Set Aside, Reply Later or Bubble Up."

func errSearchIn(hint string) *apierr.Error {
	return apierr.ErrUsageHint("--in must be imbox, feed, papertrail, or trash", hint)
}

// errBoxNotFound names the spellings a box can be given in, since a script that picked
// one up from another command's help only finds out it is wrong here.
func errBoxNotFound(name string) *apierr.Error {
	if boxKindFor(name) == searchTrash {
		return apierr.ErrNotFoundHint("box", name, "Trash is not a box. Search it with: hey search --in trash")
	}
	return apierr.ErrNotFoundHint("box", name, "Use "+boxNames+", a kind or name from hey box list, or a box ID")
}

// errMoveDestinationNotFound is errBoxNotFound for hey move --to: it names only the boxes a
// thread can be moved to, and sends Trash to the command that moves threads there.
func errMoveDestinationNotFound(name string) *apierr.Error {
	if boxKindFor(name) == searchTrash {
		return apierr.ErrNotFoundHint("box", name, "Trash is not a box. Move threads there with: hey trash <box-item-id>...")
	}
	return apierr.ErrNotFoundHint("box", name, "Use "+moveBoxNames+", a kind or name from hey box list, or a box ID. Bubble Up is not a destination: use hey bubble up")
}
