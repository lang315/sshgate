package view

// Shell is what the app shows outside its window: the tray's pending
// count, a notification per AI request, the app-wide theme ("dark",
// "light" or "auto"), and the native file pickers. main implements it with
// mygo; tests fake it.
type Shell interface {
	SetPending(n int)
	Notify(id, title, body string)
	SetTheme(pref string)
	// PickPaths asks for local files, or folders when dirs, to upload; none
	// when cancelled. It blocks until the picker closes: call it off the
	// main thread.
	PickPaths(title string, dirs bool) ([]string, error)
	// PickFolder asks for a local folder to download into; "" when
	// cancelled. It blocks like PickPaths.
	PickFolder(title string) (string, error)
}
