package htmlutil

import (
	"strings"
	"testing"
)

func TestToTextPlain(t *testing.T) {
	got := ToText("hello world")
	if got != "hello world" {
		t.Errorf("ToText plain = %q, want %q", got, "hello world")
	}
}

func TestToTextParagraphs(t *testing.T) {
	got := ToText("<p>First</p><p>Second</p>")
	if !strings.Contains(got, "First") || !strings.Contains(got, "Second") {
		t.Errorf("ToText paragraphs = %q, should contain First and Second", got)
	}
}

func TestToTextBr(t *testing.T) {
	got := ToText("line1<br>line2")
	if !strings.Contains(got, "line1\nline2") {
		t.Errorf("ToText br = %q, should contain newline between lines", got)
	}
}

func TestToTextList(t *testing.T) {
	got := ToText("<ul><li>one</li><li>two</li></ul>")
	if !strings.Contains(got, "• one") {
		t.Errorf("ToText list = %q, should contain bullet items", got)
	}
	if !strings.Contains(got, "• two") {
		t.Errorf("ToText list = %q, should contain second bullet", got)
	}
}

func TestToTextStripsEntities(t *testing.T) {
	got := ToText("&amp; &lt; &gt;")
	if !strings.Contains(got, "& < >") {
		t.Errorf("ToText entities = %q, should decode HTML entities", got)
	}
}

func TestToTextStripsScript(t *testing.T) {
	got := ToText("<p>hello</p><script>alert('xss')</script>")
	if strings.Contains(got, "alert") {
		t.Errorf("ToText should strip script content, got %q", got)
	}
}

func TestToTextEmpty(t *testing.T) {
	got := ToText("")
	if got != "" {
		t.Errorf("ToText empty = %q, want empty", got)
	}
}

func TestToTextImgTag(t *testing.T) {
	got := ToText(`<p>Before</p><img src="test.png" alt="photo"><p>After</p>`)
	if !strings.Contains(got, "[photo]") {
		t.Errorf("ToText should render img alt text, got %q", got)
	}
	if !strings.Contains(got, "Before") || !strings.Contains(got, "After") {
		t.Errorf("ToText should include surrounding text, got %q", got)
	}
}

func TestToTextImgNoAlt(t *testing.T) {
	got := ToText(`<img src="test.png">`)
	if !strings.Contains(got, "[image]") {
		t.Errorf("ToText should show [image] for img without alt, got %q", got)
	}
}

func TestToTextActionTextAttachment(t *testing.T) {
	got := ToText(`<p>Text</p><action-text-attachment filename="photo.png"><img src="url"></action-text-attachment><p>More</p>`)
	if !strings.Contains(got, "[photo.png]") {
		t.Errorf("ToText should show filename for action-text-attachment, got %q", got)
	}
	if !strings.Contains(got, "Text") || !strings.Contains(got, "More") {
		t.Errorf("ToText should include surrounding text, got %q", got)
	}
	if strings.Contains(got, "[image]") {
		t.Errorf("ToText should skip inner content of action-text-attachment, got %q", got)
	}
}

func TestToTextTrixFigure(t *testing.T) {
	got := ToText(`<p>Before</p><figure data-trix-attachment='{"filename":"photo.png","url":"/img.png","contentType":"image/png"}'></figure><p>After</p>`)
	if !strings.Contains(got, "[photo.png]") {
		t.Errorf("ToText should show filename for trix figure, got %q", got)
	}
	if !strings.Contains(got, "Before") || !strings.Contains(got, "After") {
		t.Errorf("ToText should include surrounding text, got %q", got)
	}
}

func TestPrependText(t *testing.T) {
	got := PrependText(`<div>Forwarded message</div>`, "For your review\nThanks & take care")
	want := `<div>For your review<br>Thanks &amp; take care</div><br><div>Forwarded message</div>`
	if got != want {
		t.Errorf("PrependText() = %q, want %q", got, want)
	}
}

func TestPrependTextWithoutNote(t *testing.T) {
	content := `<div>Forwarded message</div>`
	if got := PrependText(content, "  "); got != content {
		t.Errorf("PrependText() = %q, want unchanged content", got)
	}
}

func TestExtractAttachments(t *testing.T) {
	h := `<action-text-attachment sgid="sgid-1" url="/rails/blobs/report.pdf" filename="quarterly-report.pdf" content-type="application/pdf" filesize="128"></action-text-attachment>
<figure data-trix-attachment='{"sgid":"sgid-2","url":"/rails/blobs/photo.png","filename":"photo.png","contentType":"image/png","filesize":256}'></figure>`
	attachments := ExtractAttachments(h)
	if len(attachments) != 2 {
		t.Fatalf("ExtractAttachments got %d attachments, want 2", len(attachments))
	}
	if attachments[0].Filename != "quarterly-report.pdf" || attachments[0].ContentType != "application/pdf" || attachments[0].ByteSize == nil || *attachments[0].ByteSize != 128 || attachments[0].SGID != "sgid-1" {
		t.Errorf("canonical attachment = %+v", attachments[0])
	}
	if attachments[1].Filename != "photo.png" || attachments[1].URL != "/rails/blobs/photo.png" || attachments[1].ByteSize == nil || *attachments[1].ByteSize != 256 || attachments[1].SGID != "sgid-2" {
		t.Errorf("Trix attachment = %+v", attachments[1])
	}
}

func TestExtractAttachmentsDistinguishesEmptyFromUnknownSize(t *testing.T) {
	h := `<action-text-attachment url="/rails/blobs/empty.txt" filename="empty.txt" filesize="0"></action-text-attachment>
<figure data-trix-attachment='{"url":"/rails/blobs/unknown.txt","filename":"unknown.txt"}'></figure>`
	attachments := ExtractAttachments(h)
	if len(attachments) != 2 {
		t.Fatalf("ExtractAttachments got %d attachments, want 2", len(attachments))
	}
	if attachments[0].ByteSize == nil || *attachments[0].ByteSize != 0 {
		t.Errorf("empty attachment size = %v", attachments[0].ByteSize)
	}
	if attachments[1].ByteSize != nil {
		t.Errorf("unknown attachment size = %v", *attachments[1].ByteSize)
	}
}

func TestExtractAttachmentsSkipsIncompleteElements(t *testing.T) {
	h := `<action-text-attachment sgid="sgid-1" filename="missing-url.pdf"></action-text-attachment>
<figure data-trix-attachment='{"url":"/rails/blobs/missing-name"}'></figure>`
	if attachments := ExtractAttachments(h); len(attachments) != 0 {
		t.Errorf("ExtractAttachments = %+v, want none", attachments)
	}
}

func TestExtractImageURLs(t *testing.T) {
	h := `<p>Hello</p><img src="https://example.com/a.png"><img src="https://example.com/b.jpg">`
	urls := ExtractImageURLs(h)
	if len(urls) != 2 {
		t.Fatalf("ExtractImageURLs got %d urls, want 2", len(urls))
	}
	if urls[0] != "https://example.com/a.png" {
		t.Errorf("url[0] = %q, want %q", urls[0], "https://example.com/a.png")
	}
	if urls[1] != "https://example.com/b.jpg" {
		t.Errorf("url[1] = %q, want %q", urls[1], "https://example.com/b.jpg")
	}
}

func TestExtractImageURLsNone(t *testing.T) {
	urls := ExtractImageURLs("<p>No images here</p>")
	if len(urls) != 0 {
		t.Errorf("ExtractImageURLs got %d urls, want 0", len(urls))
	}
}

func TestExtractImageURLsEmptySrc(t *testing.T) {
	urls := ExtractImageURLs(`<img src="">`)
	if len(urls) != 0 {
		t.Errorf("ExtractImageURLs should skip empty src, got %d urls", len(urls))
	}
}

func TestExtractImageURLsActionTextImage(t *testing.T) {
	h := `<action-text-attachment url="/rails/blobs/photo.png" filename="photo.png" content-type="image/png"></action-text-attachment>
<action-text-attachment url="https://gopher.hey.com/signed/photo.jpg" content-type="image"></action-text-attachment>
<action-text-attachment url="/rails/blobs/report.pdf" filename="report.pdf" content-type="application/pdf"></action-text-attachment>`
	urls := ExtractImageURLs(h)
	if len(urls) != 2 {
		t.Fatalf("ExtractImageURLs Action Text got %d urls, want 2", len(urls))
	}
	if urls[0] != "/rails/blobs/photo.png" {
		t.Errorf("url[0] = %q, want %q", urls[0], "/rails/blobs/photo.png")
	}
	if urls[1] != "https://gopher.hey.com/signed/photo.jpg" {
		t.Errorf("url[1] = %q, want Gopher image", urls[1])
	}
}

func TestExtractImageURLsTrixFigure(t *testing.T) {
	h := `<figure data-trix-attachment='{"url":"/rails/blobs/abc/image.png","filename":"image.png","contentType":"image/png"}'></figure>
<figure data-trix-attachment='{"url":"/rails/blobs/abc/report.pdf","filename":"report.pdf","contentType":"application/pdf"}'></figure>`
	urls := ExtractImageURLs(h)
	if len(urls) != 1 {
		t.Fatalf("ExtractImageURLs trix got %d urls, want 1", len(urls))
	}
	if urls[0] != "/rails/blobs/abc/image.png" {
		t.Errorf("url[0] = %q, want %q", urls[0], "/rails/blobs/abc/image.png")
	}
}

func TestToTextRendersInlineHTMLTrixAttachments(t *testing.T) {
	// HEY wraps pasted rich HTML in text/html trix attachments: the markup
	// sits inside the JSON attribute, and the figure element has no children.
	content := `<figure data-trix-attachment="{&quot;contentType&quot;:&quot;text/html&quot;,&quot;content&quot;:&quot;<shadow-content><template><p>Please join us for the parent retreat on Saturday.</p></template></shadow-content>&quot;,&quot;data&quot;:&quot;{}&quot;}"></figure>` +
		`<figure data-trix-attachment="{&quot;contentType&quot;:&quot;text/html&quot;,&quot;content&quot;:&quot;<shadow-content><template><p>RSVP to maria.gonzalez@example.org by Friday.</p></template></shadow-content>&quot;,&quot;data&quot;:&quot;{}&quot;}"></figure>`

	got := ToText(content)
	if !strings.Contains(got, "Please join us for the parent retreat on Saturday.") {
		t.Errorf("ToText dropped the first inline HTML segment: %q", got)
	}
	if !strings.Contains(got, "RSVP to maria.gonzalez@example.org by Friday.") {
		t.Errorf("ToText dropped the second inline HTML segment: %q", got)
	}
}

func TestToTextKeepsFileAttachmentPlaceholders(t *testing.T) {
	content := `<figure data-trix-attachment="{&quot;contentType&quot;:&quot;application/pdf&quot;,&quot;filename&quot;:&quot;retreat-schedule.pdf&quot;,&quot;url&quot;:&quot;/attachments/12&quot;}"></figure>`
	if got := ToText(content); !strings.Contains(got, "[retreat-schedule.pdf]") {
		t.Errorf("ToText should keep the filename placeholder: %q", got)
	}
}
