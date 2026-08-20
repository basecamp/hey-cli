# hey-cli accent overlay, rendered by omarchy-theme-set from colors.toml.
# Managed by `hey setup omarchy` — edits here are overwritten on the next run.
# Override any key in your theme's own hey.toml; anything missing keeps the ANSI default.
mode = "{{ mode }}"
accent = "{{ accent }}"
selection = "{{ selection }}"
muted = "{{ muted }}"
foreground = "{{ foreground }}"
error = "{{ red }}"

# Reference colors the accent readability gate compares against. A rendered
# hey.toml is the only theme file the TUI reads, so it has to carry them;
# an unrendered {{ key }} is simply ignored.
bright_foreground = "{{ bright_foreground }}"
background = "{{ background }}"
blue = "{{ blue }}"
bright_blue = "{{ bright_blue }}"
