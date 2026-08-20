package folders

import (
	"context"
	"regexp"
	"strconv"

	"github.com/basecamp/hey-sdk/go/pkg/generated"
	hey "github.com/basecamp/hey-sdk/go/pkg/hey"
)

// List returns the folders carried by HEY's typed navigation payload.
func List(ctx context.Context, client *hey.Client) ([]generated.Folder, error) {
	navigation, err := client.Identity().GetNavigation(ctx)
	if err != nil {
		return nil, err
	}
	return FromNavigation(navigation), nil
}

// FromNavigation returns each concrete folder entry from HEY's Labels navigation group.
func FromNavigation(navigation *generated.NavigationResponse) []generated.Folder {
	if navigation == nil {
		return nil
	}

	var result []generated.Folder
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
			result = append(result, generated.Folder{Id: id, Name: entry.Title, AppUrl: entry.AppUrl})
		}
	}
	return result
}

var folderPath = regexp.MustCompile(`/folders/(\d+)(?:$|[/?#])`)

const (
	navigationIcon  = "folders"
	navigationTitle = "Labels"
)
