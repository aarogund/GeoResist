package handlers

import (
	"bytes"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/jung-kurt/gofpdf"

	"georesist/database"
	"georesist/models"
	"georesist/utils"
)

func ReportEmailHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	userID, ok := r.Context().Value(utils.UserContextKey).(int)
	if !ok || userID <= 0 {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	surveyID, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil || surveyID <= 0 {
		http.Error(w, "Invalid survey ID", http.StatusBadRequest)
		return
	}

	// Limit request body size and parse the recipient email.
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form submission", http.StatusBadRequest)
		return
	}

	recipient := strings.TrimSpace(r.FormValue("email"))
	if recipient == "" {
		http.Error(w, "Recipient email is required", http.StatusBadRequest)
		return
	}

	var report models.Report
	err = database.DB.QueryRow(`
		SELECT
			r.id,
			r.survey_id,
			COALESCE(r.introduction, ''),
			COALESCE(r.executive_summary, ''),
			COALESCE(r.local_geology, ''),
			COALESCE(r.methodology, ''),
			COALESCE(r.results, ''),
			COALESCE(r.discussion, ''),
			COALESCE(r.recommendations, ''),
			COALESCE(r.drill_depth, 0),
			COALESCE(r.aquifer_detected, FALSE),
			COALESCE(r.pdf_url, ''),
			r.created_at
		FROM reports r
		JOIN surveys s ON s.id = r.survey_id
		WHERE s.id = $1 AND s.user_id = $2
		ORDER BY r.created_at DESC, r.id DESC
		LIMIT 1
	`, surveyID, userID).Scan(
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

	if err != nil {
		http.Error(w, "Report not found for this survey", http.StatusNotFound)
		return
	}

	pdfBytes, err := buildReportPDF(report)
	if err != nil {
		http.Error(w, "Could not generate report PDF", http.StatusInternalServerError)
		return
	}

	subject := fmt.Sprintf("GeoResist Survey Report #%d", surveyID)
	body := fmt.Sprintf(
		"Hello,\n\nAttached is the GeoResist report for survey #%d.\n\nRegards,\nGeoResist",
		surveyID,
	)
	filename := fmt.Sprintf("georesist-report-%d.pdf", surveyID)

	if err := utils.SendPDFEmail(
		recipient,
		subject,
		body,
		filename,
		pdfBytes,
	); err != nil {
		// Log the technical error on the server, not to the client.
		fmt.Printf("Report email failed for survey %d: %v\n", surveyID, err)
		http.Error(w, "Unable to send report email. Check SMTP configuration and server logs.", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Report emailed successfully."))
}

// buildReportPDF creates a PDF attachment from the saved report.
func buildReportPDF(report models.Report) ([]byte, error) {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.SetTitle(fmt.Sprintf("GeoResist Report #%d", report.SurveyID), false)
	pdf.SetAuthor("GeoResist", false)
	pdf.SetMargins(20, 20, 20)
	pdf.SetAutoPageBreak(true, 20)
	pdf.AddPage()

	pdf.SetFont("Arial", "B", 18)
	pdf.CellFormat(0, 12, "GeoResist Survey Report", "", 1, "C", false, 0, "")
	pdf.Ln(4)

	pdf.SetFont("Arial", "", 11)
	pdf.CellFormat(0, 7, fmt.Sprintf("Survey ID: %d", report.SurveyID), "", 1, "L", false, 0, "")
	pdf.CellFormat(0, 7, fmt.Sprintf("Report ID: %d", report.ID), "", 1, "L", false, 0, "")
	pdf.CellFormat(0, 7, "Created: "+report.CreatedAt.Format("02 Jan 2006"), "", 1, "L", false, 0, "")
	pdf.Ln(4)

	addSection := func(title, content string) {
		if strings.TrimSpace(content) == "" {
			return
		}

		pdf.SetFont("Arial", "B", 13)
		pdf.MultiCell(0, 8, title, "", "L", false)
		pdf.SetFont("Arial", "", 10)
		pdf.MultiCell(0, 6, content, "", "L", false)
		pdf.Ln(3)
	}

	addSection("Introduction", report.Introduction)
	addSection("Executive Summary", report.ExecutiveSummary)
	addSection("Local Geology", report.LocalGeology)
	addSection("Methodology", report.Methodology)
	addSection("Results", report.Results)
	addSection("Discussion", report.Discussion)
	addSection("Recommendations", report.Recommendations)

	pdf.SetFont("Arial", "B", 11)
	pdf.CellFormat(
		0, 7,
		fmt.Sprintf("Recommended drill depth: %.2f m", report.DrillDepth),
		"", 1, "L", false, 0, "",
	)
	pdf.CellFormat(
		0, 7,
		fmt.Sprintf("Aquifer detected: %t", report.AquiferDetected),
		"", 1, "L", false, 0, "",
	)

	var output bytes.Buffer
	if err := pdf.Output(&output); err != nil {
		return nil, err
	}

	return output.Bytes(), nil
}
