package habit

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const (
	DefaultIcon  = "weights"
	DefaultColor = "blue"
)

var EveryDay = []int32{0, 1, 2, 3, 4, 5, 6}

var weekdayNumbers = map[string]int32{
	"sun": 0, "sunday": 0,
	"mon": 1, "monday": 1,
	"tue": 2, "tues": 2, "tuesday": 2,
	"wed": 3, "weds": 3, "wednesday": 3,
	"thu": 4, "thur": 4, "thurs": 4, "thursday": 4,
	"fri": 5, "friday": 5,
	"sat": 6, "saturday": 6,
}

// ParseDays accepts comma-separated weekday names or numbers from Sunday (0) through Saturday (6).
func ParseDays(value string) ([]int32, error) {
	parts := strings.FieldsFunc(strings.TrimSpace(value), func(r rune) bool {
		return r == ',' || r == ' ' || r == ';'
	})
	if len(parts) == 0 {
		return nil, fmt.Errorf("days must include at least one weekday")
	}

	seen := make(map[int32]bool, 7)
	days := make([]int32, 0, len(parts))
	for _, part := range parts {
		key := strings.ToLower(strings.TrimSpace(part))
		day, ok := weekdayNumbers[key]
		if !ok {
			number, err := strconv.ParseInt(key, 10, 32)
			if err != nil || number < 0 || number > 6 {
				return nil, fmt.Errorf("invalid weekday %q (use Sunday-Saturday or 0-6)", part)
			}
			day = int32(number)
		}
		if !seen[day] {
			seen[day] = true
			days = append(days, day)
		}
	}
	sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })
	return days, nil
}

// FormatDays returns weekdays in the numeric form accepted by HEY.
func FormatDays(days []int32) string {
	values := make([]string, 0, len(days))
	for _, day := range days {
		values = append(values, strconv.FormatInt(int64(day), 10))
	}
	return strings.Join(values, ",")
}
