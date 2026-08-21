// Package mail reads the places HEY keeps postings — a box, a label, a collection —
// through one Source type and one page read, so a caller never has to know which
// endpoint a source is served by.
package mail

import (
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

// Kind is which of HEY's posting endpoints a Source is served by.
type Kind string

const (
	KindBox        Kind = "box"
	KindFolder     Kind = "folder"
	KindCollection Kind = "collection"
)

// Source is a place postings are read from. BoxKind carries HEY's own kind for a box
// (hey.BoxKindImbox and friends), which is what tells the named box routes apart; it is
// empty for a label or a collection.
type Source struct {
	Kind      Kind
	ID        int64
	Name      string
	BoxKind   string
	AppURL    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Coverable reports whether cover art belongs over this source's seen postings. Only the
// Imbox is coverable, which is haystack's rule in Box::Imbox#coverable?.
func (s Source) Coverable() bool {
	return s.Kind == KindBox && s.BoxKind == hey.BoxKindImbox
}

// ListedBoxSource describes a box from HEY's list of them, which names the boxes without
// serving any of their postings or URLs.
func ListedBoxSource(box generated.Box) Source {
	return Source{
		Kind:    KindBox,
		ID:      box.Id,
		Name:    box.Name,
		BoxKind: box.Kind,
		AppURL:  box.AppUrl,
	}
}

// BoxSource describes the box HEY answered with.
func BoxSource(box *generated.BoxShowResponse) Source {
	return Source{
		Kind:    KindBox,
		ID:      box.Id,
		Name:    box.Name,
		BoxKind: box.Kind,
		AppURL:  box.AppUrl,
	}
}

// FolderSource describes the label HEY answered with.
func FolderSource(folder *generated.FolderWithPostings) Source {
	return Source{
		Kind:      KindFolder,
		ID:        folder.Id,
		Name:      folder.Name,
		AppURL:    folder.AppUrl,
		CreatedAt: folder.CreatedAt,
		UpdatedAt: folder.UpdatedAt,
	}
}

// CollectionSource describes the collection HEY answered with.
func CollectionSource(collection *generated.CollectionWithPostings) Source {
	return Source{
		Kind:      KindCollection,
		ID:        collection.Id,
		Name:      collection.Name,
		AppURL:    collection.AppUrl,
		CreatedAt: collection.CreatedAt,
		UpdatedAt: collection.UpdatedAt,
	}
}
