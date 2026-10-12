package cmd

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
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

// HEY resizes a photo with ImageMagick 6 (Q16) in its hotcell, and a photo it cannot
// resize is stored anyway and then never drawn. Two ceilings decide it, measured by running
// HEY's own variant command under hotcell/policy.xml and the cell's limits:
//
//   - policy.xml refuses an image wider or taller than 16KP outright;
//   - the cell's MAGICK_MEMORY_LIMIT is 896MiB per image (hotcell/Dockerfile), and a pixel
//     costs 8 bytes in it, 10 in a CMYK JPEG. Past that the cache spills to disk, where the
//     worker's 256MB file_size kills it. Auto-orienting a JPEG whose EXIF orientation is
//     anything but upright — a rotation or a flip — holds a second full copy, halving what
//     fits. A PNG's orientation is not applied.
//
// So an upright photo tops out near 117 megapixels, a rotated one near 58, a CMYK one near
// 94 and a rotated CMYK one near 47. policy.xml's 128MP area ceiling is never the one that
// binds. A 200MP phone photo is past the side limit; a 108MP one is past the pixel budget
// whenever it was taken in portrait.
const (
	contactPhotoMaxSide           = 16_000
	contactPhotoPixelCacheBytes   = 896 << 20
	contactPhotoBytesPerPixel     = 8
	contactPhotoCMYKBytesPerPixel = 10
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
			"agent_notes": "Set takes a contact ID and a local PNG or JPEG file of at most 40 MiB and 16000 pixels a side, and of at most about 117 megapixels (58 when its EXIF orientation rotates or flips it, 94 or 47 for a CMYK JPEG); remove takes a contact ID. The photo is the contact's avatar_url. Hidden contacts, aliases and other HEY users answer not_found.",
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
			"The file must be a PNG or JPEG of at most 40 MiB and 16000 pixels a side, judged by its contents rather than its name. " +
			"It must also fit the memory HEY resizes a photo in: about 117 megapixels, 58 for a JPEG its EXIF orientation rotates or flips, and 94 or 47 for a CMYK JPEG. " +
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
	if config.Width > contactPhotoMaxSide || config.Height > contactPhotoMaxSide {
		return preparedAttachment{}, apierr.ErrUsageHint(
			fmt.Sprintf("photo %q is %d×%d pixels, too large for HEY to resize", path, config.Width, config.Height),
			fmt.Sprintf("Scale it down to at most %d pixels a side; HEY shows a contact photo at 300×300.", contactPhotoMaxSide))
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return preparedAttachment{}, fmt.Errorf("rewind photo %q: %w", path, err)
	}
	budget := contactPhotoBudget{cmyk: config.ColorModel == color.CMYKModel}
	if format == "jpeg" {
		budget.reoriented = jpegOrientation(file) > 1
		if _, err = file.Seek(0, io.SeekStart); err != nil {
			return preparedAttachment{}, fmt.Errorf("rewind photo %q: %w", path, err)
		}
	}
	if pixels := int64(config.Width) * int64(config.Height); pixels > budget.maxPixels() {
		return preparedAttachment{}, apierr.ErrUsageHint(
			fmt.Sprintf("photo %q is %d×%d pixels, too large for HEY to resize", path, config.Width, config.Height),
			fmt.Sprintf("HEY can resize %s of at most %.1f megapixels; scale it down first. HEY shows a contact photo at 300×300.",
				budget.describe(), float64(budget.maxPixels())/1e6))
	}

	return preparedAttachment{
		path:        path,
		filename:    filepath.Base(path),
		contentType: contentType,
		byteSize:    info.Size(),
		file:        file,
	}, nil
}

// contactPhotoBudget is what one photo costs HEY's ImageMagick per pixel.
type contactPhotoBudget struct {
	cmyk       bool
	reoriented bool
}

func (b contactPhotoBudget) maxPixels() int64 {
	perPixel := int64(contactPhotoBytesPerPixel)
	if b.cmyk {
		perPixel = contactPhotoCMYKBytesPerPixel
	}
	if b.reoriented {
		perPixel *= 2
	}
	return contactPhotoPixelCacheBytes / perPixel
}

func (b contactPhotoBudget) describe() string {
	kind := "a photo"
	if b.cmyk {
		kind = "a CMYK JPEG"
	}
	if b.reoriented {
		kind += " that its EXIF orientation rotates or flips"
	}
	return kind
}

// jpegOrientation is the EXIF orientation of the JPEG r starts, 1 (upright) when it has
// none or it cannot be read — as it is for ImageMagick, which then leaves the image alone.
// It reads the markers before the scan and only an Exif APP1 segment's IFD0.
func jpegOrientation(r io.Reader) int {
	br := bufio.NewReader(r)
	var soi [2]byte
	if _, err := io.ReadFull(br, soi[:]); err != nil || soi != [2]byte{0xFF, 0xD8} {
		return 1
	}
	for {
		// A marker is 0xFF, any number of 0xFF fill bytes, then its code.
		b, err := br.ReadByte()
		if err != nil || b != 0xFF {
			return 1
		}
		for b == 0xFF {
			if b, err = br.ReadByte(); err != nil {
				return 1
			}
		}
		switch {
		case b == 0xDA || b == 0xD9: // start of scan, end of image
			return 1
		case b == 0x01 || (b >= 0xD0 && b <= 0xD7): // markers without a length
			continue
		}
		var length [2]byte
		if _, err := io.ReadFull(br, length[:]); err != nil {
			return 1
		}
		size := int(binary.BigEndian.Uint16(length[:])) - 2
		if size < 0 {
			return 1
		}
		segment := make([]byte, size)
		if _, err := io.ReadFull(br, segment); err != nil {
			return 1
		}
		if b == 0xE1 && len(segment) >= 6 && string(segment[:6]) == "Exif\x00\x00" {
			return exifOrientation(segment[6:])
		}
	}
}

// exifOrientation reads tag 0x0112 from the IFD0 of a TIFF-structured EXIF block.
func exifOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	ifd := int64(order.Uint32(tiff[4:8]))
	if ifd < 8 || ifd+2 > int64(len(tiff)) {
		return 1
	}
	entries := int64(order.Uint16(tiff[ifd:]))
	for i := int64(0); i < entries; i++ {
		entry := ifd + 2 + i*12
		if entry+12 > int64(len(tiff)) {
			return 1
		}
		if order.Uint16(tiff[entry:]) == 0x0112 && order.Uint16(tiff[entry+2:]) == 3 { // SHORT
			if orientation := int(order.Uint16(tiff[entry+8:])); orientation >= 1 && orientation <= 8 {
				return orientation
			}
			return 1
		}
	}
	return 1
}
