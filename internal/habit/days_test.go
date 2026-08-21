package habit

import (
	"reflect"
	"testing"
)

func TestParseDays(t *testing.T) {
	days, err := ParseDays("Friday, monday;3 1")
	if err != nil {
		t.Fatal(err)
	}
	if want := []int32{1, 3, 5}; !reflect.DeepEqual(days, want) {
		t.Errorf("days = %v, want %v", days, want)
	}
}

func TestParseDaysRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"", "7", "weekday"} {
		if _, err := ParseDays(value); err == nil {
			t.Errorf("ParseDays(%q) succeeded", value)
		}
	}
}
