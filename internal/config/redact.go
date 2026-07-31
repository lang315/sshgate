package config

import "strings"

type Redactor struct{ secrets []string }

func NewRedactor(secrets ...string) *Redactor {
	var s []string
	for _, x := range secrets {
		if x != "" {
			s = append(s, x)
		}
	}
	return &Redactor{secrets: s}
}

func (r *Redactor) Redact(s string) string {
	for _, sec := range r.secrets {
		s = strings.ReplaceAll(s, sec, "***")
	}
	return s
}
