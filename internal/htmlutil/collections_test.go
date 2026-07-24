package htmlutil

import "testing"

func TestParseCollectionTopicsHTML(t *testing.T) {
	page := `
	<article class="card card--topic">
	  <a class="card__link" href="/topics/111">Flight confirmation</a>
	</article>
	<article class="card card--topic">
	  <a class="card__link" href="/topics/111">Flight confirmation</a>
	</article>
	<div class="entry">
	  <a title="Orsay Museum &amp; tickets" class="entry__collection-topic undecorated" data-action="x" href="/topics/222">Re: Orsay Museum &amp; tickets</a>
	</div>
	<div class="attachment">
	  <a aria-label="Show message" class="undecorated" href="/topics/111#__entry_9">receipt.pdf</a>
	</div>`

	got := ParseCollectionTopicsHTML(page)
	if len(got) != 2 {
		t.Fatalf("expected 2 topics, got %d: %+v", len(got), got)
	}
	if got[0].TopicID != 111 || got[0].Title != "Flight confirmation" {
		t.Errorf("topic[0] = %+v, want {111, Flight confirmation}", got[0])
	}
	// Title should come from the title attribute (unescaped), not the "Re:" inner text.
	if got[1].TopicID != 222 || got[1].Title != "Orsay Museum & tickets" {
		t.Errorf("topic[1] = %+v, want {222, Orsay Museum & tickets}", got[1])
	}
}

func TestParseCollectionNextPage(t *testing.T) {
	with := `<a class="pagination-link" data-pagination-target="nextPageLink" href="/collections/5?page=abc%3D%3D&amp;x=1">next</a>`
	if got := ParseCollectionNextPage(with); got != "/collections/5?page=abc%3D%3D&x=1" {
		t.Errorf("ParseCollectionNextPage = %q, want unescaped path", got)
	}

	without := `<div>no pagination here</div>`
	if got := ParseCollectionNextPage(without); got != "" {
		t.Errorf("ParseCollectionNextPage = %q, want empty", got)
	}
}
