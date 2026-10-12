package cmd

import (
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // registers the decoder contactPhoto checks a JPEG with
	_ "image/png"  // registers the decoder contactPhoto checks a PNG with
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-cli/internal/apierr"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

// contactPhotoMaxBytes is the largest photo `hey contact photo set` uploads. HEY's own photo
// form posts through a proxy that refuses a body over 40 MiB, so nothing larger reaches a
// contact from the web; the direct upload used here has no cap of its own, so the CLI keeps
// to the web's. HEY shows a photo at 300×300, so a real one is far smaller.
const contactPhotoMaxBytes = 40 << 20

// contactPhotoFormats are the formats HEY's photo picker accepts, keyed by the content
// type sniffed from the bytes and naming the decoder that must agree. HEY stores whatever
// blob it is handed and only fails later, when it resizes the photo to show it: a HEIC or
// an AVIF, which HEY cannot decode, or a file that is not an image at all, then breaks the
// contact's avatar everywhere it is drawn. So the bytes are judged, not the extension.
var contactPhotoFormats = map[string]string{
	"image/png":  "png",
	"image/jpeg": "jpeg",
}

// contactPhotoMaxPixels and contactPhotoMaxSide are the largest photo the CLI uploads. HEY
// shows a contact photo at 300×300 at most, so the cap is not about quality: it admits a
// full-resolution photo from a modern phone — an iPhone's 48MP is 8064×6048, and 50MP
// Android cameras are common — and stays inside what HEY can resize. HEY stores whatever it
// is handed and only then resizes it, in ImageMagick under hotcell/policy.xml and the
// cell's 896MiB memory limit; a photo past those is never drawn. Measured against HEY's own
// variant command, the tightest ceiling is a JPEG whose EXIF orientation has to be applied,
// which holds a second copy: 7600×7600 resizes and 7700×7700 kills the worker, about 58.7MP.
// An upright photo goes to about 117MP, and policy.xml refuses any side over 16000 pixels.
// 58MP clears all of them but a rotated CMYK JPEG over 47MP, which no camera produces.
const (
	contactPhotoMaxPixels = 58_000_000
	contactPhotoMaxSide   = 16_000
)

// contactPhotoNotEditableHint is what a not_found from the photo routes usually means:
// HEY answers 404 for a contact it will not let you edit, not 403.
const contactPhotoNotEditableHint = "HEY changes the photo of a contact you can edit, or your own: not a hidden contact, an alias, or another HEY user."

type contactPhotoCommand struct {
	cmd *cobra.Command
}

func newContactPhotoCommand() *contactPhotoCommand {
	photoCommand := &contactPhotoCommand{}
	photoCommand.cmd = &cobra.Command{
		Use:     "photo",
		Aliases: []string{"avatar"},
		Short:   "Set or remove a contact's photo",
		Annotations: map[string]string{
			"agent_notes": "Set takes a contact ID and a local PNG or JPEG file of at most 40 MiB, 58 megapixels and 16000 pixels a side; remove takes a contact ID. The photo is the contact's avatar_url. Hidden contacts, aliases and other HEY users answer not_found.",
		},
	}
	photoCommand.cmd.AddCommand(newContactPhotoSetCommand().cmd)
	photoCommand.cmd.AddCommand(newContactPhotoRemoveCommand().cmd)
	return photoCommand
}

type contactPhotoSetCommand struct {
	cmd       *cobra.Command
	contactID int64
	path      string
}

func newContactPhotoSetCommand() *contactPhotoSetCommand {
	setCommand := &contactPhotoSetCommand{}
	setCommand.cmd = &cobra.Command{
		Use:   "set <contact-id> <file>",
		Short: "Upload a photo for a contact",
		Long: "Upload a local image as a contact's photo, replacing any photo they already have. " +
			"The file must be a PNG or JPEG of at most 40 MiB, 58 megapixels and 16000 pixels a side, judged by its contents rather than its name; a full-resolution photo from a modern phone fits. " +
			"HEY crops it to a square, so a square image without transparency works best. " +
			"Only a contact you can edit, or your own, takes a photo: a hidden contact, an alias or another HEY user answers not_found. " +
			"HEY caches avatars, so the old photo may still show in places for up to 30 minutes.",
		Example: `  hey contact photo set 12345 ~/Pictures/jane-doe.jpg
  hey contact photo set 12345 headshot.png --json`,
		Args: setCommand.validateArgs,
		RunE: setCommand.run,
	}
	return setCommand
}

func (c *contactPhotoSetCommand) validateArgs(cmd *cobra.Command, args []string) error {
	if err := usageExactArgs(2)(cmd, args); err != nil {
		return err
	}
	contactID, err := parseContactID(args[0])
	if err != nil {
		return err
	}
	c.contactID = contactID
	c.path = args[1]
	return nil
}

func (c *contactPhotoSetCommand) run(cmd *cobra.Command, _ []string) error {
	photo, err := openContactPhoto(c.path)
	if err != nil {
		return err
	}
	defer closePreparedAttachments([]preparedAttachment{photo})

	if err = requireAuth(); err != nil {
		return err
	}

	// Read the contact first, so a mistyped ID costs a read rather than an upload.
	contact, err := sdk.Contacts().Get(cmd.Context(), c.contactID)
	if err != nil {
		return apierr.FromSDK(err)
	}
	if contact == nil {
		return apierr.ErrNotFound("contact", fmt.Sprintf("%d", c.contactID))
	}

	upload, err := sdk.Attachments().Upload(cmd.Context(), photo.filename, photo.contentType, photo.file)
	if err != nil {
		return apierr.FromSDK(err)
	}
	if upload == nil || upload.SignedId == "" {
		return fmt.Errorf("HEY returned an empty upload reference for %q", c.path)
	}

	updated, err := sdk.Contacts().UpdateAvatar(cmd.Context(), c.contactID, upload.SignedId)
	if err != nil {
		return contactPhotoError(err, c.contactID)
	}
	// HEY answers the contact it saved; should it answer nothing, the photo is still saved.
	var data any = map[string]int64{"id": c.contactID}
	if updated != nil {
		data = updated
	}

	return writeMutationLine(cmd,
		fmt.Sprintf("Photo saved for contact %d (%s). The old photo may still show in places for up to 30 minutes.", c.contactID, terminal.SanitizeLine(contact.Name)),
		"Contact photo saved",
		data,
		output.WithBreadcrumbs(output.Breadcrumb{Action: "remove", Command: fmt.Sprintf("hey contact photo remove %d", c.contactID), Description: "Remove the photo"}),
	)
}

type contactPhotoRemoveCommand struct {
	cmd *cobra.Command
}

func newContactPhotoRemoveCommand() *contactPhotoRemoveCommand {
	removeCommand := &contactPhotoRemoveCommand{}
	removeCommand.cmd = &cobra.Command{
		Use:   "remove <contact-id>",
		Short: "Remove a contact's photo",
		Long: "Remove the photo uploaded for a contact, so HEY shows its default avatar for them again. " +
			"Removing from a contact with no photo succeeds and changes nothing. " +
			"A hidden contact, an alias or another HEY user answers not_found.",
		Example: `  hey contact photo remove 12345`,
		Args:    usageExactOneArg(),
		RunE:    removeCommand.run,
	}
	return removeCommand
}

func (c *contactPhotoRemoveCommand) run(cmd *cobra.Command, args []string) error {
	contactID, err := parseContactID(args[0])
	if err != nil {
		return err
	}
	if err := requireAuth(); err != nil {
		return err
	}
	if err := sdk.Contacts().DeleteAvatar(cmd.Context(), contactID); err != nil {
		return contactPhotoError(err, contactID)
	}
	return writeMutationLine(cmd,
		fmt.Sprintf("Photo removed from contact %d.", contactID),
		"Contact photo removed",
		map[string]int64{"id": contactID},
		output.WithBreadcrumbs(output.Breadcrumb{Action: "set", Command: fmt.Sprintf("hey contact photo set %d <file>", contactID), Description: "Upload a photo"}),
	)
}

// contactPhotoError says why a photo write answered not_found, since that is how HEY
// refuses a contact you cannot edit.
func contactPhotoError(err error, contactID int64) error {
	converted := apierr.FromSDK(err)
	if apierr.AsError(converted).Code == apierr.CodeNotFound {
		return apierr.ErrNotFoundHint("contact", fmt.Sprintf("%d", contactID), contactPhotoNotEditableHint)
	}
	return converted
}

// openContactPhoto opens path and checks it is a photo HEY can show, before anything is
// sent: a regular, non-empty file within contactPhotoMaxBytes whose bytes are a PNG or a
// JPEG with a readable header and dimensions HEY can resize. The file is left open at its start.
func openContactPhoto(path string) (preparedAttachment, error) {
	file, err := os.Open(path) // #nosec G304 -- the user explicitly selected this photo
	if err != nil {
		return preparedAttachment{}, apierr.ErrUsage(fmt.Sprintf("could not open photo %q: %v", path, err))
	}
	photo, err := checkContactPhoto(file, path)
	if err != nil {
		_ = file.Close()
		return preparedAttachment{}, err
	}
	return photo, nil
}

func checkContactPhoto(file *os.File, path string) (preparedAttachment, error) {
	info, err := file.Stat()
	if err != nil {
		return preparedAttachment{}, apierr.ErrUsage(fmt.Sprintf("could not inspect photo %q: %v", path, err))
	}
	if !info.Mode().IsRegular() {
		return preparedAttachment{}, apierr.ErrUsage(fmt.Sprintf("photo %q is not a regular file", path))
	}
	if info.Size() == 0 {
		return preparedAttachment{}, apierr.ErrUsage(fmt.Sprintf("photo %q is empty", path))
	}
	if info.Size() > contactPhotoMaxBytes {
		return preparedAttachment{}, apierr.ErrUsage(fmt.Sprintf("photo %q exceeds the %d MiB contact photo limit",
			path, contactPhotoMaxBytes>>20))
	}

	head := make([]byte, 512)
	read, err := io.ReadFull(file, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return preparedAttachment{}, apierr.ErrUsage(fmt.Sprintf("could not read photo %q: %v", path, err))
	}
	contentType := attachmentMediaType(http.DetectContentType(head[:read]))
	format, ok := contactPhotoFormats[contentType]
	if !ok {
		return preparedAttachment{}, apierr.ErrUsageHint(
			fmt.Sprintf("photo %q is %s, not a PNG or JPEG", path, contentType),
			"Convert it to PNG or JPEG first; HEY cannot show other formats as a contact photo.")
	}

	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return preparedAttachment{}, fmt.Errorf("rewind photo %q: %w", path, err)
	}
	config, decoded, err := image.DecodeConfig(file)
	if err != nil || decoded != format || config.Width <= 0 || config.Height <= 0 {
		return preparedAttachment{}, apierr.ErrUsage(fmt.Sprintf("photo %q is not a readable %s image", path, format))
	}
	if pixels := int64(config.Width) * int64(config.Height); pixels > contactPhotoMaxPixels ||
		config.Width > contactPhotoMaxSide || config.Height > contactPhotoMaxSide {
		return preparedAttachment{}, apierr.ErrUsageHint(
			fmt.Sprintf("photo %q is %d×%d pixels, larger than a contact photo can be", path, config.Width, config.Height),
			"Scale it down to at most 58 megapixels and 16000 pixels a side; HEY shows a contact photo at 300×300.")
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return preparedAttachment{}, fmt.Errorf("rewind photo %q: %w", path, err)
	}

	return preparedAttachment{
		path:        path,
		filename:    filepath.Base(path),
		contentType: contentType,
		byteSize:    info.Size(),
		file:        file,
	}, nil
}
