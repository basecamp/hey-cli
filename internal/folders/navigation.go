package folders

import (
	"context"
	"regexp"
	"strconv"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

// Label identifies a user-visible HEY label discovered through navigation.
type Label struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	AppURL string `json:"app_url,omitempty"`
}

// List returns the labels carried by HEY's typed navigation payload.
func List(ctx context.Context, client *hey.Client) ([]Label, error) {
	navigation, err := client.Identity().GetNavigation(ctx)
	if err != nil {
		return nil, err
	}
	return FromNavigation(navigation), nil
}

// FromNavigation returns each concrete label entry from HEY's Labels navigation group.
func FromNavigation(navigation *generated.NavigationResponse) []Label {
	if navigation == nil {
		return nil
	}

	var result []Label
	for _, item := range navigation.Items {
		if item.Icon.Name != navigationIcon && item.Title != navigationTitle {
			continue
		}
		for _, entry := range item.MenuItems {
			match := folderPath.FindStringSubmatch(entry.AppUrl)
			if match == nil {
				continue
			}
			id, err := strconv.ParseInt(match[1], 10, 64)
			if err != nil {
				continue
			}
			result = append(result, Label{ID: id, Name: entry.Title, AppURL: entry.AppUrl})
		}
	}
	return result
}

var folderPath = regexp.MustCompile(`/folders/(\d+)(?:$|[/?#])`)

const (
	navigationIcon  = "folders"
	navigationTitle = "Labels"
)
