package utils

import (
	"context"
	"net/http"
)

type contextKey string

const UserContextKey = contextKey("user")

func JWTMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		cookie, err := r.Cookie("token")

		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		claims, err := VerifyToken(cookie.Value)

		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		ctx := context.WithValue(r.Context(), UserContextKey, int(claims.UserID))

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
