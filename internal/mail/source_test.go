package mail

import (
	"testing"
	"time"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

func TestBoxSourceIdentifiesItselfByHEYsKind(t *testing.T) {
	source := BoxSource(&generated.BoxShowResponse{Id: 3, Kind: hey.BoxKindImbox, Name: "Imbox", AppUrl: "/imbox"})
	if source.Kind != KindBox || source.ID != 3 || source.BoxKind != hey.BoxKindImbox || source.AppURL != "/imbox" {
		t.Errorf("source = %+v", source)
	}
	if !source.Coverable() {
		t.Error("the Imbox is coverable")
	}
}

// A cover belongs over the Imbox and nothing else, and the question is asked of the kind
// HEY served rather than of a name anyone can change.
func TestOnlyTheImboxIsCoverable(t *testing.T) {
	for _, kind := range []string{hey.BoxKindFeed, hey.BoxKindTrail, hey.BoxKindSetAside, hey.BoxKindLater, hey.BoxKindBubbleUp} {
		if BoxSource(&generated.BoxShowResponse{Kind: kind}).Coverable() {
			t.Errorf("box kind %q reported itself coverable", kind)
		}
	}
	if BoxSource(&generated.BoxShowResponse{Name: "Imbox"}).Coverable() {
		t.Error("a box named Imbox but of another kind reported itself coverable")
	}
	if (Source{Kind: KindFolder, Name: "Imbox", BoxKind: hey.BoxKindImbox}).Coverable() {
		t.Error("a label reported itself coverable")
	}
}

func TestFolderAndCollectionSourcesCarryTheirTimestamps(t *testing.T) {
	created := time.Date(2026, 3, 4, 9, 30, 0, 0, time.FixedZone("CET", 3600))
	updated := created.Add(48 * time.Hour)

	folder := FolderSource(&generated.FolderWithPostings{Id: 12, Name: "Receipts", AppUrl: "/folders/12", CreatedAt: created, UpdatedAt: updated})
	if folder.Kind != KindFolder || folder.ID != 12 || folder.Name != "Receipts" || !folder.CreatedAt.Equal(created) || !folder.UpdatedAt.Equal(updated) {
		t.Errorf("folder source = %+v", folder)
	}
	if folder.CreatedAt.Format(time.RFC3339) != created.Format(time.RFC3339) {
		t.Errorf("folder source moved the zone: %s, want %s", folder.CreatedAt, created)
	}
	if folder.BoxKind != "" {
		t.Errorf("folder source claimed box kind %q", folder.BoxKind)
	}

	collection := CollectionSource(&generated.CollectionWithPostings{Id: 56, Name: "Kitchen remodel", CreatedAt: created})
	if collection.Kind != KindCollection || collection.ID != 56 || collection.Name != "Kitchen remodel" || !collection.CreatedAt.Equal(created) {
		t.Errorf("collection source = %+v", collection)
	}
}
