package main

import (
	"fmt"
	"georesist/database"
	"georesist/handlers"
	"georesist/utils"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/mux"
	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load()
	database.Connect()
	go func() {
		for {
			time.Sleep(24 * time.Hour)
			database.DB.Exec("DELETE FROM token_blacklisted WHERE expires_at < NOW()")
		}
	}()
	if os.Getenv("RUN_MIGRATION") == "true" {
		database.Migrate()
	}
	r := mux.NewRouter()
	r.PathPrefix("/static/").Handler(
		http.StripPrefix(
			"/static/",
			http.FileServer(http.Dir("./static")),
		),
	)

	// PUBLIC routes - no auth needed
	r.HandleFunc("/register", handlers.RegisterHandler).Methods("GET", "POST")
	r.HandleFunc("/login", handlers.LoginHandler).Methods("GET", "POST")
	r.HandleFunc("/logout", handlers.LogoutHandler).Methods("POST")
	r.HandleFunc("/verify-email", handlers.VerifyEmailHandler).Methods("GET")
	r.HandleFunc("/forgot-password", handlers.ForgotPasswordHandler).Methods("GET", "POST")

	// PROTECTED routes - auth required
	protected := r.PathPrefix("").Subrouter()
	protected.Use(utils.JWTMiddleware)
	protected.HandleFunc("/dashboard", handlers.DashboardHandler).Methods("GET")
	protected.HandleFunc("/surveys", handlers.SurveysHandler).Methods("GET")
	protected.HandleFunc("/survey/new", handlers.SurveyNewHandler).Methods("GET", "POST")
	protected.HandleFunc("/survey/{id}", handlers.SurveyHandler).Methods("GET")
	protected.HandleFunc("/survey/{id}/results", handlers.SurveyResultsHandler).Methods("GET")
	protected.HandleFunc("/survey/{id}/edit", handlers.SurveyEditHandler).Methods("GET", "POST")
	protected.HandleFunc("/report/{id}/new", handlers.ReportNewHandler).Methods("GET", "POST")
	protected.HandleFunc("/report/{id}/edit", handlers.ReportEditHandler).Methods("GET", "POST")
	protected.HandleFunc("/report/{id}/export", handlers.ReportExportHandler).Methods("GET")
	protected.HandleFunc("/report/{id}/share", handlers.ReportShareHandler).Methods("GET")
	protected.HandleFunc("/api/survey/{id}/curve", handlers.ApiSurveyIdHandler).Methods("GET")
	protected.HandleFunc("/api/survey/{id}/layers", handlers.ApiSurveyLayersHandler).Methods("GET")

	// static files
	fs := http.FileServer(http.Dir("./static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./template/index.html")
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}
	log.Println("GeoResist running on port:", port)
	log.Fatal(http.ListenAndServe(":"+port, r))
	dir, _ := os.Getwd()
	fmt.Println("Working directory:", dir)
}
