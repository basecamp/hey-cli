package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/markdown"
)

// screenerPreviewLoadedMsg is the email a sender in The Screener wrote, read so the
// reader can see all of it before deciding. requestID is the only preview read allowed
// to fill the preview, so a slow answer for a sender the reader already closed is dropped.
type screenerPreviewLoadedMsg struct {
	requestID   uint64
	clearanceID int64
	body        htmlutil.Markdown
	err         error
}

// screenerPreview is space's bigger look at the sender under the cursor: their most
// recent email in full, over the queue, where a row only has room for its first line.
// It is read-only — the answer is still Yes or No, and either one closes it.
type screenerPreview struct {
	row      screenerRow
	loading  bool
	failure  string
	body     htmlutil.Markdown
	width    int // the width body was last rendered at, 0 when it needs rendering again
	viewport viewport.Model
}

// openPreview reads the most recent email of the sender under the cursor. HEY serves its
// entry with each clearance, so this is one message read; nothing about it marks the
// email seen or answers the sender.
func (v *screenerView) openPreview() tea.Cmd {
	if v.tab != screenerPendingTab {
		return nil
	}
	row := v.pane().selected()
	if row == nil {
		return nil
	}
	if row.entryID == 0 {
		v.notice = "HEY sent nothing to preview for " + screenerRowName(*row)
		return nil
	}
	v.previewRequestID++
	requestID := v.previewRequestID
	v.notice = ""
	v.preview = &screenerPreview{
		row:      *row,
		loading:  true,
		viewport: viewport.New(viewport.WithWidth(0), viewport.WithHeight(0)),
	}
	sdk, ctx, entryID, clearanceID := v.vc.sdk, v.vc.ctx, row.entryID, row.id
	return func() tea.Msg {
		message, err := sdk.Messages().Get(ctx, entryID)
		if err == nil && message == nil {
			err = fmt.Errorf("message %d returned no data", entryID)
		}
		if err != nil {
			return screenerPreviewLoadedMsg{requestID: requestID, clearanceID: clearanceID, err: err}
		}
		return screenerPreviewLoadedMsg{requestID: requestID, clearanceID: clearanceID, body: htmlToMarkdown(message.Content)}
	}
}

func (v *screenerView) previewLoaded(msg screenerPreviewLoadedMsg) {
	if v.preview == nil || msg.requestID != v.previewRequestID {
		return
	}
	v.preview.loading = false
	if msg.err != nil {
		v.preview.failure = errorNotice("Could not load the email", msg.err)
		return
	}
	v.preview.body = msg.body
	v.preview.width = 0
	v.layoutPreview()
	v.preview.viewport.GotoTop()
}

func (v *screenerView) closePreview() {
	v.preview = nil
	v.previewRequestID++
}

// handlePreviewKey is every key while the preview is open. Yes and No answer for the
// sender being previewed — not whoever a live re-read has since put under the cursor —
// and put the preview away with the decision.
func (v *screenerView) handlePreviewKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	switch {
	case key == "space" || key == " " || key == "q" || msg.Key().Code == tea.KeyEscape:
		v.closePreview()
		return nil
	case key == "F":
		v.emphaticNo = !v.emphaticNo
		return nil
	case key == "y" || key == "i" || key == "n":
		row := v.preview.row
		v.closePreview()
		status := hey.ClearanceApproved
		if key == "n" {
			status = hey.ClearanceDenied
		}
		return v.screenRow(row, status)
	}
	if v.preview.loading || v.preview.failure != "" {
		return nil
	}
	v.layoutPreview()
	var cmd tea.Cmd
	v.preview.viewport, cmd = v.preview.viewport.Update(msg)
	return cmd
}

// previewHeader is who wrote, what about and when, above the body.
func (v *screenerView) previewHeader() string {
	row := v.preview.row
	width := max(v.vc.width, 20)
	name := lipgloss.NewStyle().Foreground(colorBright).Bold(true).Render(row.name)
	if row.name == "" {
		name = lipgloss.NewStyle().Foreground(colorBright).Bold(true).Render(row.email)
	} else if row.email != "" {
		name += lipgloss.NewStyle().Foreground(colorBright).Render(" <" + row.email + ">")
	}
	subject := row.subject
	if subject == row.email {
		subject = ""
	}
	var b strings.Builder
	b.WriteString(truncateStr("  "+name, width))
	if row.trailing != "" {
		b.WriteString(styleMuted.Render("  " + row.trailing))
	}
	b.WriteString("\n")
	b.WriteString(truncateStr("  "+lipgloss.NewStyle().Foreground(colorLink).Bold(true).Render(subject), width) + "\n\n")
	return b.String()
}

// layoutPreview fits the viewport under the header and renders the body at the width
// there is, again only when the width changed since the last time.
func (v *screenerView) layoutPreview() {
	preview := v.preview
	headerLines := strings.Count(v.previewHeader(), "\n")
	if v.notice != "" {
		headerLines++
	}
	preview.viewport.SetWidth(max(v.vc.width, 1))
	preview.viewport.SetHeight(max(v.vc.height-headerLines, 1))
	width := max(v.vc.width-4, 40)
	if preview.loading || preview.failure != "" || preview.width == width {
		return
	}
	preview.width = width
	content := markdown.Render(preview.body, width)
	if preview.body.IsEmpty() {
		content = styleMuted.Render("(this email has no text)")
	}
	offset := preview.viewport.YOffset()
	preview.viewport.SetContent(content)
	preview.viewport.SetYOffset(offset)
}

func (v *screenerView) previewView() string {
	var b strings.Builder
	if v.notice != "" {
		b.WriteString(v.vc.styles.title.Render(truncateStr(v.notice, max(v.vc.width, 1))) + "\n")
	}
	b.WriteString(v.previewHeader())
	switch {
	case v.preview.loading:
		b.WriteString(styleMuted.Render("  Loading…"))
	case v.preview.failure != "":
		b.WriteString(styleMuted.Render("  " + v.preview.failure))
	default:
		v.layoutPreview()
		b.WriteString(v.preview.viewport.View())
	}
	return b.String()
}
