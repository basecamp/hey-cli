package tui

import "strings"

// Wordmark is the HEY braille-art logo shown in root help and on the setup
// wizard's welcome screen.
const Wordmark = `⠀⠀⠀⠀⠀⠀⣰⠲⣄⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⡟⢳⡀⣏⠀⠘⣆⠀⠀⠀⠀⠀⣤⣤⡄⠀⠀⢠⣤⣤⣤⣤⣤⣤⣤⣤⠀⠀⢀⣤⣤⡄⠀
⠀⣴⢄⢳⠀⠹⣿⠀⠀⠸⣆⠴⠒⢢⡀⢻⣿⡇⠀⠀⢸⣿⣿⡟⠛⠛⠛⢿⣿⣇⠀⣼⣿⡟⠀⠀
⠀⢻⠈⠻⣧⠀⠹⣇⠀⢰⣿⠀⠀⠀⡇⢸⣿⣷⣶⣶⣾⣿⣿⣷⣶⣶⠀⠈⢿⣿⣼⣿⡟⠀⠀⠀
⣶⠺⣧⡀⠙⢧⠀⠉⠀⣸⢸⡆⠀⢸⠁⣼⣿⡏⠉⠉⢹⣿⣿⡏⠉⠉⠀⠀⠈⣿⣿⡟⠀⠀⠀⠀
⠘⣆⠈⠳⠀⠀⠀⠀⠀⢻⢸⠇⢀⡏⠀⣿⣿⡇⠀⠀⢸⣿⣿⣿⣶⣶⣶⡆⠀⣿⣿⡇⠀⠀⠀⠀
⠀⠈⠳⣄⡀⠀⠀⠀⠀⠈⠉⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠉⠙⠚⠃⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀`

// RenderWordmark returns the wordmark, tinted when color is enabled.
func RenderWordmark(colorEnabled bool) string {
	if !colorEnabled {
		return Wordmark
	}
	lines := strings.Split(Wordmark, "\n")
	for i, line := range lines {
		lines[i] = "\033[94m" + line + "\033[0m"
	}
	return strings.Join(lines, "\n")
}
