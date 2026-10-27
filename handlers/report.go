package handlers

import (
	"database/sql"
	"errors"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"

	"georesist/database"
	"georesist/models"
	"georesist/utils"

	"github.com/gorilla/mux"
)

type reportPageData struct {
	SurveyID    int
	SurveyTitle string
	Location    string
	Report      models.Report
	Error       string
	ShareURL    string
}

// getReportContext validates the survey ID and checks that the
// authenticated user owns the survey.
func getReportContext(r *http.Request) (int, int, string, string, error) {
	userValue := r.Context().Value(utils.UserContextKey)
	userID, ok := userValue.(int)
	if !ok || userID <= 0 {
		return 0, 0, "", "", errors.New("unauthorized")
	}

	surveyID, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil || surveyID <= 0 {
		return 0, 0, "", "", errors.New("invalid survey ID")
	}

	var title, location string
	err = database.DB.QueryRow(`
		SELECT title, location
		FROM surveys
		WHERE id = $1 AND user_id = $2
	`, surveyID, userID).Scan(&title, &location)
	if err != nil {
		return 0, 0, "", "", err
	}

	return userID, surveyID, title, location, nil
}

func loadReport(surveyID int) (models.Report, error) {
	var report models.Report

	err := database.DB.QueryRow(`
		SELECT id, survey_id, introduction, executive_summary,
       local_geology, methodology, results, discussion,
       recommendations, COALESCE(drill_depth, 0),
       aquifer_detected, COALESCE(pdf_url, ''), created_at
FROM reports
WHERE survey_id = $1
ORDER BY created_at DESC, id DESC
LIMIT 1
	`, surveyID).Scan(
		&report.ID,
		&report.SurveyID,
		&report.Introduction,
		&report.ExecutiveSummary,
		&report.LocalGeology,
		&report.Methodology,
		&report.Results,
		&report.Discussion,
		&report.Recommendations,
		&report.DrillDepth,
		&report.AquiferDetected,
		&report.PdfURL,
		&report.CreatedAt,
	)

	return report, err
}

func renderReportPage(
	w http.ResponseWriter,
	filename string,
	data reportPageData,
) {
	tmpl, err := template.ParseFiles("template/" + filename)
	if err != nil {
		log.Printf("Parse %s: %v", filename, err)
		http.Error(w, "Unable to load report page", http.StatusInternalServerError)
		return
	}

	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("Render %s: %v", filename, err)
	}
}

// ReportNewHandler displays the form on GET and creates a report on POST.
func ReportNewHandler(w http.ResponseWriter, r *http.Request) {
	_, surveyID, surveyTitle, location, err := getReportContext(r)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err.Error() == "unauthorized" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		http.Error(w, "Invalid survey ID", http.StatusBadRequest)
		return
	}

	data := reportPageData{
		SurveyID:    surveyID,
		SurveyTitle: surveyTitle,
		Location:    location,
		Report: models.Report{
			SurveyID: surveyID,
		},
	}

	if r.Method == http.MethodGet {
		_, err := loadReport(surveyID)
		if err == nil {
			http.Redirect(w, r,
				"/report/"+strconv.Itoa(surveyID)+"/edit",
				http.StatusSeeOther)
			return
		}
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("Check existing report for survey %d: %v", surveyID, err)
			http.Error(w, "Unable to check report", http.StatusInternalServerError)
			return
		}

		renderReportPage(w, "reportnew.html", data)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form submission", http.StatusBadRequest)
		return
	}

	report := models.Report{
		SurveyID:         surveyID,
		Introduction:     strings.TrimSpace(r.FormValue("introduction")),
		ExecutiveSummary: strings.TrimSpace(r.FormValue("executive_summary")),
		LocalGeology:     strings.TrimSpace(r.FormValue("local_geology")),
		Methodology:      strings.TrimSpace(r.FormValue("methodology")),
		Results:          strings.TrimSpace(r.FormValue("results")),
		Discussion:       strings.TrimSpace(r.FormValue("discussion")),
		Recommendations:  strings.TrimSpace(r.FormValue("recommendations")),
		AquiferDetected:  r.FormValue("aquifer_detected") == "true",
	}

	depth := strings.TrimSpace(r.FormValue("drill_depth"))
	if depth != "" {
		report.DrillDepth, err = strconv.ParseFloat(depth, 64)
		if err != nil || report.DrillDepth < 0 {
			data.Report = report
			data.Error = "Drill depth must be a valid non-negative number."
			renderReportPage(w, "reportnew.html", data)
			return
		}
	}

	if report.Introduction == "" || report.ExecutiveSummary == "" {
		data.Report = report
		data.Error = "Introduction and executive summary are required."
		renderReportPage(w, "reportnew.html", data)
		return
	}

	_, err = database.DB.Exec(`
		INSERT INTO reports (
			survey_id, introduction, executive_summary, local_geology,
			methodology, results, discussion, recommendations,
			drill_depth, aquifer_detected
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, report.SurveyID, report.Introduction, report.ExecutiveSummary,
		report.LocalGeology, report.Methodology, report.Results,
		report.Discussion, report.Recommendations, report.DrillDepth,
		report.AquiferDetected)

	if err != nil {
		log.Printf("Create report for survey %d: %v", surveyID, err)
		data.Report = report
		data.Error = "Unable to save report. Check whether a report already exists."
		renderReportPage(w, "reportnew.html", data)
		return
	}

	http.Redirect(w, r,
		"/report/"+strconv.Itoa(surveyID)+"/edit",
		http.StatusSeeOther)
}

// ReportEditHandler displays and updates the latest report for a survey.
func ReportEditHandler(w http.ResponseWriter, r *http.Request) {
	_, surveyID, surveyTitle, location, err := getReportContext(r)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err.Error() == "unauthorized" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		http.Error(w, "Invalid survey ID", http.StatusBadRequest)
		return
	}

	report, err := loadReport(surveyID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Redirect(w, r,
				"/report/"+strconv.Itoa(surveyID)+"/new",
				http.StatusSeeOther)
			return
		}
		log.Printf("Load report for survey %d: %v", surveyID, err)
		http.Error(w, "Unable to load report", http.StatusInternalServerError)
		return
	}

	data := reportPageData{
		SurveyID:    surveyID,
		SurveyTitle: surveyTitle,
		Location:    location,
		Report:      report,
	}

	if r.Method == http.MethodGet {
		renderReportPage(w, "reportedit.html", data)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form submission", http.StatusBadRequest)
		return
	}

	report.Introduction = strings.TrimSpace(r.FormValue("introduction"))
	report.ExecutiveSummary = strings.TrimSpace(r.FormValue("executive_summary"))
	report.LocalGeology = strings.TrimSpace(r.FormValue("local_geology"))
	report.Methodology = strings.TrimSpace(r.FormValue("methodology"))
	report.Results = strings.TrimSpace(r.FormValue("results"))
	report.Discussion = strings.TrimSpace(r.FormValue("discussion"))
	report.Recommendations = strings.TrimSpace(r.FormValue("recommendations"))
	report.AquiferDetected = r.FormValue("aquifer_detected") == "true"

	depth := strings.TrimSpace(r.FormValue("drill_depth"))
	if depth != "" {
		report.DrillDepth, err = strconv.ParseFloat(depth, 64)
		if err != nil || report.DrillDepth < 0 {
			data.Report = report
			data.Error = "Drill depth must be a valid non-negative number."
			renderReportPage(w, "reportedit.html", data)
			return
		}
	} else {
		report.DrillDepth = 0
	}

	if report.Introduction == "" || report.ExecutiveSummary == "" {
		data.Report = report
		data.Error = "Introduction and executive summary are required."
		renderReportPage(w, "reportedit.html", data)
		return
	}

	_, err = database.DB.Exec(`
		UPDATE reports
		SET introduction = $1,
		    executive_summary = $2,
		    local_geology = $3,
		    methodology = $4,
		    results = $5,
		    discussion = $6,
		    recommendations = $7,
		    drill_depth = $8,
		    aquifer_detected = $9
		WHERE id = $10 AND survey_id = $11
	`, report.Introduction, report.ExecutiveSummary, report.LocalGeology,
		report.Methodology, report.Results, report.Discussion,
		report.Recommendations, report.DrillDepth, report.AquiferDetected,
		report.ID, surveyID)

	if err != nil {
		log.Printf("Update report %d: %v", report.ID, err)
		data.Report = report
		data.Error = "Unable to save report changes."
		renderReportPage(w, "reportedit.html", data)
		return
	}

	http.Redirect(w, r,
		"/report/"+strconv.Itoa(surveyID)+"/edit",
		http.StatusSeeOther)
}

// ReportExportHandler downloads the saved report as HTML.
func ReportExportHandler(w http.ResponseWriter, r *http.Request) {
	_, surveyID, surveyTitle, location, err := getReportContext(r)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err.Error() == "unauthorized" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		http.Error(w, "Invalid survey ID", http.StatusBadRequest)
		return
	}

	report, err := loadReport(surveyID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "No report exists for this survey", http.StatusNotFound)
			return
		}
		http.Error(w, "Unable to load report", http.StatusInternalServerError)
		return
	}

	tmpl, err := template.ParseFiles("template/reportexport.html")
	if err != nil {
		log.Printf("Parse reportexport.html: %v", err)
		http.Error(w, "Unable to prepare report export", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set(
		"Content-Disposition",
		`attachment; filename="georesist-report-`+strconv.Itoa(surveyID)+`.html"`,
	)

	err = tmpl.Execute(w, reportPageData{
		SurveyID:    surveyID,
		SurveyTitle: surveyTitle,
		Location:    location,
		Report:      report,
	})
	if err != nil {
		log.Printf("Render report export: %v", err)
	}
}

// ReportShareHandler displays an authenticated link to the report export.
// This is not a public share link: the recipient must be authenticated
// and authorized to access the survey.
// func ReportShareHandler(w http.ResponseWriter, r *http.Request) {
// 	_, surveyID, surveyTitle, location, err := getReportContext(r)
// 	if err != nil {
// 		if errors.Is(err, sql.ErrNoRows) {
// 			http.NotFound(w, r)
// 			return
// 		}
// 		if err.Error() == "unauthorized" {
// 			http.Error(w, "Unauthorized", http.StatusUnauthorized)
// 			return
// 		}
// 		http.Error(w, "Invalid survey ID", http.StatusBadRequest)
// 		return
// 	}

// 	report, err := loadReport(surveyID)
// 	if err != nil {
// 		if errors.Is(err, sql.ErrNoRows) {
// 			http.Error(w, "Create a report before sharing it", http.StatusNotFound)
// 			return
// 		}
// 		http.Error(w, "Unable to load report", http.StatusInternalServerError)
// 		return
// 	}

// 	scheme := "https"
// 	if r.TLS == nil {
// 		scheme = "http"
// 	}

// 	data := reportPageData{
// 		SurveyID:    surveyID,
// 		SurveyTitle: surveyTitle,
// 		Location:    location,
// 		Report:      report,
// 		ShareURL: scheme + "://" + r.Host +
// 			"/report/" + strconv.Itoa(surveyID) + "/export",
// 	}

// 	renderReportPage(w, "reportshare.html", data)
// }

func ApiSurveyIdHandler(w http.ResponseWriter, r *http.Request) {
}

func ApiSurveyLayersHandler(w http.ResponseWriter, r *http.Request) {
}
