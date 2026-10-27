package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

// buildReportDefaults creates an editable draft from persisted survey metadata,
// measurements, and the saved layer model. It deliberately avoids claiming an
// aquifer or prescribing a drill depth from resistivity alone.
func buildReportDefaults(ctx context.Context, surveyID int, title, location string) (models.Report, error) {
	var terrain, geology, wells string
	err := database.DB.QueryRowContext(ctx, `
		SELECT COALESCE(terrain_type,''), COALESCE(geologic_history,''), COALESCE(previous_wells,'')
		FROM surveys WHERE id=$1`, surveyID).Scan(&terrain, &geology, &wells)
	if err != nil {
		return models.Report{}, err
	}

	var count int
	var minRho, maxRho sql.NullFloat64
	err = database.DB.QueryRowContext(ctx, `
		SELECT COUNT(*), MIN(apparent_resistivity), MAX(apparent_resistivity)
		FROM measurements WHERE survey_id=$1 AND apparent_resistivity > 0`, surveyID).Scan(&count, &minRho, &maxRho)
	if err != nil {
		return models.Report{}, err
	}
	if count == 0 || !minRho.Valid || !maxRho.Valid {
		return models.Report{}, errors.New("no positive apparent-resistivity measurements available")
	}

	var layerLines []string
	var overburdenThickness float64
	var candidateIntervals []string
	var provisionalDrillDepth float64
	layerRows, err := database.DB.QueryContext(ctx, `
		SELECT layer_number, COALESCE(resistivity,0), COALESCE(thickness,0)
		FROM layers WHERE survey_id=$1 ORDER BY layer_number`, surveyID)
	if err != nil {
		return models.Report{}, err
	}
	for layerRows.Next() {
		var n int
		var rho, thickness float64
		if err := layerRows.Scan(&n, &rho, &thickness); err != nil {
			_ = layerRows.Close()
			return models.Report{}, err
		}
		if !positiveFinite(rho) {
			continue
		}
		// In a 1-D layered model, finite layer thicknesses sum to the depth
		// of the bottom of the modeled overburden. This is a model-derived
		// estimate, not a geological basement pick.
		if thickness > 0 {
			overburdenThickness += thickness
		}
		// Broad screening only: moderate resistivity and meaningful thickness
		// can flag a candidate interval, but cannot prove saturation or yield.
		if n > 1 && thickness >= 3 && rho >= 10 && rho <= 150 {
			top := overburdenThickness - thickness
			bottom := overburdenThickness
			candidateIntervals = append(candidateIntervals, fmt.Sprintf("Layer %d: %.1f–%.1f m depth (%.1f Ω·m; %.1f m thick; screening candidate only)", n, top, bottom, rho, thickness))
			if provisionalDrillDepth == 0 {
				provisionalDrillDepth = bottom
			}
		}
		if n == 1 {
			layerLines = append(layerLines, fmt.Sprintf("Layer %d: resistivity %.2f Ω·m, thickness %.2f m", n, rho, thickness))
		} else if thickness > 0 {
			layerLines = append(layerLines, fmt.Sprintf("Layer %d: resistivity %.2f Ω·m, thickness %.2f m", n, rho, thickness))
		} else {
			layerLines = append(layerLines, fmt.Sprintf("Layer %d: resistivity %.2f Ω·m (bottom half-space)", n, rho))
		}
	}
	if err := layerRows.Err(); err != nil {
		_ = layerRows.Close()
		return models.Report{}, err
	}
	_ = layerRows.Close()

	results := fmt.Sprintf("VES DATA SUMMARY\nValid apparent-resistivity observations: %d\nObserved apparent-resistivity range: %.2f–%.2f Ω·m.", count, minRho.Float64, maxRho.Float64)
	if len(layerLines) > 0 {
		results += "\n\nINTERPRETED GEOELECTRIC LAYER MODEL\n" + strings.Join(layerLines, "\n")
	} else {
		results += "\n\nINTERPRETED GEOELECTRIC LAYER MODEL\nNo saved layer model was available when this draft was generated. Open Results → Edit model, save/recalculate the model, then regenerate or update this report."
	}
	results += fmt.Sprintf("\n\nOVERBURDEN / MODEL THICKNESS ESTIMATE\nSum of finite modeled layer thicknesses: %.2f m. This represents the depth to the bottom of the finite layers in the current model; it is not automatically the depth to competent basement.\n", overburdenThickness)
	if len(candidateIntervals) > 0 {
		results += "\nPOTENTIAL WATER-BEARING INTERVALS (SCREENING ONLY)\n" + strings.Join(candidateIntervals, "\n")
		results += fmt.Sprintf("\nProvisional investigation depth: approximately %.1f m (bottom of first flagged interval). This is a preliminary model-based target for field review, not a guaranteed drilling depth.", provisionalDrillDepth)
	} else {
		results += "\nPOTENTIAL WATER-BEARING INTERVALS\nNo interval met the broad screening criteria (resistivity 10–150 Ω·m and finite thickness ≥3 m). This does not rule out groundwater; review geology, terrain, curve shape, and local borehole records."
	}
	results += "\n\nHYDROGEOLOGICAL INTERPRETATION LIMITATION\nThese are heuristic screening flags, not automatic aquifer confirmation. Clay-rich zones can be conductive without being productive aquifers, and fractured/weathered basement can be resistive or conductive depending on saturation and lithology. Validate with local geology, nearby wells, and preferably borehole evidence before siting a borehole."

	localGeology := strings.TrimSpace(geology)
	if localGeology == "" {
		localGeology = "No local geologic history or reconnaissance notes were entered."
	}
	if strings.TrimSpace(wells) != "" {
		localGeology += "\n\nPrevious-well information: " + strings.TrimSpace(wells)
	}
	if strings.TrimSpace(terrain) != "" {
		localGeology = "Recorded terrain type: " + terrain + ".\n\n" + localGeology
	}

	return models.Report{
		SurveyID:         surveyID,
		Introduction:     fmt.Sprintf("This report presents the vertical electrical sounding (VES) data collected for %q at %s. The report draft is populated from the survey record and entered measurements; the interpretation should be checked against field notes and local geology.", title, location),
		ExecutiveSummary: fmt.Sprintf("A total of %d valid positive apparent-resistivity observations were recorded, spanning %.2f–%.2f Ω·m. %s", count, minRho.Float64, maxRho.Float64, "Any layer model shown below is a numerical interpretation, not independent proof of lithology or groundwater productivity."),
		LocalGeology:     localGeology,
		Methodology:      "Vertical Electrical Sounding (VES) data were reviewed using the recorded AB/2 spacing and apparent resistivity. The layer model is a 1-D interpretation of the sounding response; report the electrode array, spacing units, instrument details, inversion method, and fit statistic from field records before issuing the final report.",
		Results:          results,
		Discussion:       "Apparent resistivity is non-unique: different lithologies, saturation levels, clay content, weathering, and fractures can produce overlapping values. Review the fitted response alongside reconnaissance, borehole logs, and nearby well records. A low numerical misfit does not guarantee a unique subsurface model.",
		Recommendations:  "Verify field data and electrode geometry; compare interpreted layer resistivities and thicknesses with reconnaissance, regional geology, and nearby borehole logs; investigate any flagged interval using independent evidence; and determine final borehole depth with a qualified hydrogeologist/driller. The listed interval and depth are preliminary screening estimates, not a guarantee of groundwater or yield.",
		DrillDepth:       provisionalDrillDepth,
	}, nil
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
		defaults, defaultErr := buildReportDefaults(r.Context(), surveyID, surveyTitle, location)
		if defaultErr != nil {
			log.Printf("Build report defaults for survey %d: %v", surveyID, defaultErr)
			// Keep the report form available even when optional summary data cannot
			// be loaded; the user can still complete it manually.
		} else {
			data.Report = defaults
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
		// Reports may have been drafted before the layer model was saved. Refresh
		// only the generated model section when it is absent; preserve other user edits.
		if !strings.Contains(report.Results, "INTERPRETED GEOELECTRIC LAYER MODEL") {
			defaults, draftErr := buildReportDefaults(r.Context(), surveyID, surveyTitle, location)
			if draftErr == nil && strings.TrimSpace(defaults.Results) != "" {
				if strings.TrimSpace(report.Results) == "" {
					report.Results = defaults.Results
				} else {
					report.Results += "\n\n" + defaults.Results
				}
				data.Report = report
			}
		}
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
