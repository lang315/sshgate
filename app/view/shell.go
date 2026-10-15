package view

// Shell is what the app shows outside its window: the tray's pending
// count, a notification per AI request, and the app-wide theme ("dark",
// "light" or "auto"). main implements it with mygo; tests fake it.
type Shell interface {
	SetPending(n int)
	Notify(id, title, body string)
	SetTheme(pref string)
}
