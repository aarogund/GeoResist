package utils

import (
	"context"
	"net/http"
	"strings"
)

type contextKey string

const UserContextKey = contextKey("user")

func JWTMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		// Try Authorization header first
		authHeader := r.Header.Get("Authorization")

		var tokenStr string

		if authHeader != "" {

			parts := strings.Split(authHeader, " ")

			if len(parts) != 2 {
				http.Error(w, "invalid token format", http.StatusUnauthorized)
				return
			}

			tokenStr = parts[1]

		} else {

			// fallback to cookie
			cookie, err := r.Cookie("token")
			if err != nil {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			tokenStr = cookie.Value
		}

		claims, err := VerifyToken(tokenStr)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// attach user ID to request context
		ctx := context.WithValue(r.Context(), UserContextKey, claims.UserID)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
