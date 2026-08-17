package htmlutil

import "testing"

func TestParseSearchResultsHTML(t *testing.T) {
	source := `<!doctype html><html><body>
<section class="search__results-group">
  <article class="bulk-actions__container search-result posting">
    <time class="posting__time search-result__timestamp" datetime="2026-08-16T15:00:00Z">Today</time>
    <div class="search__content">
      <a href="/topics/42">
        <h3><span class="posting__title search-topic__title"><span class="u-for-screen-reader">Unread: </span>Quarterly planning<span class="u-for-screen-reader">, selected</span></span></h3>
        <div class="search-result__summary"><span>Amanda wrote</span> <span class="posting__summary">Please review the <mark>plan</mark>.</span></div>
      </a>
    </div>
  </article>
  <article class="search-result posting">
    <time class="search-result__timestamp" datetime="2026-08-15T14:30:00-05:00">Yesterday</time>
    <a href="https://app.hey.com/topics/77?source=search">
      <span class="search-topic__title">Project update</span>
      <div class="search-result__summary">Latest status</div>
    </a>
  </article>
  <article class="search-result"><a href="/contacts/12">Not a topic</a></article>
</section>
<a class="pagination-link" href="/advanced_search?q=planning&amp;page=2">More</a>
</body></html>`

	page := ParseSearchResultsHTML(source)
	if len(page.Results) != 2 {
		t.Fatalf("results = %#v, want 2", page.Results)
	}
	if page.NextPage != 2 {
		t.Errorf("next page = %d, want 2", page.NextPage)
	}

	first := page.Results[0]
	if first.ID != 42 {
		t.Errorf("ID = %d, want 42", first.ID)
	}
	if first.Subject != "Quarterly planning" {
		t.Errorf("subject = %q", first.Subject)
	}
	if first.Summary != "Amanda wrote Please review the plan." {
		t.Errorf("summary = %q", first.Summary)
	}
	if first.ActiveAt != "2026-08-16T15:00:00Z" {
		t.Errorf("active_at = %q", first.ActiveAt)
	}
	if first.AppURL != "/topics/42" {
		t.Errorf("app_url = %q", first.AppURL)
	}

	second := page.Results[1]
	if second.ID != 77 || second.Subject != "Project update" {
		t.Errorf("second result = %#v", second)
	}
}

func TestParseSearchResultsHTMLEmpty(t *testing.T) {
	page := ParseSearchResultsHTML(`<div class="search__results-group">No results</div>`)
	if len(page.Results) != 0 {
		t.Fatalf("results = %#v, want empty", page.Results)
	}
	if page.NextPage != 0 {
		t.Errorf("next page = %d, want 0", page.NextPage)
	}
}

func TestParseSearchResultsHTMLRejectsInvalidTopicIDAndPage(t *testing.T) {
	source := `<article class="search-result"><a href="/topics/not-a-number"><span class="search-topic__title">Bad</span></a></article>
<a class="pagination-link" href="/advanced_search?page=next">More</a>`
	page := ParseSearchResultsHTML(source)
	if len(page.Results) != 0 {
		t.Fatalf("results = %#v, want empty", page.Results)
	}
	if page.NextPage != 0 {
		t.Errorf("next page = %d, want 0", page.NextPage)
	}
}
