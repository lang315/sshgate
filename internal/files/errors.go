package files

// Errors keeps the first MaxErrors messages and counts all of them.
type Errors struct {
	List  []string `json:"errors"`
	Count int      `json:"errorCount"`
}

func (e *Errors) Add(p string, err error) {
	e.Count++
	if len(e.List) < MaxErrors {
		e.List = append(e.List, p+": "+err.Error())
	}
}
