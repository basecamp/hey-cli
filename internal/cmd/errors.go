package cmd

import "errors"

var errNotLoggedIn = errors.New("not logged in — run `hey login` first")
