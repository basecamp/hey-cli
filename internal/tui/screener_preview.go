package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	hey "github.com/basecamp/hey-sdk/go/pkg/hey"

	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/mail"
	"github.com/basecamp/hey-cli/internal/markdown"
)

// screenerPreviewLoadedMsg is the email a sender in The Screener wrote, read so the
// reader can see all of it before deciding. requestID is the only preview read allowed
// to fill the preview, so a slow answer for a sender the reader already closed is dropped.
type screenerPreviewLoadedMsg struct {
	requestID   uint64
	clearanceID int64
	body        htmlutil.Markdown
	recipients  mail.Recipients
	receivedVia []string
	err         error
}

// screenerPreview is space's bigger look at the sender under the cursor: their most
// recent email in full, over the queue, where a row only has room for its first line.
// It is read-only — the answer is still Yes or No, and either one closes it.
type screenerPreview struct {
	row          screenerRow
	loading      bool
	failure      string
	body         htmlutil.Markdown
	recipients   mail.Recipients
	receivedVia  []string
	width        int    // the width body was last rendered at, 0 when it needs rendering again
	scrollHeader string // header included in the viewport on short terminals
	viewport     viewport.Model
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
		recipients, receivedVia := mail.Envelope(*message)
		return screenerPreviewLoadedMsg{
			requestID:   requestID,
			clearanceID: clearanceID,
			body:        htmlToMarkdown(message.Content),
			recipients:  recipients,
			receivedVia: receivedVia,
		}
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
	v.preview.recipients = msg.recipients
	v.preview.receivedVia = msg.receivedVia
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
	v.layoutPreview()
	var cmd tea.Cmd
	v.preview.viewport, cmd = v.preview.viewport.Update(msg)
	return cmd
}

// previewHeader is who wrote, what about and when, above the body. Each line is cut to
// the width before it is styled, the date's room held back first: a line that wrapped
// would make the header taller than layoutPreview counts, and push the preview off the
// bottom of the screen.
func (v *screenerView) previewHeader() string {
	row := v.preview.row
	width := max(v.vc.width, 20)
	date := ""
	if row.trailing != "" {
		date = "  " + row.trailing
	}
	room := max(width-2-displayWidth(date), 1)
	name, address := row.name, ""
	if name == "" {
		name = row.email
	} else if row.email != "" {
		address = " <" + row.email + ">"
	}
	name = truncateStr(name, room)
	address = truncateStr(address, room-displayWidth(name))
	subject := row.subject
	if subject == row.email {
		subject = ""
	}
	subject = truncateStr(subject, width-2)

	var b strings.Builder
	b.WriteString("  " + lipgloss.NewStyle().Foreground(colorBright).Bold(true).Render(name))
	b.WriteString(lipgloss.NewStyle().Foreground(colorBright).Render(address))
	b.WriteString(styleMuted.Render(date) + "\n")
	for _, line := range envelopeLines(v.preview.recipients, v.preview.receivedVia, width-2) {
		b.WriteString("  " + styleMuted.Render(line) + "\n")
	}
	b.WriteString("  " + lipgloss.NewStyle().Foreground(colorLink).Bold(true).Render(subject) + "\n\n")
	return b.String()
}

// layoutPreview keeps the header fixed when there is room for it and a body row.
// On short terminals the header scrolls with the body so every address stays reachable.
func (v *screenerView) layoutPreview() {
	preview := v.preview
	header := v.previewHeader()
	headerLines := strings.Count(header, "\n")
	height := max(v.vc.height, 1)
	if v.notice != "" && height > 1 {
		height--
	}
	scrollHeader := ""
	if headerLines >= height {
		scrollHeader = header
		headerLines = 0
	}
	if preview.scrollHeader != scrollHeader {
		preview.scrollHeader = scrollHeader
		preview.width = 0
	}
	preview.viewport.SetWidth(max(v.vc.width, 1))
	preview.viewport.SetHeight(height - headerLines)
	width := max(v.vc.width-4, 40)
	if !preview.loading && preview.failure == "" && preview.width == width {
		return
	}
	var content string
	switch {
	case preview.loading:
		content = styleMuted.Render("  Loading…")
	case preview.failure != "":
		content = styleMuted.Render("  " + preview.failure)
	default:
		preview.width = width
		content = markdown.Render(preview.body, width)
		if preview.body.IsEmpty() {
			content = styleMuted.Render("(this email has no text)")
		}
	}
	offset := preview.viewport.YOffset()
	preview.viewport.SetContent(scrollHeader + content)
	preview.viewport.SetYOffset(offset)
}

func (v *screenerView) previewView() string {
	v.layoutPreview()
	var b strings.Builder
	if v.notice != "" && v.vc.height > 1 {
		b.WriteString(v.vc.styles.title.Render(truncateStr(v.notice, max(v.vc.width, 1))) + "\n")
	}
	if v.preview.scrollHeader == "" {
		b.WriteString(v.previewHeader())
	}
	b.WriteString(v.preview.viewport.View())
	return b.String()
}
