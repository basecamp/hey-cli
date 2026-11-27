package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Detail images have their own lifetime: opening text settles the foreground read
// before image work starts. Unlike that read, this work never shows a spinner.
type detailImages struct {
	pending *detailImageRequest
}

type detailImageRequest struct {
	cancel context.CancelFunc
}

type detailImagesLoadedMsg struct {
	request *detailImageRequest
	images  [][]byte
}

func (d *detailImages) start(vc *viewContext, urls []string) tea.Cmd {
	d.cancel()
	if len(urls) == 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(vc.ctx)
	request := &detailImageRequest{cancel: cancel}
	d.pending = request
	fetcher := vc.imageFetcher
	return func() tea.Msg {
		return detailImagesLoadedMsg{request: request, images: newImageBudget().fetchImages(ctx, fetcher, urls)}
	}
}

func (d *detailImages) cancel() {
	if d.pending != nil {
		d.pending.cancel()
		d.pending = nil
	}
}

func (d *detailImages) accept(msg detailImagesLoadedMsg) bool {
	if d.pending == nil || d.pending != msg.request {
		return false
	}
	d.cancel()
	return true
}

func renderDetailImages(vc *viewContext, images [][]byte) (string, tea.Cmd) {
	var content strings.Builder
	var uploads []tea.Cmd
	for _, data := range images {
		rendered := vc.imageRenderer.render(data, nextImageID(), vc.width-4)
		if rendered.content != "" {
			content.WriteString("\n\n")
			content.WriteString(rendered.content)
		}
		if rendered.raw != "" {
			uploads = append(uploads, tea.Raw(rendered.raw))
		}
	}
	return content.String(), tea.Batch(uploads...)
}
