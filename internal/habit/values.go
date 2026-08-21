package habit

import (
	"fmt"
	"strings"
)

const (
	// IconValues lists the icon names HEY accepts for habits.
	IconValues = "weights, art, baseball, basketball, bed, bicycle, brain, camera, cat, church, clean, cook, dog, football, fruit, game, garden, guitar, heart, hydrate, meditate, money, music, piano, pill, plant, read, run, smoke, soccer, study, swim, tea, toothbrush, tree, tv, vegetable, walk, water, write, yoga, heat, ice, lotus, breathe, drink, star"
	// ColorValues lists the color names HEY accepts for habits.
	ColorValues = "blue, red, gold, green, teal, purple, pink, brown"
)

var (
	acceptedIcons  = acceptedValues(IconValues)
	acceptedColors = acceptedValues(ColorValues)
)

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
