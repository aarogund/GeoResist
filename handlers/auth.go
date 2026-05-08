package handlers

import (
	"database/sql"
	"georesist/database"
	"georesist/utils"
	"html/template"
	"net/http"
)

func RegisterHandler(w http.ResponseWriter, r *http.Request) {
	// SHOW REGISTRATION PAGE
	if r.Method == "GET" {
		tmpl, err := template.ParseFiles("template/register.html")
		if err != nil {
			http.Error(w, "failed to load page", http.StatusInternalServerError)
			return
		}
		tmpl.Execute(w, nil)
		return
	}
	// HANDLE FORM SUBMISSION
	if r.Method == "POST" {
		// 1. GET FORM VALUES
		name := r.FormValue("name")
		email := r.FormValue("email")
		password := r.FormValue("password")
		// 2. VALIDATE INPUT
		err := utils.ValidateRegistration(name, email, password)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// 3. CHECK EMAIL DOESN'T EXIST
		var existingID int
		err = database.DB.QueryRow(
			"SELECT id FROM users WHERE email = $1",
			email,
		).Scan(&existingID)
		// if email already exists
		if err == nil {
			http.Error(w, "email already registered", http.StatusConflict)
			return
		}
		// real DB error
		if err != sql.ErrNoRows {
			http.Error(w, "database error", http.StatusInternalServerError)
			return
		}
		// 4. HASH PASSWORD
		Password, err := utils.HashPassword(password)
		if err != nil {
			http.Error(w, "failed to hash password", http.StatusInternalServerError)
			return
		}
		// OPTIONAL: EMAIL VERIFICATION TOKEN
		verificationToken := utils.GenerateVerificationToken()
		// 5. STORE IN DATABASE
		_, err = database.DB.Exec(`
			INSERT INTO users (
				name,
				email,
				password,
				verification_token
			)
			VALUES ($1, $2, $3, $4)
		`,
			name,
			email,
			Password,
			verificationToken,
		)
		if err != nil {
			http.Error(w, "failed to create account", http.StatusInternalServerError)
			return
		}
		// TODO:
		// send verification email here later
		// 6. REDIRECT TO LOGIN
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	// METHOD NOT ALLOWED
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func LoginHandler(w http.ResponseWriter, r *http.Request) {
	// SHOW LOGIN PAGE
	if r.Method == "GET" {
		tmpl, err := template.ParseFiles("template/login.html")
		if err != nil {
			http.Error(w, "failed to load login page", http.StatusInternalServerError)
			return
		}
		tmpl.Execute(w, nil)
		return
	}
	// HANDLE LOGIN
	if r.Method == "POST" {
		// 1. GET FORM VALUES
		email := r.FormValue("email")
		password := r.FormValue("password")
		// 2. FIND USER
		var userID int
		var storedHash string
		var emailVerified bool
		err := database.DB.QueryRow(`
			SELECT id, password, verified
			FROM users
			WHERE email = $1
		`, email).Scan(
			&userID,
			&storedHash,
			&emailVerified,
		)
		if err != nil {
			http.Error(w, "invalid email or password", http.StatusUnauthorized)
			return
		}
		// 3. VERIFY PASSWORD
		err = utils.CheckPassword(password, storedHash)
		if err != nil {
			http.Error(w, "invalid email or password", http.StatusUnauthorized)
			return
		}
		// 4. CHECK EMAIL VERIFIED
		// if !emailVerified {
		// 	http.Error(w, "please verify your email first", http.StatusUnauthorized)
		// 	return
		// }
		// 5. GENERATE JWT TOKEN
		token, err := utils.GenerateToken(userID)
		if err != nil {
			http.Error(w, "failed to generate token", http.StatusInternalServerError)
			return
		}
		// 6. STORE TOKEN IN SECURE COOKIE
		http.SetCookie(w, &http.Cookie{
			Name:     "token",
			Value:    token,
			HttpOnly: true,
			Path:     "/",
			MaxAge:   86400, // 24 hours
			SameSite: http.SameSiteLaxMode,
			// enable in production with HTTPS
			Secure: false,
		})
		// 7. REDIRECT TO DASHBOARD
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func LogoutHandler(w http.ResponseWriter, r *http.Request) {
}

func VerifyEmailHandler(w http.ResponseWriter, r *http.Request) {
}

func ForgotPasswordHandler(w http.ResponseWriter, r *http.Request) {
}
