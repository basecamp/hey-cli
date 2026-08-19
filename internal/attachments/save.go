package attachments

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/basecamp/hey-cli/internal/apierr"
)

type Downloader interface {
	DownloadBlob(context.Context, string, io.Writer) (int64, http.Header, error)
}

func Destination(outputPath, filename string) (string, error) {
	if outputPath == "" {
		return PortableFilename(filename)
	}
	info, err := os.Stat(outputPath)
	if err == nil && info.IsDir() {
		safeFilename, filenameErr := PortableFilename(filename)
		if filenameErr != nil {
			return "", filenameErr
		}
		return filepath.Join(outputPath, safeFilename), nil
	}
	if err != nil && !os.IsNotExist(err) {
		return "", apierr.ErrAPI(0, fmt.Sprintf("could not inspect output path: %v", err))
	}
	return filepath.Clean(outputPath), nil
}

func PortableFilename(filename string) (string, error) {
	filename = strings.TrimSpace(filename)
	filename = path.Base(strings.ReplaceAll(filename, `\`, "/"))
	if filename == "" || filename == "." || filename == ".." || filename == "/" {
		return "", apierr.ErrUsage("attachment has no safe filename")
	}
	filename = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, filename)
	filename = strings.TrimRight(filename, ". ")
	if filename == "" {
		filename = "attachment"
	}
	if windowsReservedFilename(filename) {
		filename = "_" + filename
	}
	return filename, nil
}

func Save(ctx context.Context, downloader Downloader, destination, sourceURL string, force bool) (int64, error) {
	if !force {
		if _, err := os.Lstat(destination); err == nil {
			return 0, apierr.ErrUsage(fmt.Sprintf("destination already exists: %s (use --force to replace it)", destination))
		} else if !os.IsNotExist(err) {
			return 0, apierr.ErrAPI(0, fmt.Sprintf("could not inspect attachment destination: %v", err))
		}
	}

	directory := filepath.Dir(destination)
	temporary, err := os.CreateTemp(directory, ".hey-attachment-*")
	if err != nil {
		return 0, apierr.ErrAPI(0, fmt.Sprintf("could not create temporary attachment file: %v", err))
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()

	written, _, err := downloader.DownloadBlob(ctx, sourceURL, temporary)
	if err != nil {
		_ = temporary.Close()
		return written, err
	}
	if err := temporary.Close(); err != nil {
		return written, apierr.ErrAPI(0, fmt.Sprintf("could not close attachment file: %v", err))
	}

	if force {
		if err := replaceFile(temporaryPath, destination); err != nil {
			return written, apierr.ErrAPI(0, fmt.Sprintf("could not replace attachment file: %v", err))
		}
		return written, nil
	}
	if err := commitFileNoReplace(temporaryPath, destination); os.IsExist(err) {
		return written, apierr.ErrUsage(fmt.Sprintf("destination already exists: %s (use --force to replace it)", destination))
	} else if err != nil {
		return written, apierr.ErrAPI(0, fmt.Sprintf("could not save attachment file: %v", err))
	}
	return written, nil
}

func windowsReservedFilename(filename string) bool {
	stem := filename
	if dot := strings.IndexByte(stem, '.'); dot >= 0 {
		stem = stem[:dot]
	}
	stem = strings.ToUpper(stem)
	if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || stem == "CONIN$" || stem == "CONOUT$" {
		return true
	}
	runes := []rune(stem)
	if len(runes) != 4 || (string(runes[:3]) != "COM" && string(runes[:3]) != "LPT") {
		return false
	}
	if runes[3] == '¹' || runes[3] == '²' || runes[3] == '³' {
		return true
	}
	return runes[3] >= '1' && runes[3] <= '9'
}
