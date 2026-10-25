package utils

import (
	"context"
	"georesist/database"
	"net/http"
)

type contextKey string

const UserContextKey = contextKey("user")

func JWTMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		// 1. Read the cookie
		cookie, err := r.Cookie("token")
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// 2. Verify the token signature
		claims, err := VerifyToken(cookie.Value)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// 3. NEW: Check if token is blacklisted
		var isBlacklisted bool
		err = database.DB.QueryRow(
			"SELECT EXISTS(SELECT 1 FROM token_blacklisted WHERE jti = $1)",
			claims.ID,
		).Scan(&isBlacklisted)

		if err != nil {
			http.Error(w, "Database error", http.StatusInternalServerError)
			return
		}

		// If token is blacklisted, reject the request
		if isBlacklisted {
			ClearCookie(w)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// 4. Set context and proceed
		ctx := context.WithValue(r.Context(), UserContextKey, int(claims.UserID))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
