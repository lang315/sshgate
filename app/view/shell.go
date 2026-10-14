package view

// Shell is what the app shows outside its window: the tray's pending
// count and a notification per AI request. main implements it with mygo;
// tests fake it.
type Shell interface {
	SetPending(n int)
	Notify(id, title, body string)
}
