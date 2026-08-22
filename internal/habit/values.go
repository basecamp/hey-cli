package habit

import (
	"fmt"
	"strings"
)

// Icon is one of the icons HEY draws a habit with. HEY serves an SVG per icon, which a
// terminal cannot draw, so each carries the emoji that stands in for it. The emoji are
// all two cells wide — see TestEveryHabitEmojiIsTwoCellsWide — because a habit's icon
// sits in lists whose width is measured.
type Icon struct {
	Name  string
	Emoji string
}

// Icons are the habit icons HEY accepts, in the order its own enum declares them.
var Icons = []Icon{
	{"weights", "💪"}, {"art", "🎨"}, {"baseball", "⚾"}, {"basketball", "🏀"},
	{"bed", "😴"}, {"bicycle", "🚲"}, {"brain", "🧠"}, {"camera", "📷"},
	{"cat", "🐱"}, {"church", "⛪"}, {"clean", "🧹"}, {"cook", "🍳"},
	{"dog", "🐶"}, {"football", "🏈"}, {"fruit", "🍎"}, {"game", "🎮"},
	{"garden", "🌻"}, {"guitar", "🎸"}, {"heart", "💗"}, {"hydrate", "💧"},
	{"meditate", "🧘"}, {"money", "💰"}, {"music", "🎵"}, {"piano", "🎹"},
	{"pill", "💊"}, {"plant", "🌱"}, {"read", "📖"}, {"run", "🏃"},
	{"smoke", "🚬"}, {"soccer", "⚽"}, {"study", "📚"}, {"swim", "🌊"},
	{"tea", "🍵"}, {"toothbrush", "🪥"}, {"tree", "🌳"}, {"tv", "📺"},
	{"vegetable", "🥕"}, {"walk", "🚶"}, {"water", "🚰"}, {"write", "📝"},
	{"yoga", "🤸"}, {"heat", "🔥"}, {"ice", "🧊"}, {"lotus", "🌸"},
	{"breathe", "💨"}, {"drink", "🥤"}, {"star", "⭐"},
}

// Colors are the habit colors HEY accepts, in the order its own enum declares them.
var Colors = []string{"blue", "red", "gold", "green", "teal", "purple", "pink", "brown"}

var (
	// IconValues lists the icon names HEY accepts for habits.
	IconValues = iconNames()
	// ColorValues lists the color names HEY accepts for habits.
	ColorValues = strings.Join(Colors, ", ")

	acceptedIcons  = acceptedValues(IconValues)
	acceptedColors = acceptedValues(ColorValues)
)

// EmojiFor answers the emoji standing in for an icon, and nothing for a name HEY does
// not know, so a habit carrying an icon this build has not heard of still lists.
func EmojiFor(icon string) string {
	for _, known := range Icons {
		if known.Name == icon {
			return known.Emoji
		}
	}
	return ""
}

func iconNames() string {
	names := make([]string, len(Icons))
	for i, icon := range Icons {
		names[i] = icon.Name
	}
	return strings.Join(names, ", ")
}

// ValidateIcon accepts an icon name supported by HEY habits.
func ValidateIcon(value string) error {
	if !acceptedIcons[value] {
		return fmt.Errorf("icon must be one of: %s", IconValues)
	}
	return nil
}

// ValidateColor accepts a color name supported by HEY habits.
func ValidateColor(value string) error {
	if !acceptedColors[value] {
		return fmt.Errorf("color must be one of: %s", ColorValues)
	}
	return nil
}

func acceptedValues(values string) map[string]bool {
	accepted := make(map[string]bool)
	for _, value := range strings.Split(values, ", ") {
		accepted[value] = true
	}
	return accepted
}
