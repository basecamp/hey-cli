package cmd

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basecamp/hey-cli/internal/apierr"
)

type contactPhotoServer struct {
	server   *httptest.Server
	recorded *recordedContacts
	contact  string
}

func newContactPhotoServer(t *testing.T) *contactPhotoServer {
	t.Helper()
	state := &contactPhotoServer{
		recorded: &recordedContacts{statuses: make(map[string]int)},
		contact:  `{"id":7,"account_id":1,"name":"Jane Doe","email_address":"jane@example.com","contactable_type":"Person","avatar_url":"https://app.hey.com/contacts/7/avatar"}`,
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body := new(bytes.Buffer)
		_, _ = body.ReadFrom(req.Body)
		state.recorded.mu.Lock()
		state.recorded.requests = append(state.recorded.requests, recordedContactRequest{
			Method: req.Method, Path: req.URL.Path, Query: req.URL.RawQuery, Body: body.Bytes(),
		})
		status := state.recorded.statuses[req.Method+" "+req.URL.Path]
		state.recorded.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if status != 0 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"request failed"}`))
			return
		}
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/contacts/7.json":
			_, _ = w.Write([]byte(state.contact))
		case req.Method == http.MethodPost && req.URL.Path == "/rails/active_storage/direct_uploads.json":
			_, _ = w.Write([]byte(`{"signed_id":"signed-avatar","attachable_sgid":"sgid-avatar","direct_upload":{"url":"` +
				server.URL + `/storage/avatar","headers":{"Content-Type":"image/png"}}}`))
		case req.Method == http.MethodPut && req.URL.Path == "/storage/avatar":
			w.WriteHeader(http.StatusNoContent)
		case req.Method == http.MethodPut && req.URL.Path == "/contacts/7/uploaded_avatar.json":
			_, _ = w.Write([]byte(`{"id":7,"account_id":1,"name":"Jane Doe","email_address":"jane@example.com","contactable_type":"Person","avatar_url":"https://app.hey.com/contacts/7/avatar?v=2"}`))
		case req.Method == http.MethodDelete && req.URL.Path == "/contacts/7/uploaded_avatar.json":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, req)
		}
	}))
	state.server = server
	t.Cleanup(server.Close)
	return state
}

func (s *contactPhotoServer) requestLines() []string {
	requests := s.recorded.snapshot()
	lines := make([]string, 0, len(requests))
	for _, request := range requests {
		lines = append(lines, request.Method+" "+request.Path)
	}
	return lines
}

func writePhotoFixture(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func pngPhoto(t *testing.T) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, 4, 3))
	picture.Set(1, 1, color.RGBA{R: 200, G: 120, B: 40, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func jpegPhoto(t *testing.T) []byte {
	t.Helper()
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, image.NewGray(image.Rect(0, 0, 4, 3)), nil); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func TestContactPhotoSetUploadsThenAttachesTheSignedBlob(t *testing.T) {
	state := newContactPhotoServer(t)
	photo := writePhotoFixture(t, "jane-doe.png", pngPhoto(t))

	resp, err := runContacts(t, state.server, "photo", "set", "7", photo)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"GET /contacts/7.json",
		"POST /rails/active_storage/direct_uploads.json",
		"PUT /storage/avatar",
		"PUT /contacts/7/uploaded_avatar.json",
	}
	if got := state.requestLines(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	requests := state.recorded.snapshot()
	var reservation struct {
		Blob struct {
			Filename    string `json:"filename"`
			ContentType string `json:"content_type"`
			ByteSize    int64  `json:"byte_size"`
		} `json:"blob"`
	}
	if err := json.Unmarshal(requests[1].Body, &reservation); err != nil {
		t.Fatal(err)
	}
	if reservation.Blob.Filename != "jane-doe.png" || reservation.Blob.ContentType != "image/png" || reservation.Blob.ByteSize != int64(len(pngPhoto(t))) {
		t.Errorf("direct upload blob = %+v", reservation.Blob)
	}
	if !bytes.Equal(requests[2].Body, pngPhoto(t)) {
		t.Error("the storage upload did not carry the photo's bytes")
	}
	if got, want := string(requests[3].Body), `{"contact":{"uploaded_avatar":"signed-avatar"}}`; got != want {
		t.Errorf("avatar body = %s, want %s", got, want)
	}

	contact := decodeContactData[struct {
		ID        int64  `json:"id"`
		AvatarURL string `json:"avatar_url"`
	}](t, resp.Data)
	if contact.ID != 7 || contact.AvatarURL != "https://app.hey.com/contacts/7/avatar?v=2" {
		t.Errorf("data = %+v, want the contact HEY answered", contact)
	}
	if resp.Summary != "Contact photo saved" {
		t.Errorf("summary = %q", resp.Summary)
	}
}

func TestContactPhotoSetJudgesTheBytesNotTheName(t *testing.T) {
	tests := []struct {
		name            string
		filename        string
		content         func(*testing.T) []byte
		wantContentType string
	}{
		{name: "jpeg", filename: "jane-doe.jpg", content: jpegPhoto, wantContentType: "image/jpeg"},
		{name: "png named jpg", filename: "jane-doe.jpg", content: pngPhoto, wantContentType: "image/png"},
		{name: "jpeg with no extension", filename: "jane-doe", content: jpegPhoto, wantContentType: "image/jpeg"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := newContactPhotoServer(t)
			photo := writePhotoFixture(t, tc.filename, tc.content(t))
			if _, err := runContacts(t, state.server, "photo", "set", "7", photo); err != nil {
				t.Fatal(err)
			}
			var reservation struct {
				Blob struct {
					ContentType string `json:"content_type"`
				} `json:"blob"`
			}
			if err := json.Unmarshal(state.recorded.snapshot()[1].Body, &reservation); err != nil {
				t.Fatal(err)
			}
			if reservation.Blob.ContentType != tc.wantContentType {
				t.Errorf("declared content type = %q, want %q", reservation.Blob.ContentType, tc.wantContentType)
			}
		})
	}
}

func TestContactPhotoSetRefusesBadFilesBeforeAnyRequest(t *testing.T) {
	pngSignature := []byte("\x89PNG\r\n\x1a\n")
	tests := []struct {
		name    string
		path    func(t *testing.T) string
		message string
	}{
		{name: "missing file", path: func(t *testing.T) string { return filepath.Join(t.TempDir(), "jane-doe.png") }, message: "could not open photo"},
		{name: "directory", path: func(t *testing.T) string { return t.TempDir() }, message: "not a regular file"},
		{name: "empty", path: func(t *testing.T) string { return writePhotoFixture(t, "jane-doe.png", nil) }, message: "is empty"},
		{name: "heic from a phone", path: func(t *testing.T) string {
			return writePhotoFixture(t, "IMG_0042.HEIC", append([]byte("\x00\x00\x00\x18ftypheic\x00\x00\x00\x00mif1heic"), make([]byte, 64)...))
		}, message: "not a PNG or JPEG"},
		{name: "gif", path: func(t *testing.T) string {
			return writePhotoFixture(t, "jane-doe.gif", []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"))
		}, message: "is image/gif, not a PNG or JPEG"},
		{name: "pdf named png", path: func(t *testing.T) string {
			return writePhotoFixture(t, "jane-doe.png", []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\n"))
		}, message: "is application/pdf, not a PNG or JPEG"},
		{name: "text named jpg", path: func(t *testing.T) string {
			return writePhotoFixture(t, "jane-doe.jpg", []byte("Jane Doe, Head of Partnerships"))
		}, message: "not a PNG or JPEG"},
		{name: "png signature with a broken header", path: func(t *testing.T) string {
			return writePhotoFixture(t, "jane-doe.png", append(pngSignature, []byte("this is not an IHDR chunk")...))
		}, message: "is not a readable png image"},
		{name: "png cut off after its signature", path: func(t *testing.T) string {
			return writePhotoFixture(t, "jane-doe.png", pngSignature)
		}, message: "is not a readable png image"},
		{name: "jpeg cut off before its frame header", path: func(t *testing.T) string {
			return writePhotoFixture(t, "jane-doe.jpg", jpegPhoto(t)[:24])
		}, message: "is not a readable jpeg image"},
		{name: "over the size limit", path: func(t *testing.T) string {
			path := writePhotoFixture(t, "jane-doe.png", pngPhoto(t))
			if err := os.Truncate(path, contactPhotoMaxBytes+1); err != nil {
				t.Fatal(err)
			}
			return path
		}, message: "exceeds the 40 MiB contact photo limit"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := newContactPhotoServer(t)
			_, err := runContacts(t, state.server, "photo", "set", "7", tc.path(t))
			var cliErr *apierr.Error
			if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage {
				t.Fatalf("error = %v, want usage error", err)
			}
			if !strings.Contains(cliErr.Message, tc.message) {
				t.Errorf("message = %q, want it to contain %q", cliErr.Message, tc.message)
			}
			if requests := state.requestLines(); len(requests) != 0 {
				t.Fatalf("a refused photo sent requests: %v", requests)
			}
		})
	}
}

func TestContactPhotoSetAcceptsAPhotoAtTheSizeLimit(t *testing.T) {
	photo := writePhotoFixture(t, "jane-doe.png", pngPhoto(t))
	// A PNG decoder stops at IEND, so padding the file out keeps it a valid photo.
	if err := os.Truncate(photo, contactPhotoMaxBytes); err != nil {
		t.Fatal(err)
	}
	prepared, err := openContactPhoto(photo)
	if err != nil {
		t.Fatalf("a photo of exactly %d bytes was refused: %v", contactPhotoMaxBytes, err)
	}
	closePreparedAttachments([]preparedAttachment{prepared})
}

// pngHeader is the start of a PNG claiming the given dimensions: the signature and a
// well-formed IHDR, which is all a decoder reads to answer them. It lets a test name a
// photo of any size without allocating its pixels.
func pngHeader(width, height uint32) []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], width)
	binary.BigEndian.PutUint32(ihdr[4:], height)
	ihdr[8] = 8 // bit depth
	ihdr[9] = 6 // RGBA
	chunk := append([]byte("IHDR"), ihdr...)
	header := []byte("\x89PNG\r\n\x1a\n")
	header = binary.BigEndian.AppendUint32(header, uint32(len(ihdr)))
	header = append(header, chunk...)
	return binary.BigEndian.AppendUint32(header, crc32.ChecksumIEEE(chunk))
}

type exifByteOrder interface {
	binary.ByteOrder
	binary.AppendByteOrder
}

// jpegHeader is the start of a JPEG claiming the given dimensions and component count
// (1 gray, 3 color, 4 CMYK), with an EXIF orientation when orientation is non-zero, in the
// byte order named: everything a decoder reads before the scan, and no pixels.
func jpegHeader(width, height uint16, components byte, orientation uint16, order exifByteOrder) []byte {
	header := []byte{0xFF, 0xD8}
	if orientation != 0 {
		tiff := []byte("MM")
		if order == binary.LittleEndian {
			tiff = []byte("II")
		}
		tiff = order.AppendUint16(tiff, 42)
		tiff = order.AppendUint32(tiff, 8)      // IFD0 offset
		tiff = order.AppendUint16(tiff, 2)      // entries
		tiff = order.AppendUint16(tiff, 0x010F) // Make, which is not the orientation
		tiff = order.AppendUint16(tiff, 2)
		tiff = order.AppendUint32(tiff, 4)
		tiff = append(tiff, "HEY\x00"...)
		tiff = order.AppendUint16(tiff, 0x0112)
		tiff = order.AppendUint16(tiff, 3) // SHORT
		tiff = order.AppendUint32(tiff, 1)
		tiff = order.AppendUint16(tiff, orientation)
		tiff = order.AppendUint16(tiff, 0)
		tiff = order.AppendUint32(tiff, 0) // no next IFD
		app1 := append([]byte("Exif\x00\x00"), tiff...)
		header = append(header, 0xFF, 0xE1)
		header = binary.BigEndian.AppendUint16(header, uint16(len(app1)+2))
		header = append(header, app1...)
	}
	sof := []byte{8}
	sof = binary.BigEndian.AppendUint16(sof, height)
	sof = binary.BigEndian.AppendUint16(sof, width)
	sof = append(sof, components)
	for id := byte(1); id <= components; id++ {
		sof = append(sof, id, 0x11, 0)
	}
	header = append(header, 0xFF, 0xC0)
	header = binary.BigEndian.AppendUint16(header, uint16(len(sof)+2))
	header = append(header, sof...)
	return append(header, 0xFF, 0xDA, 0x00, 0x08, 0x01, 0x01, 0x00, 0x00, 0x3F, 0x00)
}

func TestJPEGOrientationReadsTheExifTag(t *testing.T) {
	for _, order := range []exifByteOrder{binary.BigEndian, binary.LittleEndian} {
		for orientation := uint16(1); orientation <= 8; orientation++ {
			got := jpegOrientation(bytes.NewReader(jpegHeader(640, 480, 3, orientation, order)))
			if got != int(orientation) {
				t.Errorf("%v orientation %d read as %d", order, orientation, got)
			}
		}
	}
	upright := jpegHeader(640, 480, 3, 6, binary.BigEndian)
	for name, content := range map[string][]byte{
		"no exif":             jpegHeader(640, 480, 3, 0, nil),
		"out of range":        jpegHeader(640, 480, 3, 9, binary.BigEndian),
		"a real encoder's":    jpegPhoto(t),
		"not a jpeg":          pngPhoto(t),
		"cut off in the exif": upright[:30],
		"empty":               nil,
	} {
		if got := jpegOrientation(bytes.NewReader(content)); got != 1 {
			t.Errorf("%s: orientation = %d, want 1", name, got)
		}
	}
}

func TestJPEGOrientationSkipsFillBytesBeforeAMarker(t *testing.T) {
	rotated := jpegHeader(640, 480, 3, 6, binary.BigEndian)
	for fill := 1; fill <= 3; fill++ {
		padded := append([]byte{0xFF, 0xD8}, bytes.Repeat([]byte{0xFF}, fill)...)
		padded = append(padded, rotated[2:]...)
		if got := jpegOrientation(bytes.NewReader(padded)); got != 6 {
			t.Errorf("%d fill bytes: orientation = %d, want 6", fill, got)
		}
	}
}

func TestJPEGOrientationSurvivesEveryTruncation(t *testing.T) {
	rotated := jpegHeader(640, 480, 3, 6, binary.LittleEndian)
	for cut := range rotated {
		_ = jpegOrientation(bytes.NewReader(rotated[:cut]))
	}
}

// The boundaries below are HEY's, measured by running its variant command under
// hotcell/policy.xml and the cell's limits: 10800×10800 resizes and 11000×11000 does not;
// rotated, 7600×7600 does and 7700×7700 does not; CMYK, 9600×9600 does and 9800×9600 does
// not; rotated CMYK, 6000×6000 does and 7000×7000 does not. Each case sits on the line the
// budget draws between them.
func TestContactPhotoSetRefusesPhotosTooLargeForHEYToResize(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
		hint    string
	}{
		{name: "too wide", content: pngHeader(contactPhotoMaxSide+1, 1), hint: "16000 pixels a side"},
		{name: "too tall", content: jpegHeader(1, contactPhotoMaxSide+1, 3, 0, nil), hint: "16000 pixels a side"},
		{name: "a 200 megapixel phone photo", content: jpegHeader(16_320, 12_240, 3, 0, nil), hint: "16000 pixels a side"},
		{name: "upright png", content: pngHeader(16_000, 7_341), hint: "a photo of at most 117.4 megapixels"},
		{name: "upright jpeg", content: jpegHeader(16_000, 7_341, 3, 1, binary.BigEndian), hint: "a photo of at most 117.4 megapixels"},
		{name: "grayscale jpeg", content: jpegHeader(16_000, 7_341, 1, 0, nil), hint: "a photo of at most 117.4 megapixels"},
		{name: "a 108 megapixel portrait", content: jpegHeader(12_000, 9_000, 3, 6, binary.LittleEndian), hint: "a photo that its EXIF orientation rotates or flips of at most 58.7 megapixels"},
		{name: "rotated jpeg", content: jpegHeader(8_000, 7_341, 3, 8, binary.BigEndian), hint: "rotates or flips of at most 58.7 megapixels"},
		{name: "mirrored jpeg", content: jpegHeader(8_000, 7_341, 3, 2, binary.BigEndian), hint: "rotates or flips of at most 58.7 megapixels"},
		{name: "cmyk jpeg", content: jpegHeader(16_000, 5_873, 4, 0, nil), hint: "a CMYK JPEG of at most 94.0 megapixels"},
		{name: "rotated cmyk jpeg", content: jpegHeader(8_000, 5_873, 4, 6, binary.BigEndian), hint: "a CMYK JPEG that its EXIF orientation rotates or flips of at most 47.0 megapixels"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := newContactPhotoServer(t)
			photo := writePhotoFixture(t, "jane-doe.jpg", tc.content)
			_, err := runContacts(t, state.server, "photo", "set", "7", photo)
			var cliErr *apierr.Error
			if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeUsage {
				t.Fatalf("error = %v, want usage error", err)
			}
			if !strings.Contains(cliErr.Message, "too large for HEY to resize") || !strings.Contains(cliErr.Hint, tc.hint) {
				t.Errorf("message = %q, hint = %q, want hint containing %q", cliErr.Message, cliErr.Hint, tc.hint)
			}
			if requests := state.requestLines(); len(requests) != 0 {
				t.Fatalf("a refused photo sent requests: %v", requests)
			}
		})
	}
}

func TestContactPhotoSetAcceptsPhotosAtTheResizeLimits(t *testing.T) {
	for name, content := range map[string][]byte{
		"widest png":              pngHeader(contactPhotoMaxSide, 1),
		"tallest jpeg":            jpegHeader(1, contactPhotoMaxSide, 3, 0, nil),
		"largest upright png":     pngHeader(16_000, 7_340),
		"largest upright jpeg":    jpegHeader(16_000, 7_340, 3, 1, binary.LittleEndian),
		"png whatever its exif":   pngHeader(16_000, 7_340),
		"largest rotated jpeg":    jpegHeader(8_000, 7_340, 3, 6, binary.BigEndian),
		"largest cmyk jpeg":       jpegHeader(16_000, 5_872, 4, 0, nil),
		"largest rotated cmyk":    jpegHeader(8_000, 5_872, 4, 5, binary.LittleEndian),
		"a 48 megapixel portrait": jpegHeader(8_064, 6_048, 3, 6, binary.LittleEndian),
	} {
		photo := writePhotoFixture(t, "jane-doe.jpg", content)
		prepared, err := openContactPhoto(photo)
		if err != nil {
			t.Errorf("%s was refused: %v", name, err)
			continue
		}
		if offset, _ := prepared.file.Seek(0, io.SeekCurrent); offset != 0 {
			t.Errorf("%s: the photo was left at offset %d, not its start", name, offset)
		}
		closePreparedAttachments([]preparedAttachment{prepared})
	}
}

func TestContactPhotoSetRejectsBadArgumentsBeforeAnyRequest(t *testing.T) {
	photo := writePhotoFixture(t, "jane-doe.png", pngPhoto(t))
	tests := []struct {
		name string
		args []string
	}{
		{name: "no arguments", args: []string{"photo", "set"}},
		{name: "no file", args: []string{"photo", "set", "7"}},
		{name: "extra argument", args: []string{"photo", "set", "7", photo, photo}},
		{name: "malformed id", args: []string{"photo", "set", "jane", photo}},
		{name: "zero id", args: []string{"photo", "set", "0", photo}},
		{name: "remove without id", args: []string{"photo", "remove"}},
		{name: "remove malformed id", args: []string{"photo", "remove", "jane"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := newContactPhotoServer(t)
			// A wrong count is answered by the shared argument check, the rest as usage errors.
			if _, err := runContacts(t, state.server, tc.args...); err == nil {
				t.Fatal("expected an error")
			}
			if requests := state.requestLines(); len(requests) != 0 {
				t.Fatalf("bad arguments sent requests: %v", requests)
			}
		})
	}
}

func TestContactPhotoSetDoesNotUploadForAMissingContact(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusNoContent} {
		state := newContactPhotoServer(t)
		state.recorded.statuses["GET /contacts/7.json"] = status
		photo := writePhotoFixture(t, "jane-doe.png", pngPhoto(t))

		resp, err := runContacts(t, state.server, "photo", "set", "7", photo)
		var cliErr *apierr.Error
		if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeNotFound {
			t.Fatalf("status %d: error = %v, want not_found", status, err)
		}
		if resp.OK {
			t.Errorf("status %d: response = %+v, want failure", status, resp)
		}
		if got := state.requestLines(); len(got) != 1 {
			t.Errorf("status %d: requests = %v, want only the contact read", status, got)
		}
	}
}

func TestContactPhotoSaysWhyHEYRefusedTheContact(t *testing.T) {
	tests := []struct {
		name      string
		args      func(t *testing.T) []string
		statusKey string
	}{
		{name: "set", statusKey: "PUT /contacts/7/uploaded_avatar.json", args: func(t *testing.T) []string {
			return []string{"photo", "set", "7", writePhotoFixture(t, "jane-doe.png", pngPhoto(t))}
		}},
		{name: "remove", statusKey: "DELETE /contacts/7/uploaded_avatar.json", args: func(*testing.T) []string {
			return []string{"photo", "remove", "7"}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := newContactPhotoServer(t)
			state.recorded.statuses[tc.statusKey] = http.StatusNotFound
			_, err := runContacts(t, state.server, tc.args(t)...)
			var cliErr *apierr.Error
			if !errors.As(err, &cliErr) || cliErr.Code != apierr.CodeNotFound {
				t.Fatalf("error = %v, want not_found", err)
			}
			if cliErr.Hint != contactPhotoNotEditableHint {
				t.Errorf("hint = %q, want %q", cliErr.Hint, contactPhotoNotEditableHint)
			}
		})
	}
}

func TestContactPhotoSetStopsWhenTheUploadFails(t *testing.T) {
	for _, statusKey := range []string{"POST /rails/active_storage/direct_uploads.json", "PUT /storage/avatar"} {
		state := newContactPhotoServer(t)
		state.recorded.statuses[statusKey] = http.StatusUnprocessableEntity
		photo := writePhotoFixture(t, "jane-doe.png", pngPhoto(t))

		if _, err := runContacts(t, state.server, "photo", "set", "7", photo); err == nil {
			t.Fatalf("%s failing: expected an error", statusKey)
		}
		for _, line := range state.requestLines() {
			if line == "PUT /contacts/7/uploaded_avatar.json" {
				t.Errorf("%s failing: the avatar was still set", statusKey)
			}
		}
	}
}

func TestContactPhotoRemove(t *testing.T) {
	state := newContactPhotoServer(t)
	resp, err := runContacts(t, state.server, "photo", "remove", "7")
	if err != nil {
		t.Fatal(err)
	}
	if got := state.requestLines(); len(got) != 1 || got[0] != "DELETE /contacts/7/uploaded_avatar.json" {
		t.Errorf("requests = %v", got)
	}
	data := decodeContactData[map[string]int64](t, resp.Data)
	if data["id"] != 7 || resp.Summary != "Contact photo removed" {
		t.Errorf("response = %+v", resp)
	}
}

func TestContactPhotoStyledOutput(t *testing.T) {
	state := newContactPhotoServer(t)
	photo := writePhotoFixture(t, "jane-doe.png", pngPhoto(t))

	got, err := runStyledCommand(t, state.server.Config.Handler, "contact", "photo", "set", "7", photo)
	if err != nil {
		t.Fatal(err)
	}
	if want := "Photo saved for contact 7 (Jane Doe). The old photo may still show in places for up to 30 minutes.\n"; got != want {
		t.Errorf("set output = %q, want %q", got, want)
	}

	got, err = runStyledCommand(t, state.server.Config.Handler, "contact", "avatar", "remove", "7")
	if err != nil {
		t.Fatal(err)
	}
	if want := "Photo removed from contact 7.\n"; got != want {
		t.Errorf("remove output = %q, want %q", got, want)
	}
}

func TestContactPhotoStyledOutputSanitizesTheName(t *testing.T) {
	state := newContactPhotoServer(t)
	state.contact = `{"id":7,"name":"Jane \u001b[31mDoe","email_address":"jane@example.com"}`
	photo := writePhotoFixture(t, "jane-doe.png", pngPhoto(t))

	got, err := runStyledCommand(t, state.server.Config.Handler, "contact", "photo", "set", "7", photo)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(got, '\x1b') {
		t.Errorf("output carried an escape sequence: %q", got)
	}
}
