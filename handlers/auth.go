package handlers

import (
	"database/sql"
<<<<<<< HEAD
	"encoding/json"
	"html/template"
	"net/http"
	"time"

	"georesist/database"
	"georesist/utils"

	"github.com/google/uuid"
)
=======
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
			http.Error(w, err.Error(), 500)
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
>>>>>>> ec7d6fb3ac126ba9fa48818abe6ada4bc5b8a551

func VerifyEmailHandler(w http.ResponseWriter, r *http.Request) {

	token := r.URL.Query().Get("token")

	if token == "" {
		http.Error(w, "missing token", 400)
		return
	}

	result, err := database.DB.Exec(
		"UPDATE users SET email_verified = TRUE, verification_token = NULL WHERE verification_token = $1",
		token,
	)

	if err != nil {
		http.Error(w, "verification failed", 500)
		return
	}

	rows, _ := result.RowsAffected()

	if rows == 0 {
		http.Error(w, "invalid token", 400)
		return
	}

	w.Write([]byte("Email verified successfully"))
}

func ForgotPasswordHandler(w http.ResponseWriter, r *http.Request) {
<<<<<<< HEAD

	type Req struct {
		Email string
	}

	var req Req
	json.NewDecoder(r.Body).Decode(&req)

	token := uuid.New().String()
	expiry := time.Now().Add(1 * time.Hour)

	_, err := database.DB.Exec(
		"UPDATE users SET reset_token=$1, reset_token_expiry=$2 WHERE email=$3",
		token, expiry, req.Email,
	)

	if err != nil {
		http.Error(w, "error processing request", 500)
		return
	}

	// In production → send email
	// For now → return token

	json.NewEncoder(w).Encode(map[string]string{
		"reset_token": token,
	})
}

func LogoutHandler(w http.ResponseWriter, r *http.Request) {

	// With JWT, logout is handled on client
	// by deleting token (no server session)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Logged out successfully"))
}

func ResetPasswordHandler(w http.ResponseWriter, r *http.Request) {

	type Req struct {
		Token    string
		Password string
	}

	var req Req
	json.NewDecoder(r.Body).Decode(&req)

	hash, _ := utils.HashPassword(req.Password)

	result, err := database.DB.Exec(
		`UPDATE users 
		 SET password_hash=$1, reset_token=NULL, reset_token_expiry=NULL
		 WHERE reset_token=$2 AND reset_token_expiry > NOW()`,
		hash, req.Token,
	)

	if err != nil {
		http.Error(w, "reset failed", 500)
		return
	}

	rows, _ := result.RowsAffected()

	if rows == 0 {
		http.Error(w, "invalid or expired token", 400)
		return
	}

	w.Write([]byte("Password reset successful"))
}

type RegisterRequest struct {
	Name     string
	Email    string
	Password string
}

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
		hashedPassword, err := utils.HashPassword(password)
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
				password_hash,
				verification_token
			)
			VALUES ($1, $2, $3, $4)
		`,
			name,
			email,
			hashedPassword,
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

// 	var req RegisterRequest
// 	json.NewDecoder(r.Body).Decode(&req)

// 	err := utils.ValidateRegistration(req.Name, req.Email, req.Password)
// 	if err != nil {
// 		http.Error(w, err.Error(), http.StatusBadRequest)
// 		return
// 	}

// 	hash, _ := utils.HashPassword(req.Password)

// 	token := uuid.New().String()

// 	_, err = database.DB.Exec(
// 		"INSERT INTO users (name, email, password_hash, verification_token) VALUES ($1,$2,$3,$4)",
// 		req.Name, req.Email, hash, token,
// 	)
// 	if err != nil {
// 		http.Error(w, "error saving user", 500)
// 		return
// 	}

// 	fmt.Fprint(w, "User registered")
// }

func LoginHandler(w http.ResponseWriter, r *http.Request) {

	var req RegisterRequest
	json.NewDecoder(r.Body).Decode(&req)

	var id int
	var hash string

	err := database.DB.QueryRow(
		"SELECT id, password_hash FROM users WHERE email=$1",
		req.Email,
	).Scan(&id, &hash)

	if err != nil {
		http.Error(w, "invalid credentials", 401)
		return
	}

	err = utils.CheckPassword(req.Password, hash)
	if err != nil {
		http.Error(w, "invalid credentials", 401)
		return
	}

	token, _ := utils.GenerateToken(id)

	json.NewEncoder(w).Encode(map[string]string{
		"token": token,
	})
=======
>>>>>>> ec7d6fb3ac126ba9fa48818abe6ada4bc5b8a551
}
