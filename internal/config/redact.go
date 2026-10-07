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
	s, _ = r.RedactCount(s)
	return s
}

// RedactCount masks every vault secret as "***" and reports how many
// occurrences it replaced.
func (r *Redactor) RedactCount(s string) (string, int) {
	n := 0
	for _, sec := range r.secrets {
		n += strings.Count(s, sec)
		s = strings.ReplaceAll(s, sec, "***")
	}
	return s, n
}
