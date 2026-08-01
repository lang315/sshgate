package web

import "regexp"

func regexpMustCompile(p string) *regexp.Regexp { return regexp.MustCompile(p) }
