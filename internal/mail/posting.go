package mail

import (
	"strconv"
	"strings"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
)

// Posting is one row of a source: a thread, a bundle or a single entry as HEY lists it.
// The timestamps are the ones HEY served, so whatever shows one decides how it reads —
// formatting a time into a string and parsing it back is how a reader east of UTC ends
// up looking at yesterday's date.
type Posting struct {
	ID                    int64
	TopicID               int64
	CreatedAt             time.Time
	Name                  string
	Summary               string
	AlternativeSenderName string
	Seen                  bool
	BubbledUp             bool
	Muted                 bool
	VisibleEntryCount     int32
	Creator               Contact
	Extenzions            []Extenzion
	Folders               []Folder
	Collections           []Collection
}

// Contact is who a posting came from.
type Contact struct {
	ID           int64
	Name         string
	EmailAddress string
}

// Extenzion is the HEY extension — a group address — a posting arrived through.
type Extenzion struct {
	ID   int64
	Name string
}

// Folder is a label a posting is filed under.
type Folder struct {
	ID   int64
	Name string
}

// Collection is a collection a posting's topic belongs to.
type Collection struct {
	ID   int64
	Name string
}

// Postings describes a page of postings HEY answered with.
func Postings(postings []generated.Posting) []Posting {
	described := make([]Posting, 0, len(postings))
	for _, posting := range postings {
		described = append(described, NewPosting(posting))
	}
	return described
}

// NewPosting describes one posting HEY answered with.
func NewPosting(posting generated.Posting) Posting {
	return Posting{
		ID:                    posting.Id,
		TopicID:               topicIDIn(posting.AppUrl),
		CreatedAt:             posting.CreatedAt,
		Name:                  posting.Name,
		Summary:               posting.Summary,
		AlternativeSenderName: posting.AlternativeSenderName,
		Seen:                  posting.Seen,
		BubbledUp:             posting.BubbledUp,
		Muted:                 posting.Muted,
		VisibleEntryCount:     posting.VisibleEntryCount,
		Creator:               contactOf(posting.Creator),
		Extenzions:            extenzionsOf(posting.Extenzions),
		Folders:               foldersOf(posting.Folders),
		Collections:           collectionsOf(posting.Collections),
	}
}

func contactOf(contact generated.Contact) Contact {
	return Contact{ID: contact.Id, Name: contact.Name, EmailAddress: contact.EmailAddress}
}

func extenzionsOf(extenzions []generated.Extenzion) []Extenzion {
	if len(extenzions) == 0 {
		return nil
	}
	described := make([]Extenzion, len(extenzions))
	for i, extenzion := range extenzions {
		described[i] = Extenzion{ID: extenzion.Id, Name: extenzion.Name}
	}
	return described
}

func foldersOf(folders []generated.Folder) []Folder {
	if len(folders) == 0 {
		return nil
	}
	described := make([]Folder, len(folders))
	for i, folder := range folders {
		described[i] = Folder{ID: folder.Id, Name: folder.Name}
	}
	return described
}

func collectionsOf(collections []generated.Collection) []Collection {
	if len(collections) == 0 {
		return nil
	}
	described := make([]Collection, len(collections))
	for i, collection := range collections {
		described[i] = Collection{ID: collection.Id, Name: collection.Name}
	}
	return described
}

// topicIDIn reads the thread out of a posting's app_url, which is the only place HEY's
// posting JSON says which topic a posting is: `_posting.jbuilder` serves no topic and no
// topic_id, and the web app follows the URL. A posting that addresses something else has
// no topic — a bundle's app_url is its sender's contact page — and answers zero.
func topicIDIn(appURL string) int64 {
	marker := strings.LastIndex(appURL, "/topics/")
	if marker < 0 {
		return 0
	}
	segment := appURL[marker+len("/topics/"):]
	if end := strings.IndexAny(segment, "/?#"); end >= 0 {
		segment = segment[:end]
	}
	id, err := strconv.ParseInt(segment, 10, 64)
	if err != nil {
		return 0
	}
	return id
}
