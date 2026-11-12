package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

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
	find     bodyFind
	lines    []string
	linkRuns []markdown.LinkTextRun
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
	preview := v.preview
	if preview.find.editing {
		cmd, committed := preview.find.edit(msg)
		if committed {
			preview.find.scan(findDocument{lines: preview.lines, links: preview.linkRuns})
			preview.find.firstFrom(preview.viewport.YOffset())
			v.showPreviewMatch()
		}
		return cmd
	}
	key := msg.String()
	if !preview.loading && preview.failure == "" {
		switch {
		case key == "/":
			return preview.find.start()
		case preview.find.query != "" && (key == "n" || key == "N"):
			delta := 1
			if key == "N" {
				delta = -1
			}
			preview.find.next(delta)
			v.showPreviewMatch()
			return nil
		case preview.find.active() && msg.Key().Code == tea.KeyEscape:
			preview.find = bodyFind{}
			preview.viewport.SetContentLines(preview.lines)
			return nil
		}
	}
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
	b.WriteString("  " + lipgloss.NewStyle().Foreground(colorLink).Bold(true).Render(subject) + "\n\n")
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
	oldHeight := preview.viewport.Height()
	preview.viewport.SetHeight(max(v.vc.height-headerLines, 1))
	width := max(v.vc.width-4, 40)
	if preview.loading || preview.failure != "" {
		return
	}
	if preview.width == width {
		if oldHeight != preview.viewport.Height() {
			v.revealPreviewMatch()
		}
		return
	}
	preview.width = width
	linked := markdown.RenderLinked(preview.body, width, -1)
	content := linked.Text
	preview.linkRuns = nil
	for _, link := range linked.Links {
		preview.linkRuns = append(preview.linkRuns, link.TextRuns...)
	}
	if preview.body.IsEmpty() {
		content = styleMuted.Render("(this email has no text)")
	}
	offset := preview.viewport.YOffset()
	preview.lines = strings.Split(content, "\n")
	preview.find.scan(findDocument{lines: preview.lines, links: preview.linkRuns})
	preview.viewport.SetContentLines(preview.find.highlight(preview.lines))
	preview.viewport.SetYOffset(offset)
	v.revealPreviewMatch()
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

func (v *screenerView) showPreviewMatch() {
	preview := v.preview
	preview.viewport.SetContentLines(preview.find.highlight(preview.lines))
	v.revealPreviewMatch()
}

func (v *screenerView) revealPreviewMatch() {
	preview := v.preview
	if match, found := preview.find.selected(); found {
		plain := ansi.Strip(preview.lines[match.line])
		preview.viewport.EnsureVisible(match.line, ansi.StringWidth(plain[:match.start]), ansi.StringWidth(plain[:match.end]))
	}
}

// The find prompt shares the app's footer allocation rather than making the
// preview's fixed header taller.
func (v *screenerView) LinkFooter() (string, bool) {
	if v.preview == nil || !v.preview.find.active() {
		return "", false
	}
	return v.preview.find.footer(v.vc.width), true
}
