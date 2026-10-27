package handlers

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"html/template"
	"log"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"georesist/database"
	"georesist/models"
	"georesist/utils"
	"net/mail"

	"github.com/gorilla/mux"
	"github.com/jung-kurt/gofpdf"
)

const (
	reportStorageDir = "storage/reports"
	maxShareLifetime = 30 * 24 * time.Hour
)

type reportSharePageData struct {
	SurveyID    int
	SurveyTitle string
	Location    string
	Error       string
	Message     string
	ShareURL    string
	WhatsAppURL string
	ExpiresAt   time.Time
}

// shareReportContext loads the latest report for a survey only if the
// authenticated user owns that survey.
func shareReportContext(
	w http.ResponseWriter,
	r *http.Request,
) (models.Report, string, string, int, bool) {
	userID, ok := r.Context().Value(utils.UserContextKey).(int)
	if !ok || userID <= 0 {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return models.Report{}, "", "", 0, false
	}

	surveyID, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil || surveyID <= 0 {
		http.Error(w, "Invalid survey ID", http.StatusBadRequest)
		return models.Report{}, "", "", 0, false
	}

	var title, location string
	err = database.DB.QueryRowContext(
		r.Context(),
		`SELECT title, COALESCE(location, '')
		 FROM surveys
		 WHERE id = $1 AND user_id = $2`,
		surveyID,
		userID,
	).Scan(&title, &location)

	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Survey not found", http.StatusNotFound)
		return models.Report{}, "", "", 0, false
	}
	if err != nil {
		log.Printf("load survey for report sharing: %v", err)
		http.Error(w, "Unable to load survey", http.StatusInternalServerError)
		return models.Report{}, "", "", 0, false
	}

	report, err := loadShareReport(r, surveyID)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Create the report before sharing it", http.StatusNotFound)
		return models.Report{}, "", "", 0, false
	}
	if err != nil {
		log.Printf("load report for sharing: %v", err)
		http.Error(w, "Unable to load report", http.StatusInternalServerError)
		return models.Report{}, "", "", 0, false
	}

	return report, title, location, surveyID, true
}

func loadShareReport(r *http.Request, surveyID int) (models.Report, error) {
	var report models.Report

	err := database.DB.QueryRowContext(
		r.Context(),
		`SELECT
			id,
			survey_id,
			COALESCE(introduction, ''),
			COALESCE(executive_summary, ''),
			COALESCE(local_geology, ''),
			COALESCE(methodology, ''),
			COALESCE(results, ''),
			COALESCE(discussion, ''),
			COALESCE(recommendations, ''),
			COALESCE(drill_depth, 0),
			COALESCE(aquifer_detected, FALSE),
			COALESCE(pdf_url, ''),
			COALESCE(created_at, NOW())
		FROM reports
		WHERE survey_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 1`,
		surveyID,
	).Scan(
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

// reportPDFBytes generates a PDF from the persisted report data.
func reportPDFBytes(
	report models.Report,
	surveyTitle string,
	location string,
) ([]byte, error) {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.SetTitle("GeoResist Survey Report - "+surveyTitle, true)
	pdf.SetAuthor("GeoResist", true)
	pdf.SetMargins(18, 18, 18)
	pdf.SetAutoPageBreak(true, 18)
	pdf.AddPage()

	pdf.SetFont("Arial", "B", 19)
	pdf.CellFormat(0, 12, "GeoResist Survey Report", "", 1, "L", false, 0, "")
	pdf.Ln(2)

	pdf.SetFont("Arial", "", 11)
	pdf.MultiCell(0, 6, "Survey: "+pdfText(surveyTitle), "", "L", false)
	pdf.MultiCell(0, 6, "Location: "+pdfText(location), "", "L", false)
	pdf.MultiCell(0, 6, "Report ID: "+strconv.Itoa(report.ID), "", "L", false)
	pdf.MultiCell(0, 6, "Generated: "+time.Now().Format("02 Jan 2006 15:04 MST"), "", "L", false)
	pdf.Ln(4)

	addSection := func(title, body string) {
		pdf.SetFont("Arial", "B", 13)
		pdf.MultiCell(0, 8, title, "", "L", false)
		pdf.SetFont("Arial", "", 10)
		body = strings.TrimSpace(body)
		if body == "" {
			body = "Not provided."
		}
		pdf.MultiCell(0, 5.5, pdfText(body), "", "L", false)
		pdf.Ln(3)
	}

	addSection("Executive Summary", report.ExecutiveSummary)
	addSection("Introduction", report.Introduction)
	addSection("Local Geology", report.LocalGeology)
	addSection("Methodology", report.Methodology)
	addSection("Results", report.Results)
	addSection("Discussion", report.Discussion)
	addSection("Recommendations", report.Recommendations)

	pdf.SetFont("Arial", "B", 11)
	drillDepth := fmt.Sprintf("%.2f", report.DrillDepth)
	pdf.MultiCell(0, 6, "Recommended drill depth: "+drillDepth, "", "L", false)

	aquifer := "No"
	if report.AquiferDetected {
		aquifer = "Yes"
	}
	pdf.MultiCell(0, 6, "Aquifer detected: "+aquifer, "", "L", false)

	var out bytes.Buffer
	if err := pdf.Output(&out); err != nil {
		return nil, err
	}
	if pdf.Error() != nil {
		return nil, fmt.Errorf("PDF generation failed: %w", pdf.Error())
	}

	return out.Bytes(), nil
}

// pdfText replaces characters unsupported by gofpdf's built-in Arial font.
// For full Unicode support, embed a suitable TTF font in the PDF.
func pdfText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.ToValidUTF8(s, "?")
}

// saveReportPDF writes a PDF to a private server-side directory and stores
// its relative path in reports.pdf_url.
func saveReportPDF(
	r *http.Request,
	report models.Report,
	title, location string,
) (string, error) {
	pdfBytes, err := reportPDFBytes(report, title, location)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(reportStorageDir, 0700); err != nil {
		return "", err
	}

	filename := fmt.Sprintf("report-%d.pdf", report.ID)
	fullPath := filepath.Join(reportStorageDir, filename)

	tmp, err := os.CreateTemp(reportStorageDir, "report-*.tmp")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()

	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(0600); err != nil {
		return "", err
	}
	if _, err := tmp.Write(pdfBytes); err != nil {
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, fullPath); err != nil {
		return "", err
	}
	ok = true

	// Save a relative path only; never accept a filesystem path from a request.
	_, err = database.DB.ExecContext(
		r.Context(),
		`UPDATE reports SET pdf_url = $1 WHERE id = $2`,
		filepath.ToSlash(filepath.Join(reportStorageDir, filename)),
		report.ID,
	)
	if err != nil {
		return "", err
	}

	return fullPath, nil
}

// ReportPDFHandler downloads a generated PDF. Requires authentication and
// survey ownership.
func ReportPDFHandler(w http.ResponseWriter, r *http.Request) {
	report, title, location, surveyID, ok := shareReportContext(w, r)
	if !ok {
		return
	}
	if report.SurveyID != surveyID {
		http.Error(w, "Report not found", http.StatusNotFound)
		return
	}

	path, err := saveReportPDF(r, report, title, location)
	if err != nil {
		log.Printf("generate report PDF: %v", err)
		http.Error(w, "Unable to generate PDF", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set(
		"Content-Disposition",
		fmt.Sprintf(`attachment; filename="georesist-report-%d.pdf"`, surveyID),
	)
	http.ServeFile(w, r, path)
}

// createReportShareToken creates a random 256-bit token. Only the SHA-256
// hash is stored in the database.
func createReportShareToken(
	r *http.Request,
	reportID int,
	lifetime time.Duration,
) (string, time.Time, error) {
	if lifetime <= 0 || lifetime > maxShareLifetime {
		lifetime = 7 * 24 * time.Hour
	}

	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", time.Time{}, err
	}

	token := hex.EncodeToString(random)
	hash := sha256.Sum256([]byte(token))
	hashHex := hex.EncodeToString(hash[:])
	expiresAt := time.Now().UTC().Add(lifetime)

	_, err := database.DB.ExecContext(
		r.Context(),
		`INSERT INTO report_share_tokens (report_id, token_hash, expires_at)
		 VALUES ($1, $2, $3)`,
		reportID,
		hashHex,
		expiresAt,
	)
	if err != nil {
		return "", time.Time{}, err
	}

	return token, expiresAt, nil
}

func baseURL(r *http.Request) (string, error) {
	// Set APP_BASE_URL in production, e.g. https://georesist.example.com.
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("APP_BASE_URL")), "/")
	if base != "" {
		u, err := url.Parse(base)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
			return "", errors.New("APP_BASE_URL must be a valid HTTP(S) URL")
		}
		if os.Getenv("APP_ENV") == "production" && u.Scheme != "https" {
			return "", errors.New("APP_BASE_URL must use HTTPS in production")
		}
		return base, nil
	}

	// Avoid trusting arbitrary Host headers in production.
	if os.Getenv("APP_ENV") == "production" {
		return "", errors.New("APP_BASE_URL must be configured in production")
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host, nil
}

// ReportShareHandler displays sharing controls and processes email requests.
// POST action=link creates a public, expiring download link for WhatsApp.
// POST action=email sends the PDF as an email attachment.
func ReportShareHandler(w http.ResponseWriter, r *http.Request) {
	report, title, location, surveyID, ok := shareReportContext(w, r)
	if !ok {
		return
	}

	data := reportSharePageData{
		SurveyID:    surveyID,
		SurveyTitle: title,
		Location:    location,
	}

	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form", http.StatusBadRequest)
			return
		}

		switch r.FormValue("action") {
		case "link":
			token, expiry, err := createReportShareToken(r, report.ID, 7*24*time.Hour)
			if err != nil {
				log.Printf("create report share token: %v", err)
				http.Error(w, "Unable to create share link", http.StatusInternalServerError)
				return
			}

			base, err := baseURL(r)
			if err != nil {
				log.Printf("configure share URL: %v", err)
				http.Error(w, "Sharing URL is not configured", http.StatusInternalServerError)
				return
			}

			data.ShareURL = base + "/shared/report/" + url.PathEscape(token)
			data.WhatsAppURL = "https://wa.me/?text=" + url.QueryEscape(
				"GeoResist survey report: "+title+"\n"+
					"Location: "+location+"\n"+
					"Download PDF: "+data.ShareURL+"\n"+
					"This link expires on "+expiry.Format("02 Jan 2006 15:04 MST")+".",
			)
			data.ExpiresAt = expiry
			data.Message = "Share link created. It expires in 7 days."

		case "email":
			recipient := strings.TrimSpace(r.FormValue("email"))
			if recipient == "" || len(recipient) > 320 {
				data.Error = "Enter a valid recipient email address."
				break
			}

			if _, err := mail.ParseAddress(recipient); err != nil {
				data.Error = "Enter a valid recipient email address."
				break
			}

			path, err := saveReportPDF(r, report, title, location)
			if err != nil {
				log.Printf("generate PDF for email: %v", err)
				data.Error = "Unable to generate the PDF."
				break
			}

			pdfBytes, err := os.ReadFile(path)
			if err != nil {
				log.Printf("read generated PDF: %v", err)
				data.Error = "Unable to read the generated PDF."
				break
			}

			if err := sendReportEmail(recipient, title, location, pdfBytes, surveyID); err != nil {
				log.Printf("send report email: %v", err)
				data.Error = "Email could not be sent. Check the SMTP configuration."
				break
			}

			data.Message = "Report emailed successfully."
		default:
			data.Error = "Choose a valid sharing action."
		}
	}

	tmpl, err := template.ParseFiles("template/reportshare.html")
	if err != nil {
		log.Printf("parse report share template: %v", err)
		http.Error(w, "Unable to display shared report",
			http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("render report share page: %v", err)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("render report share page: %v", err)
	}
}

// SharedReportHandler validates a share token and serves the PDF directly.
func SharedReportHandler(w http.ResponseWriter, r *http.Request) {
	token := mux.Vars(r)["token"]
	if len(token) != 64 {
		http.NotFound(w, r)
		return
	}
	if _, err := hex.DecodeString(token); err != nil {
		http.NotFound(w, r)
		return
	}

	hash := sha256.Sum256([]byte(token))
	hashHex := hex.EncodeToString(hash[:])

	var reportID, surveyID int
	var title, location string
	var expiresAt time.Time
	err := database.DB.QueryRowContext(
		r.Context(),
		`SELECT rst.report_id, r.survey_id, s.title,
		        COALESCE(s.location, ''), rst.expires_at
		 FROM report_share_tokens rst
		 JOIN reports r ON r.id = rst.report_id
		 JOIN surveys s ON s.id = r.survey_id
		 WHERE rst.token_hash = $1
		   AND rst.revoked_at IS NULL
		   AND rst.expires_at > NOW()`,
		hashHex,
	).Scan(&reportID, &surveyID, &title, &location, &expiresAt)

	if err != nil {
		http.NotFound(w, r)
		return
	}

	var report models.Report
	err = database.DB.QueryRowContext(
		r.Context(),
		`SELECT id, survey_id,
			COALESCE(introduction, ''),
			COALESCE(executive_summary, ''),
			COALESCE(local_geology, ''),
			COALESCE(methodology, ''),
			COALESCE(results, ''),
			COALESCE(discussion, ''),
			COALESCE(recommendations, ''),
			COALESCE(drill_depth, 0),
			COALESCE(aquifer_detected, FALSE),
			COALESCE(pdf_url, ''),
			COALESCE(created_at, NOW())
		 FROM reports WHERE id = $1 AND survey_id = $2`,
		reportID, surveyID,
	).Scan(
		&report.ID, &report.SurveyID,
		&report.Introduction, &report.ExecutiveSummary,
		&report.LocalGeology, &report.Methodology,
		&report.Results, &report.Discussion,
		&report.Recommendations, &report.DrillDepth,
		&report.AquiferDetected, &report.PdfURL, &report.CreatedAt,
	)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	pdfBytes, err := reportPDFBytes(report, title, location)
	if err != nil {
		log.Printf("generate shared PDF: %v", err)
		http.Error(w, "Unable to generate PDF", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="georesist-report-%d.pdf"`, surveyID))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "georesist-report.pdf", time.Now(), bytes.NewReader(pdfBytes))
}

// SharedReportPDFHandler is retained as an alias if you want a separate
// /shared/report/{token}/pdf route.
func SharedReportPDFHandler(w http.ResponseWriter, r *http.Request) {
	SharedReportHandler(w, r)
}

func sendReportEmail(
	to, title, location string,
	pdfBytes []byte,
	surveyID int,
) error {
	host := strings.TrimSpace(os.Getenv("SMTP_HOST"))
	port := strings.TrimSpace(os.Getenv("SMTP_PORT"))
	username := os.Getenv("SMTP_USER")
	password := os.Getenv("SMTP_PASS")
	from := strings.TrimSpace(os.Getenv("SMTP_FROM"))

	if host == "" || port == "" || from == "" {
		return errors.New("SMTP_HOST, SMTP_PORT and SMTP_FROM are required")
	}
	if username != "" && password == "" {
		return errors.New("SMTP_PASS is required when SMTP_USER is set")
	}
	if _, err := mail.ParseAddress(from); err != nil {
		return errors.New("SMTP_FROM is not a valid email address")
	}

	subject := "GeoResist Survey Report - " + title
	filename := fmt.Sprintf("georesist-report-%d.pdf", surveyID)
	boundaryBytes := make([]byte, 18)
	if _, err := rand.Read(boundaryBytes); err != nil {
		return err
	}
	boundary := "GeoResist-" + hex.EncodeToString(boundaryBytes)

	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\n", from)
	fmt.Fprintf(&msg, "To: %s\r\n", to)
	fmt.Fprintf(&msg, "Subject: %s\r\n", mime.QEncoding.Encode("UTF-8", subject))
	fmt.Fprintf(&msg, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&msg, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", boundary)

	fmt.Fprintf(&msg, "--%s\r\n", boundary)
	fmt.Fprintf(&msg, "Content-Type: text/plain; charset=UTF-8\r\n")
	fmt.Fprintf(&msg, "Content-Transfer-Encoding: 8bit\r\n\r\n")
	fmt.Fprintf(&msg,
		"Please find attached the GeoResist survey report.\n\nSurvey: %s\nLocation: %s\n",
		title, location,
	)

	fmt.Fprintf(&msg, "\r\n--%s\r\n", boundary)
	fmt.Fprintf(&msg, "Content-Type: application/pdf; name=%q\r\n", filename)
	fmt.Fprintf(&msg, "Content-Disposition: attachment; filename=%q\r\n", filename)
	fmt.Fprintf(&msg, "Content-Transfer-Encoding: base64\r\n\r\n")

	// Base64 MIME attachment, wrapped at 76 characters per line.
	encoded := []byte(base64.StdEncoding.EncodeToString(pdfBytes))
	for i := 0; i < len(encoded); i += 76 {
		end := i + 76
		if end > len(encoded) {
			end = len(encoded)
		}
		msg.Write(encoded[i:end])
		msg.WriteString("\r\n")
	}
	fmt.Fprintf(&msg, "--%s--\r\n", boundary)

	addr := net.JoinHostPort(host, port)
	var auth smtp.Auth
	if username != "" {
		auth = smtp.PlainAuth("", username, password, host)
	}

	return smtp.SendMail(addr, auth, from, []string{to}, msg.Bytes())
}

func encodeBase64(dst, src []byte) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	j := 0
	for i := 0; i < len(src); i += 3 {
		n := uint32(src[i]) << 16
		if i+1 < len(src) {
			n |= uint32(src[i+1]) << 8
		}
		if i+2 < len(src) {
			n |= uint32(src[i+2])
		}
		dst[j] = alphabet[(n>>18)&63]
		dst[j+1] = alphabet[(n>>12)&63]
		if i+1 < len(src) {
			dst[j+2] = alphabet[(n>>6)&63]
		} else {
			dst[j+2] = '='
		}
		if i+2 < len(src) {
			dst[j+3] = alphabet[n&63]
		} else {
			dst[j+3] = '='
		}
		j += 4
	}
}

// RevokeReportShareHandler revokes a link by its token. This handler is
// intentionally not routed yet; use the token-management endpoint added later.
func RevokeReportShareHandler(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Share-link revocation endpoint not configured", http.StatusNotImplemented)
}

// Keep html imported only if you add report preview rendering in this file.
var _ = html.EscapeString
var _ = subtle.ConstantTimeCompare
