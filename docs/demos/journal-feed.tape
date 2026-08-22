# Journal feed usability concept, recorded against the development Haystack.
Output docs/demos/journal-feed.gif

Require ./bin/hey

Set Shell "bash"
Set FontSize 18
Set Width 1100
Set Height 650
Set TypingSpeed 35ms
Set Framerate 30
Set Theme "Catppuccin Mocha"

Type "./bin/hey tui" Enter
Sleep 4s

# Open Journal and browse far enough to load older entries.
Type "J"
Sleep 4s
PageDown@120ms 2
Sleep 3s
Down@100ms 5
Sleep 2s

# Search journal content and open the result.
Type "/"
Sleep 1s
Type "Northstar"
Enter
Sleep 4s
Enter
Sleep 3s
Escape
Sleep 2s
Type "c"
Sleep 4s

# Jump directly to a known date.
Type "g"
Sleep 1s
Type "2026-07-14"
Enter
Sleep 4s
Escape
Sleep 2s

# Add today's entry from the feed.
Type "a"
Sleep 3s
Type "Captured feedback on the new journal feed and planned the next usability pass."
Sleep 2s
Ctrl+S
Sleep 5s

Ctrl+C
Sleep 500ms
Ctrl+C
