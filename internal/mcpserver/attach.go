package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	stdhtml "html"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/basecamp/mcp/filecontent"
	"github.com/basecamp/mcp/gateway"
)

// attach_file: a file, attached to a saved draft, in a tool of its own (hey_attach),
// the way hey-mcp-server's attach_file takes one (basecamp-mcp-server's file tools, as
// HEY takes them).
//
// An attachment rides on a draft and is never sent by itself: the tool uploads the file
// (Active Storage's direct upload), then saves the draft with the file after its body,
// everything else as it was. It reaches anyone only when the person sends the draft:
// send_draft's confirmation names every attachment with its size (sendprompt.go), as
// `hey draft send` and HEY's own Send do.
//
// The file comes as content_base64, optionally with its sha256, checked before anything
// is uploaded, read by the toolkit's reader (readAttachment), as hey-mcp-server reads
// it. hey-mcp-server also takes ChatGPT's file object (fileParams); hey mcp runs on stdio,
// where ChatGPT never connects, so it lists no file argument and the reader refuses one.
// A shell agent with a file on disk has `hey compose --draft --attach` and
// `hey reply <thread-id> --draft --attach`.
//
// The markup that carries the file is built here from what HEY answered for the upload
// and the bytes counted, never from anything the caller wrote.

const (
	attachToolName = "hey_attach"
	attachFileName = "attach_file"

	// attachmentNameMax bounds a file name, in runes.
	attachmentNameMax = 255
)

// attachmentMaxBytes is the largest file attach_file takes. HEY sets no limit of its own
// on a draft's attachments, but mail goes out base64 encoded, a third larger, and the
// providers most people write to refuse a message past 25 MB of that. A var, so a test
// can lower it.
var attachmentMaxBytes int64 = 18 << 20

// MaxFrameBytes is the largest JSON-RPC frame hey mcp reads from its client: room for
// the largest file as content_base64 (a third larger than the file) and the rest of the
// call. The SDK's default (16 MiB) holds about 12 MB of file, and a frame past the cap
// ends the session rather than answering, so a file up to attachmentMaxBytes must fit
// and one a little past it is answered "too large".
const MaxFrameBytes = 32 << 20

// DraftFiles is the slice of hey-sdk attach_file drives. Put (the draft's revision) and
// Upload must go through a client that never retries. The revision is a plain PUT, as
// update_message's is, so its answer is read as update_message's is (savedAsDraft): the
// SDK's UpdateDraft takes any 2xx as saved.
type DraftFiles interface {
	GetEdit(ctx context.Context, draftID int64) (*generated.MessageEditState, error)
	Put(ctx context.Context, path string, body any) (*hey.Response, error)
	Upload(ctx context.Context, filename, contentType string, content io.ReadSeeker) (*generated.DirectUpload, error)
}

// AttachConfig serves attach_file. Nil serves none.
type AttachConfig struct {
	Files DraftFiles
	// Origin is the HEY this runs against, e.g. https://app.hey.com: links point there.
	Origin string
}

type attacher struct {
	cfg AttachConfig
}

func newAttacher(cfg AttachConfig) (*attacher, error) {
	switch {
	case cfg.Files == nil:
		return nil, errors.New("attach_file needs HEY's drafts")
	case cfg.Origin == "":
		return nil, errors.New("attach_file needs the HEY origin")
	}
	return &attacher{cfg: cfg}, nil
}

const attachDescription = "Attach a file to a saved draft, after its body; the rest of the draft stays as it is. " +
	"It isn't sent: the person sends the draft as any other, and the confirmation that sends it names each attachment and its size. " +
	"A draft scheduled to send isn't changed here. A file on disk is attached with `hey compose --draft --attach <path>` or " +
	"`hey reply <thread-id> --draft --attach <path>` instead, where a shell is at hand. " +
	"Actions: attach_file (params: draft_id, filename, content_type, and content_base64 with an optional sha256), describe."

func (a *attacher) tool() *mcp.Tool {
	destructive, openWorld := true, false
	return &mcp.Tool{
		Name:        attachToolName,
		Title:       "Attach a file to a HEY draft",
		Description: attachDescription,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{"type": "string", "enum": []any{attachFileName, gateway.DescribeAction}},
				"params": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"draft_id":       map[string]any{"type": []any{"integer", "string"}, "description": "The draft's id, from create_message, create_reply or list_drafts."},
						"filename":       map[string]any{"type": "string", "description": "The file's name as the recipient sees it, e.g. report.pdf."},
						"content_type":   map[string]any{"type": "string", "description": "The file's media type, e.g. application/pdf."},
						"content_base64": map[string]any{"type": "string", "description": "The file's bytes, base64 encoded."},
						"sha256":         map[string]any{"type": "string", "description": "Hex SHA-256 of the decoded bytes; a mismatch attaches nothing."},
					},
				},
			},
			"required": []any{"action"},
		},
		Annotations: &mcp.ToolAnnotations{
			Title:           "Attach a file to a HEY draft",
			ReadOnlyHint:    false,
			DestructiveHint: &destructive,
			IdempotentHint:  false,
			OpenWorldHint:   &openWorld,
		},
	}
}

var attachParams = map[string]bool{"draft_id": true, "filename": true, "content_type": true, "content_base64": true, "sha256": true, "file": true}

func (a *attacher) handle(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var args struct {
		Action string         `json:"action"`
		Params map[string]any `json:"params"`
	}
	if len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return gateway.ErrorResult("invalid arguments: %v", err), nil
		}
	}
	switch args.Action {
	case gateway.DescribeAction:
		return gateway.JSONResult(map[string]any{
			"action":      attachFileName,
			"description": attachDescription,
			"params": map[string]any{
				"draft_id": "integer (required)", "filename": "string (required)", "content_type": "string (required)",
				"content_base64": "string, the file's bytes in base64", "sha256": "string (optional), hex SHA-256 of the bytes",
			},
		})
	case attachFileName:
	default:
		return gateway.ErrorResult("unknown action %q in %s (actions: %s, %s)", args.Action, attachToolName, attachFileName, gateway.DescribeAction), nil
	}
	for key := range args.Params {
		if !attachParams[key] {
			return gateway.ErrorResult("attach_file doesn't take params.%s", key), nil
		}
	}
	draftID, err := draftIDOf(args.Params)
	if err != nil {
		return gateway.ErrorResult("%v", err), nil
	}
	filename, err := attachmentName(args.Params["filename"])
	if err != nil {
		return gateway.ErrorResult("%v", err), nil
	}
	ct, _ := args.Params["content_type"].(string)
	contentType := attachmentType(ct)

	// The file first: everything wrong with it is refused before HEY is asked anything.
	file, errResult := readAttachment(ctx, args.Params, attachmentMaxBytes)
	if errResult != nil {
		return errResult, nil
	}
	defer file.Done()

	edit, err := a.cfg.Files.GetEdit(ctx, draftID)
	if err != nil {
		var apiErr *hey.Error
		if errors.As(err, &apiErr) && (apiErr.HTTPStatus == http.StatusNotFound || apiErr.HTTPStatus == http.StatusUnprocessableEntity) {
			return gateway.ErrorResult("nothing attached: %d isn't a draft HEY can change (it may have been sent, trashed or deleted)", draftID), nil
		}
		if answer := heyUpstreamFailure(err, gateway.KindRead, ""); answer != nil {
			return answer, nil
		}
		return gateway.ErrorResult("nothing attached: couldn't read draft %d: %s", draftID, heyReason(err)), nil
	}
	// HEY serves a message's edit state only while it's a live draft (drafted and not
	// in its undo window), so reading it is the check that it's still one.
	if edit == nil || edit.Id != draftID {
		return gateway.ErrorResult("nothing attached: %d isn't a draft HEY can change", draftID), nil
	}
	draft := hey.DraftContentFromEdit(edit)
	// The revision replaces every recipient, and a contact with no address can't be
	// written back: it would be dropped from the draft. Refuse first, as send_draft does.
	for _, contacts := range [][]generated.Contact{edit.Addressed.Directly, edit.Addressed.Copied, edit.Addressed.Blindcopied} {
		for _, c := range contacts {
			if normalizedAddress(c.EmailAddress) == "" {
				return gateway.ErrorResult("nothing attached: a recipient of draft %d has no email address, and saving the file would drop them; attach it in HEY: %s", draftID, a.editURL(draftID)), nil
			}
		}
	}
	if draft.ActingSenderID == 0 {
		return gateway.ErrorResult("nothing attached: HEY named no sender for draft %d", draftID), nil
	}
	if draft.Schedule != nil {
		return gateway.ErrorResult("nothing attached: this draft is scheduled to send, so a file is attached to it in HEY: %s", a.editURL(draftID)), nil
	}

	upload, err := a.cfg.Files.Upload(ctx, filename, contentType, bytes.NewReader(file.Data))
	if err != nil {
		if answer := heyUpstreamFailure(err, gateway.KindWrite, ""); answer != nil {
			return answer, nil
		}
		return gateway.ErrorResult("nothing attached: HEY didn't take the file: %s", heyReason(err)), nil
	}
	if upload == nil || upload.AttachableSgid == "" {
		return gateway.ErrorResult("nothing attached: HEY took the file but named no attachment for it, so the draft wasn't changed"), nil
	}
	added := attachment{SGID: upload.AttachableSgid, Filename: filename, Size: int64(len(file.Data)), ContentType: contentType}
	draft.Content = withAttachment(draft.Content, added)

	resp, err := a.cfg.Files.Put(ctx, fmt.Sprintf("/messages/%d.json", draftID), draftRevision(draft))
	if err != nil {
		if refusedByHEY(err) {
			return gateway.ErrorResult("nothing attached: HEY refused the draft's revision: %s. The file was uploaded but isn't in the draft", heyReason(err)), nil
		}
		if answer := heyUpstreamFailure(err, gateway.KindWrite, unsettledDraftNext); answer != nil {
			return answer, nil
		}
		return gateway.ErrorResult("%s", unsettledDraft(attachFileName, err)), nil
	}
	if !savedAsDraft(resp) {
		return gateway.ErrorResult("%s", unconfirmedDraft(attachFileName, resp)), nil
	}
	return gateway.JSONResult(map[string]any{
		"draft_id": draftID,
		"attached": map[string]any{"filename": filename, "content_type": contentType, "byte_size": added.Size},
		"sent":     false,
		"edit_url": a.editURL(draftID),
		"note":     "Attached to the draft, not sent. " + howToSendNote(ctx),
	})
}

// howToSendNote says how the draft goes out, as the draft saves' notes do: hey_send
// where this client is offered it.
func howToSendNote(ctx context.Context) string {
	if sendable(ctx) {
		return "When the person asks to send it, hey_send's send_draft asks them to confirm, showing the attachment."
	}
	return "The person sends it from its page in HEY (edit_url) or with `hey draft send <id>`."
}

func (a *attacher) editURL(draftID int64) string {
	return fmt.Sprintf("%s/messages/%d/edit", strings.TrimRight(a.cfg.Origin, "/"), draftID)
}

// draftIDOf reads params.draft_id: a draft's id, as an integer or its digits.
func draftIDOf(params map[string]any) (int64, error) {
	raw, ok := params["draft_id"]
	if !ok {
		return 0, errors.New("attach_file needs params.draft_id, the id create_message or create_reply returned")
	}
	only := map[string]any{"draft_id": raw}
	id, err := draftIDParam(only)
	if err != nil {
		return 0, fmt.Errorf("params.draft_id must be a draft's id, got %v", raw)
	}
	return id, nil
}

// attachmentName is the file's name as HEY gets it: its last path segment, trimmed,
// with no control characters, and not empty.
func attachmentName(raw any) (string, error) {
	name, _ := raw.(string)
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name))
	switch {
	case name == "" || name == "." || name == "..":
		return "", errors.New("attach_file needs params.filename: the file's name, such as report.pdf")
	case len([]rune(name)) > attachmentNameMax:
		return "", fmt.Errorf("attach_file: params.filename is longer than %d characters", attachmentNameMax)
	}
	return name, nil
}

// attachmentType is the media type HEY is told, its parameters dropped; anything that
// isn't one is sent as bytes.
func attachmentType(contentType string) string {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil || !strings.Contains(mediaType, "/") {
		return "application/octet-stream"
	}
	return mediaType
}

// withAttachment is content with the file after it, as hey compose --attach puts it:
// the element Action Text resolves by its sgid, with nothing inside.
func withAttachment(content string, a attachment) string {
	var b strings.Builder
	b.WriteString(content)
	if content != "" {
		b.WriteString("<br>")
	}
	fmt.Fprintf(&b, `<action-text-attachment sgid="%s" content-type="%s" filename="%s" filesize="%d"></action-text-attachment>`,
		stdhtml.EscapeString(a.SGID), stdhtml.EscapeString(a.ContentType), stdhtml.EscapeString(a.Filename), a.Size)
	return b.String()
}

// readAttachment reads the file's bytes from params.content_base64, with its sha256 when
// given, refusing anything wrong with them before HEY is asked anything. hey mcp takes
// no ChatGPT file object (AcceptsFile false), so the reader never fetches. The caller
// defers the content's Done.
func readAttachment(ctx context.Context, params map[string]any, maxSize int64) (*filecontent.Content, *mcp.CallToolResult) {
	content, err := filecontent.Read(ctx, params, filecontent.Options{MaxSize: maxSize, RequireBytes: true})
	if err != nil {
		return nil, gateway.ErrorResult("nothing attached: %v", err)
	}
	return content, nil
}

// draftRevision is UpdateMessage's body for draft, held to a draft: what the SDK's
// UpdateDraft sends for one with no schedule (attach_file refuses a scheduled draft).
func draftRevision(draft hey.DraftContent) map[string]any {
	return map[string]any{
		"acting_sender_id": draft.ActingSenderID,
		"message":          map[string]any{"subject": draft.Subject, "content": draft.Content},
		"entry": map[string]any{"status": "drafted", "addressed": map[string]any{
			"directly": nonNil(draft.To), "copied": nonNil(draft.CC), "blindcopied": nonNil(draft.BCC)}},
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
