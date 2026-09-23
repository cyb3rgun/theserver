package admin

import (
	"net/http"
	"slices"
)

// The theme of the admin pages (D-073): auto follows the browser and the
// operating system, light and dark are the manual choices. The choice lives
// in a cookie beside the language cookie, and the page carries it as
// data-theme on the html element, so the first paint is already in the
// right theme and a dark screen never flashes white.

// ThemeCookieName is the cookie that holds the theme an admin chose.
const ThemeCookieName = "theserver_theme"

// The themes an admin can choose.
const (
	ThemeAuto  = "auto"
	ThemeLight = "light"
	ThemeDark  = "dark"
)

// Themes lists the choices in the order the switch shows them.
func Themes() []string { return []string{ThemeAuto, ThemeLight, ThemeDark} }

// theme reads the theme of a request: the cookie when it names one of the
// three, auto otherwise.
func (a *Admin) theme(r *http.Request) string {
	cookie, err := r.Cookie(ThemeCookieName)
	if err != nil || !slices.Contains(Themes(), cookie.Value) {
		return ThemeAuto
	}
	return cookie.Value
}

// setTheme keeps the chosen theme in its cookie for as long as a login
// lasts, and sends the browser back to the page it came from.
func (a *Admin) setTheme(w http.ResponseWriter, r *http.Request) {
	theme := r.PostFormValue("theme")
	if slices.Contains(Themes(), theme) {
		http.SetCookie(w, &http.Cookie{
			Name:     ThemeCookieName,
			Value:    theme,
			Path:     cookiePath,
			MaxAge:   int(a.sessionLifetime().Seconds()),
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteStrictMode,
		})
	}
	http.Redirect(w, r, backTo(r.PostFormValue("back")), http.StatusSeeOther)
}
