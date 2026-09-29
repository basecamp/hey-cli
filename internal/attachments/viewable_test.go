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
