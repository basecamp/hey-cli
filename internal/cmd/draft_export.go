package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/basecamp/hey-sdk/go/pkg/generated"

	"github.com/basecamp/hey-cli/internal/apierr"
	attachmentfiles "github.com/basecamp/hey-cli/internal/attachments"
	"github.com/basecamp/hey-cli/internal/htmlutil"
	"github.com/basecamp/hey-cli/internal/output"
	"github.com/basecamp/hey-cli/internal/terminal"
)

const (
	draftExportFormat           = "hey-draft-export/v1"
	draftExportManifestMaxBytes = 1 << 20
)

type draftExportCommand struct {
	cmd    *cobra.Command
	output string
	force  bool
}

type draftExportAttachment struct {
	Position    int    `json:"position"`
	Filename    string `json:"filename"`
	Path        string `json:"path"`
	ContentType string `json:"content_type,omitempty"`
	ByteSize    int64  `json:"byte_size"`
	SHA256      string `json:"sha256"`
}

type draftExportManifest struct {
	Format              string                  `json:"format"`
	ExportedAt          time.Time               `json:"exported_at"`
	DraftID             int64                   `json:"draft_id"`
	Subject             string                  `json:"subject,omitempty"`
	Body                string                  `json:"body"`
	HTMLPath            string                  `json:"html_path"`
	Attachments         []draftExportAttachment `json:"attachments"`
	To                  []string                `json:"to,omitempty"`
	CC                  []string                `json:"cc,omitempty"`
	BCC                 []string                `json:"bcc,omitempty"`
	From                string                  `json:"from,omitempty"`
	IsReply             bool                    `json:"is_reply,omitempty"`
	ScheduledDeliveryAt *time.Time              `json:"scheduled_delivery_at,omitempty"`
	UpdatedAt           *time.Time              `json:"updated_at,omitempty"`
}

type draftExportResult struct {
	DraftID      int64                   `json:"draft_id"`
	Path         string                  `json:"path"`
	HTMLPath     string                  `json:"html_path"`
	ManifestPath string                  `json:"manifest_path"`
	Attachments  []draftExportAttachment `json:"attachments"`
}

func newDraftExportCommand() *draftExportCommand {
	exportCommand := &draftExportCommand{}
	exportCommand.cmd = &cobra.Command{
		Use:   "export <draft-id>",
		Short: "Export a complete draft bundle",
		Annotations: map[string]string{
			"agent_notes": "Reads one exact draft into a private local directory containing draft.html, draft.json, and downloaded attachments. HEY is not changed. The destination must not exist; --force only replaces a complete export of the same draft. Files are staged first, and an incomplete download publishes nothing.",
		},
		Example: `  hey draft export 12345 --output ./draft-12345
  hey draft export 12345 --output ./draft-12345 --force
  hey draft export 12345 --output ./draft-12345 --json`,
		RunE: exportCommand.run,
		Args: usageExactOneArg(),
	}
	exportCommand.cmd.Flags().StringVarP(&exportCommand.output, "output", "o", "", "New directory for the exported draft bundle")
	exportCommand.cmd.Flags().BoolVar(&exportCommand.force, "force", false, "Replace a complete export of this same draft")
	_ = exportCommand.cmd.MarkFlagRequired("output")
	return exportCommand
}

func (c *draftExportCommand) run(cmd *cobra.Command, args []string) error {
	if writer.RequestedFormat() == output.FormatHTML {
		return apierr.ErrUsage("--html is not supported by draft export; the bundle already contains draft.html")
	}
	draftID, err := parseDraftID(args[0])
	if err != nil {
		return err
	}
	destination, existed, err := preflightDraftExportDestination(c.output, draftID, c.force)
	if err != nil {
		return err
	}
	if authErr := requireAuth(); authErr != nil {
		return authErr
	}

	edit, err := sdk.Messages().GetEdit(cmd.Context(), draftID)
	if err != nil {
		return apierr.FromSDK(err)
	}
	if edit == nil {
		return apierr.ErrNotFound("draft", args[0])
	}

	result, err := exportDraftBundle(cmd.Context(), destination, draftID, edit, existed)
	if err != nil {
		return err
	}
	if writer.IsStyled() {
		fmt.Fprintf(cmd.OutOrStdout(), "Draft %d exported to %s\n", result.DraftID, terminal.SanitizeLine(result.Path))
		fmt.Fprintf(cmd.OutOrStdout(), "  draft.html\n  draft.json\n  attachments: %d\n", len(result.Attachments))
		return nil
	}
	return writeOK(result, output.WithSummary(fmt.Sprintf("Draft %d exported", result.DraftID)))
}

func preflightDraftExportDestination(outputPath string, draftID int64, force bool) (string, bool, error) {
	if strings.TrimSpace(outputPath) == "" {
		return "", false, apierr.ErrUsage("--output must name a new draft export directory")
	}
	destination, err := filepath.Abs(filepath.Clean(outputPath))
	if err != nil {
		return "", false, apierr.ErrAPI(0, fmt.Sprintf("could not resolve output path: %v", err))
	}
	parent := filepath.Dir(destination)
	parentInfo, err := os.Stat(parent)
	if err != nil {
		return "", false, apierr.ErrAPI(0, fmt.Sprintf("could not inspect output parent: %v", err))
	}
	if !parentInfo.IsDir() {
		return "", false, apierr.ErrUsage(fmt.Sprintf("output parent is not a directory: %s", parent))
	}

	_, err = os.Lstat(destination)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return destination, false, nil
	case err != nil:
		return "", false, apierr.ErrAPI(0, fmt.Sprintf("could not inspect output destination: %v", err))
	case !force:
		return "", true, apierr.ErrUsage(fmt.Sprintf("destination already exists: %s (use --force to replace a complete export of this draft)", destination))
	}
	if err := validateExistingDraftExport(destination, draftID); err != nil {
		return "", true, err
	}
	return destination, true, nil
}

func exportDraftBundle(ctx context.Context, destination string, draftID int64, edit *generated.MessageEditState, replace bool) (draftExportResult, error) {
	parent := filepath.Dir(destination)
	staging, err := os.MkdirTemp(parent, ".hey-draft-export-*")
	if err != nil {
		return draftExportResult{}, apierr.ErrAPI(0, fmt.Sprintf("could not create export staging directory: %v", err))
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(staging)
		}
	}()
	stagingRoot, err := os.OpenRoot(staging)
	if err != nil {
		return draftExportResult{}, apierr.ErrAPI(0, fmt.Sprintf("could not open export staging directory: %v", err))
	}
	rootOpen := true
	defer func() {
		if rootOpen {
			_ = stagingRoot.Close()
		}
	}()

	if mkdirErr := stagingRoot.Mkdir("attachments", 0o700); mkdirErr != nil {
		return draftExportResult{}, apierr.ErrAPI(0, fmt.Sprintf("could not create attachment staging directory: %v", mkdirErr))
	}
	if writeErr := writePrivateExportFile(stagingRoot, "draft.html", []byte(edit.Content)); writeErr != nil {
		return draftExportResult{}, writeErr
	}

	sourceAttachments := htmlutil.ExtractAttachments(edit.Content)
	exportedAttachments := make([]draftExportAttachment, 0, len(sourceAttachments))
	usedFilenames := make(map[string]struct{}, len(sourceAttachments))
	for index, attachment := range sourceAttachments {
		exported, exportErr := exportDraftAttachment(ctx, stagingRoot, index+1, attachment, usedFilenames)
		if exportErr != nil {
			return draftExportResult{}, exportErr
		}
		exportedAttachments = append(exportedAttachments, exported)
	}

	out := draftOutputFor(draftID, edit)
	manifest := draftExportManifest{
		Format:              draftExportFormat,
		ExportedAt:          time.Now().UTC(),
		DraftID:             draftID,
		Subject:             out.Subject,
		Body:                out.Body.String(),
		HTMLPath:            "draft.html",
		Attachments:         exportedAttachments,
		To:                  out.To,
		CC:                  out.CC,
		BCC:                 out.BCC,
		From:                edit.Sender.EmailAddress,
		IsReply:             out.IsReply,
		ScheduledDeliveryAt: out.ScheduledDeliveryAt,
		UpdatedAt:           out.UpdatedAt,
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return draftExportResult{}, apierr.ErrAPI(0, fmt.Sprintf("could not encode draft manifest: %v", err))
	}
	if err := writePrivateExportFile(stagingRoot, "draft.json", append(manifestJSON, '\n')); err != nil {
		return draftExportResult{}, err
	}
	if err := stagingRoot.Close(); err != nil {
		return draftExportResult{}, apierr.ErrAPI(0, fmt.Sprintf("could not close export staging directory: %v", err))
	}
	rootOpen = false

	if replace {
		if err := replaceDraftExportDirectory(staging, destination, draftID); err != nil {
			return draftExportResult{}, err
		}
	} else if err := commitDraftExportDirectory(staging, destination); err != nil {
		return draftExportResult{}, err
	}
	published = true

	return draftExportResult{
		DraftID:      draftID,
		Path:         destination,
		HTMLPath:     filepath.Join(destination, "draft.html"),
		ManifestPath: filepath.Join(destination, "draft.json"),
		Attachments:  exportedAttachments,
	}, nil
}

func exportDraftAttachment(ctx context.Context, root *os.Root, position int, attachment htmlutil.Attachment, used map[string]struct{}) (draftExportAttachment, error) {
	filename, err := uniqueDraftExportFilename(attachment.Filename, used)
	if err != nil {
		return draftExportAttachment{}, err
	}
	relativePath := filepath.Join("attachments", filename)
	written, err := downloadDraftExportAttachment(ctx, root, relativePath, attachment.URL)
	if err != nil {
		return draftExportAttachment{}, err
	}
	if attachment.ByteSize != nil && written != *attachment.ByteSize {
		return draftExportAttachment{}, apierr.ErrAPI(0, fmt.Sprintf(
			"attachment %q downloaded %d bytes; HEY reported %d", terminal.SanitizeLine(attachment.Filename), written, *attachment.ByteSize))
	}
	digest, err := sha256File(root, relativePath)
	if err != nil {
		return draftExportAttachment{}, err
	}
	return draftExportAttachment{
		Position:    position,
		Filename:    attachment.Filename,
		Path:        filepath.ToSlash(filepath.Join("attachments", filename)),
		ContentType: attachment.ContentType,
		ByteSize:    written,
		SHA256:      digest,
	}, nil
}

func uniqueDraftExportFilename(filename string, used map[string]struct{}) (string, error) {
	portable, err := attachmentfiles.PortableFilename(filename)
	if err != nil {
		return "", err
	}
	extension := filepath.Ext(portable)
	stem := strings.TrimSuffix(portable, extension)
	if stem == "" {
		stem = portable
		extension = ""
	}
	for sequence := 1; ; sequence++ {
		candidate := portable
		if sequence > 1 {
			candidate = fmt.Sprintf("%s-%d%s", stem, sequence, extension)
		}
		key := strings.ToLower(candidate)
		if _, exists := used[key]; exists {
			continue
		}
		used[key] = struct{}{}
		return candidate, nil
	}
}

func downloadDraftExportAttachment(ctx context.Context, root *os.Root, path, sourceURL string) (int64, error) {
	file, err := root.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, apierr.ErrAPI(0, fmt.Sprintf("could not create exported attachment: %v", err))
	}
	written, _, downloadErr := sdk.DownloadBlob(ctx, sourceURL, file)
	closeErr := file.Close()
	if downloadErr != nil {
		return written, apierr.FromSDK(downloadErr)
	}
	if closeErr != nil {
		return written, apierr.ErrAPI(0, fmt.Sprintf("could not close exported attachment: %v", closeErr))
	}
	return written, nil
}

func writePrivateExportFile(root *os.Root, path string, data []byte) error {
	file, err := root.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return apierr.ErrAPI(0, fmt.Sprintf("could not create export file: %v", err))
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return apierr.ErrAPI(0, fmt.Sprintf("could not write export file: %v", err))
	}
	if err := file.Close(); err != nil {
		return apierr.ErrAPI(0, fmt.Sprintf("could not close export file: %v", err))
	}
	return nil
}

func sha256File(root *os.Root, path string) (string, error) {
	file, err := root.Open(path)
	if err != nil {
		return "", apierr.ErrAPI(0, fmt.Sprintf("could not verify exported attachment: %v", err))
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil {
		return "", apierr.ErrAPI(0, fmt.Sprintf("could not verify exported attachment: %v", copyErr))
	}
	if closeErr != nil {
		return "", apierr.ErrAPI(0, fmt.Sprintf("could not close exported attachment: %v", closeErr))
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func commitDraftExportDirectory(staging, destination string) error {
	if err := commitDraftExportDirectoryNoReplace(staging, destination); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return apierr.ErrUsage(fmt.Sprintf("destination already exists: %s (use --force to replace a complete export of this draft)", destination))
		}
		return apierr.ErrAPI(0, fmt.Sprintf("could not publish draft export: %v", err))
	}
	return nil
}

func replaceDraftExportDirectory(staging, destination string, draftID int64) error {
	if err := validateExistingDraftExport(destination, draftID); err != nil {
		return err
	}
	backup, err := os.MkdirTemp(filepath.Dir(destination), ".hey-draft-export-previous-*")
	if err != nil {
		return apierr.ErrAPI(0, fmt.Sprintf("could not reserve prior-export path: %v", err))
	}
	if err := os.Remove(backup); err != nil {
		return apierr.ErrAPI(0, fmt.Sprintf("could not prepare prior-export path: %v", err))
	}
	if err := commitDraftExportDirectoryNoReplace(destination, backup); err != nil {
		return apierr.ErrAPI(0, fmt.Sprintf("could not preserve prior export: %v", err))
	}
	if err := validateExistingDraftExport(backup, draftID); err != nil {
		rollbackErr := commitDraftExportDirectoryNoReplace(backup, destination)
		if rollbackErr != nil {
			return apierr.ErrAPI(0, fmt.Sprintf("the destination changed while export replacement began; the moved directory remains at %s because rollback failed: %v", backup, rollbackErr))
		}
		return apierr.ErrUsage("the destination changed while export replacement began; it was restored and nothing was replaced")
	}
	if err := commitDraftExportDirectoryNoReplace(staging, destination); err != nil {
		rollbackErr := commitDraftExportDirectoryNoReplace(backup, destination)
		if rollbackErr != nil {
			return apierr.ErrAPI(0, fmt.Sprintf("could not publish draft export: %v; prior export remains at %s because rollback failed: %v", err, backup, rollbackErr))
		}
		return apierr.ErrAPI(0, fmt.Sprintf("could not publish draft export: %v", err))
	}
	if err := os.RemoveAll(backup); err != nil {
		return apierr.ErrAPI(0, fmt.Sprintf("draft export was published at %s, but the prior export could not be removed from %s: %v", destination, backup, err))
	}
	return nil
}

func validateExistingDraftExport(destination string, draftID int64) error {
	info, err := os.Lstat(destination)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return invalidExistingDraftExport(destination)
	}
	root, err := os.OpenRoot(destination)
	if err != nil {
		return invalidExistingDraftExport(destination)
	}
	defer func() { _ = root.Close() }()
	manifestData, err := readLimitedRegularFile(root, "draft.json", draftExportManifestMaxBytes)
	if err != nil {
		return invalidExistingDraftExport(destination)
	}
	var manifest draftExportManifest
	if unmarshalErr := json.Unmarshal(manifestData, &manifest); unmarshalErr != nil || manifest.Format != draftExportFormat || manifest.DraftID != draftID || manifest.HTMLPath != "draft.html" {
		return invalidExistingDraftExport(destination)
	}

	rootEntries, err := fs.ReadDir(root.FS(), ".")
	if err != nil || len(rootEntries) != 3 {
		return invalidExistingDraftExport(destination)
	}
	wantedRoot := map[string]bool{"draft.html": false, "draft.json": false, "attachments": true}
	for _, entry := range rootEntries {
		wantDirectory, ok := wantedRoot[entry.Name()]
		if !ok {
			return invalidExistingDraftExport(destination)
		}
		entryInfo, infoErr := entry.Info()
		if infoErr != nil || entryInfo.Mode()&os.ModeSymlink != 0 || entryInfo.IsDir() != wantDirectory || (!wantDirectory && !entryInfo.Mode().IsRegular()) {
			return invalidExistingDraftExport(destination)
		}
	}

	expectedAttachments := make(map[string]struct{}, len(manifest.Attachments))
	for _, attachment := range manifest.Attachments {
		localPath := filepath.FromSlash(attachment.Path)
		if filepath.IsAbs(localPath) || filepath.Dir(localPath) != "attachments" || filepath.Base(localPath) == "." || filepath.Base(localPath) == ".." {
			return invalidExistingDraftExport(destination)
		}
		filename := filepath.Base(localPath)
		if _, duplicate := expectedAttachments[filename]; duplicate {
			return invalidExistingDraftExport(destination)
		}
		expectedAttachments[filename] = struct{}{}
	}
	attachmentEntries, err := fs.ReadDir(root.FS(), "attachments")
	if err != nil || len(attachmentEntries) != len(expectedAttachments) {
		return invalidExistingDraftExport(destination)
	}
	for _, entry := range attachmentEntries {
		_, expected := expectedAttachments[entry.Name()]
		entryInfo, infoErr := entry.Info()
		if !expected || infoErr != nil || !entryInfo.Mode().IsRegular() || entryInfo.Mode()&os.ModeSymlink != 0 {
			return invalidExistingDraftExport(destination)
		}
	}
	return nil
}

func readLimitedRegularFile(root *os.Root, path string, limit int64) ([]byte, error) {
	info, err := root.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > limit {
		return nil, fs.ErrInvalid
	}
	file, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if int64(len(data)) > limit {
		return nil, fs.ErrInvalid
	}
	return data, nil
}

func invalidExistingDraftExport(destination string) error {
	return apierr.ErrUsage(fmt.Sprintf("--force only replaces a complete %s export of this same draft: %s", draftExportFormat, destination))
}
