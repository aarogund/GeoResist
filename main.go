package main

import (
	"georesist/database"
	"georesist/handlers"
	"github.com/gorilla/mux"
	"github.com/joho/godotenv"
	"log"
	"net/http"
	"os"
)

func main() {
	godotenv.Load()
	database.Connect()
	database.Migrate()
	r := mux.NewRouter()

	// auth routes
	r.HandleFunc("/register", handlers.RegisterHandler).Methods("GET", "POST")
	r.HandleFunc("/verify-email", handlers.VerifyEmailHandler).Methods("GET")
	r.HandleFunc("/login", handlers.LoginHandler).Methods("GET", "POST")
	r.HandleFunc("/logout", handlers.LogoutHandler).Methods("POST")
	r.HandleFunc("/forgot-password", handlers.ForgotPasswordHandler).Methods("GET", "POST")

	// survey routes
	r.HandleFunc("/surveys", handlers.SurveysHandler).Methods("GET")
	r.HandleFunc("/survey/new", handlers.SurveyNewHandler).Methods("GET", "POST")
	r.HandleFunc("/survey/{id}", handlers.SurveyHandler).Methods("GET")
	r.HandleFunc("/survey/{id}/results", handlers.SurveyResultsHandler).Methods("GET")
	r.HandleFunc("/survey/{id}/edit", handlers.SurveyEditHandler).Methods("GET", "POST")

	// report routes
	r.HandleFunc("/report/{id}/new", handlers.ReportNewHandler).Methods("GET", "POST")
	r.HandleFunc("/report/{id}/edit", handlers.ReportEditHandler).Methods("GET", "POST")
	r.HandleFunc("/report/{id}/export", handlers.ReportExportHandler).Methods("GET")
	r.HandleFunc("/report/{id}/share", handlers.ReportShareHandler).Methods("GET")

	// api routes
	r.HandleFunc("/api/survey/{id}/curve", handlers.ApiSurveyIdHandler).Methods("GET")
	r.HandleFunc("/api/survey/{id}/layers", handlers.ApiSurveyLayersHandler).Methods("GET")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}
	log.Println("GeoResist running on port:", port)
	log.Fatal(http.ListenAndServe(":"+port, r))
}
