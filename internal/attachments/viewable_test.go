package attachments

import "testing"

func TestOpensInViewerOnlyForDocumentsImagesAndMedia(t *testing.T) {
	for filename, want := range map[string]bool{
		"agenda.pdf":          true,
		"Chart.PNG":           true,
		"minutes.docx":        true,
		"invite.ics":          true,
		"recording.m4a":       true,
		"../../notes.txt":     true,
		"Invoice.terminal":    false,
		"setup.command":       false,
		"report.pdf.command":  false,
		"report.pdf.terminal": false,
		"Invoice.terminal. ":  false,
		"Shortcut.webloc":     false,
		"Server.inetloc":      false,
		"Folder.fileloc":      false,
		"Script.scpt":         false,
		"Installer.pkg":       false,
		"Disk.dmg":            false,
		"launcher.desktop":    false,
		"install.sh":          false,
		"tool.AppImage":       false,
		"invoice.exe":         false,
		"invoice.bat":         false,
		"invoice.cmd":         false,
		"invoice.lnk":         false,
		"invoice.url":         false,
		"invoice.hta":         false,
		"invoice.js":          false,
		"invoice.vbs":         false,
		"invoice.ps1":         false,
		"invoice.msi":         false,
		"macro.docm":          false,
		"page.html":           false,
		"drawing.svg":         false,
		"archive.zip":         false,
		"README":              false,
		"":                    false,
		"..":                  false,
	} {
		if got := OpensInViewer(filename); got != want {
			t.Errorf("OpensInViewer(%q) = %v, want %v", filename, got, want)
		}
	}
}

func TestContentOpensInViewerOnlyWhenTheContentsAgree(t *testing.T) {
	for _, tc := range []struct {
		filename, contentType string
		want                  bool
	}{
		{"agenda.pdf", "application/pdf", true},
		{"notes.md", "text/plain", true},
		{"Chart.PNG", " Image/PNG ", true},
		{"photo.jpg", "image/png", true},
		{"minutes.docx", "application/zip", true},
		{"budget.ods", "application/zip", true},
		{"agenda.pdf", "application/vnd.microsoft.portable-executable", false},
		{"agenda.md", "application/x-ms-dos-executable", false},
		{"notes.txt", "application/x-shellscript", false},
		{"notes.txt", "text/x-script.python", false},
		{"photo.jpg", "application/zip", false},
		{"photo.png", "image/svg+xml", false},
		{"photo.png", "application/x-desktop", false},
		{"photo.png", "application/octet-stream", false},
		{"photo.png", "", false},
		{"Invoice.terminal", "text/plain", false},
		{"report.pdf.command", "application/pdf", false},
	} {
		if got := ContentOpensInViewer(tc.filename, tc.contentType); got != tc.want {
			t.Errorf("ContentOpensInViewer(%q, %q) = %v, want %v", tc.filename, tc.contentType, got, tc.want)
		}
	}
}
