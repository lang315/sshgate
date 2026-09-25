// Sets data-theme before the first paint so the window never flashes the
// wrong theme; App (theme.ts) keeps it in sync afterwards. Loaded from 'self'
// because the CSP forbids inline scripts.
(function () {
  var p = 'auto'
  try { p = localStorage.getItem('ssh-mcp.theme') || 'auto' } catch (e) { /* Auto */ }
  if (p !== 'dark' && p !== 'light') p = matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
  document.documentElement.dataset.theme = p
})()
