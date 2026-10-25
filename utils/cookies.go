package utils

import "net/http"

func ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:   "token",
		Value:  "",
		MaxAge: -1, // Delete cookie
		Path:   "/",
	})
}
