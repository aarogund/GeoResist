package handlers

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"georesist/database"
	"georesist/models"
	"georesist/utils"
	"html/template"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/gorilla/mux"
)

func SurveyNewHandler(w http.ResponseWriter, r *http.Request) {

	// =========================
	// SHOW PAGE
	// =========================
	if r.Method == "GET" {

		tmpl, err := template.ParseFiles("template/surveynew.html")
		if err != nil {
			http.Error(w, "failed to load page", http.StatusInternalServerError)
			return
		}

		tmpl.Execute(w, nil)
		return
	}

	// =========================
	// ONLY ALLOW POST
	// =========================
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// =========================
	// PARSE MULTIPART FORM
	// =========================
	err := r.ParseMultipartForm(10 << 20)

	if err != nil {

		// fallback to normal form parsing
		err = r.ParseForm()

		if err != nil {

			http.Error(
				w,
				"failed to parse form",
				http.StatusBadRequest,
			)

			return
		}
	}

	// =========================
	// AUTH USER
	// =========================
	userVal := r.Context().Value(utils.UserContextKey)

	if userVal == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	userID, ok := userVal.(int)
	if !ok {
		http.Error(w, "invalid user context", http.StatusUnauthorized)
		return
	}

	// =========================
	// GET SURVEY FIELDS
	// =========================
	title := r.FormValue("title")

	location := r.FormValue("location")

	terrainType := r.FormValue("terrain_type")

	geologicHistory := r.FormValue("geologic_history")

	previousWells := r.FormValue("previous_wells")

	surveyDate := r.FormValue("survey_date")

	latitude := r.FormValue("latitude")

	longitude := r.FormValue("longitude")

	// =========================
	// SAVE SURVEY
	// =========================
	var surveyID int

	err = database.DB.QueryRow(`
		INSERT INTO surveys (
			user_id,
			title,
			location,
			terrain_type,
			geologic_history,
			previous_wells,
			date,
			latitude,
			longitude
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id
	`,
		userID,
		title,
		location,
		terrainType,
		geologicHistory,
		previousWells,
		surveyDate,
		latitude,
		longitude,
	).Scan(&surveyID)

	if err != nil {
		log.Println("survey save error:", err)

		http.Error(w, "failed to save survey", http.StatusInternalServerError)

		return
	}

	// =========================
	// MEASUREMENT STORAGE
	// =========================
	// =========================
	// MEASUREMENT STORAGE
	// =========================
	// =========================
	// MEASUREMENT STORAGE
	// =========================
	var measurements []models.Measurement

	// =========================
	// CHECK FILE UPLOAD
	// =========================
	file, header, err := r.FormFile("survey_file")

	// =====================================================
	// FILE UPLOAD PATH (CSV / EXCEL)
	// =====================================================
	if err == nil && header != nil {

		defer file.Close()

		ext := strings.ToLower(filepath.Ext(header.Filename))

		// =====================================================
		// FILE PARSER (CSV + EXCEL + AUTO DETECT)
		// =====================================================

		switch ext {

		// =====================================================
		// CSV
		// =====================================================

		case ".csv":

			reader := csv.NewReader(file)

			records, err := reader.ReadAll()
			if err != nil {
				http.Error(w, "invalid csv file", http.StatusBadRequest)
				return
			}

			if len(records) < 2 {
				http.Error(w, "empty csv file", http.StatusBadRequest)
				return
			}

			// =========================================
			// DETECT COLUMN INDEXES
			// =========================================

			headerRow := records[0]

			columnMap := map[string]int{}

			for i, col := range headerRow {

				col = strings.ToLower(strings.TrimSpace(col))

				switch col {

				case "ab2", "ab/2":
					columnMap["ab2"] = i

				case "mn2", "mn/2":
					columnMap["mn2"] = i

				case "resistance", "r":
					columnMap["resistance"] = i

				case "apparent_resistivity",
					"apparent resistivity",
					"rhoa",
					"ρa":

					columnMap["rhoa"] = i
				}
			}

			_, hasAB2 := columnMap["ab2"]
			_, hasMN2 := columnMap["mn2"]

			if !hasAB2 || !hasMN2 {

				http.Error(
					w,
					"csv must contain AB2 and MN2 columns",
					http.StatusBadRequest,
				)

				return
			}

			// =========================================
			// PARSE ROWS
			// =========================================

			for _, row := range records[1:] {

				m := models.Measurement{}

				// AB2
				if idx, ok := columnMap["ab2"]; ok &&
					idx < len(row) {

					m.AB2, _ = strconv.ParseFloat(
						strings.TrimSpace(row[idx]),
						64,
					)
				}

				// MN2
				if idx, ok := columnMap["mn2"]; ok &&
					idx < len(row) {

					m.MN2, _ = strconv.ParseFloat(
						strings.TrimSpace(row[idx]),
						64,
					)
				}

				// Resistance
				if idx, ok := columnMap["resistance"]; ok &&
					idx < len(row) {

					m.Resistance, _ = strconv.ParseFloat(
						strings.TrimSpace(row[idx]),
						64,
					)
				}

				// Apparent Resistivity
				if idx, ok := columnMap["rhoa"]; ok &&
					idx < len(row) {

					m.ApparentResistivity, _ =
						strconv.ParseFloat(
							strings.TrimSpace(row[idx]),
							64,
						)
				}

				// AUTO COMPUTE RHOA
				if m.ApparentResistivity == 0 &&
					m.Resistance != 0 {

					k := utils.CalculateK(
						m.AB2,
						m.MN2,
					)

					m.ApparentResistivity =
						utils.CalculateRhoA(
							k,
							m.Resistance,
						)
				}

				measurements = append(measurements, m)
			}

		// =====================================================
		// EXCEL
		// =====================================================

		case ".xlsx", ".xls":

			excelFile, err := excelize.OpenReader(file)
			if err != nil {

				http.Error(
					w,
					"invalid excel file",
					http.StatusBadRequest,
				)

				return
			}

			sheetName := excelFile.GetSheetName(0)

			rows, err := excelFile.GetRows(sheetName)
			if err != nil {

				http.Error(
					w,
					"failed to read excel file",
					http.StatusInternalServerError,
				)

				return
			}

			if len(rows) < 2 {

				http.Error(
					w,
					"empty excel file",
					http.StatusBadRequest,
				)

				return
			}

			// =========================================
			// DETECT COLUMN INDEXES
			// =========================================

			headerRow := rows[0]

			columnMap := map[string]int{}

			for i, col := range headerRow {

				col = strings.ToLower(strings.TrimSpace(col))

				switch col {

				case "ab2", "ab/2":
					columnMap["ab2"] = i

				case "mn2", "mn/2":
					columnMap["mn2"] = i

				case "resistance", "r":
					columnMap["resistance"] = i

				case "apparent_resistivity",
					"apparent resistivity",
					"rhoa",
					"ρa":

					columnMap["rhoa"] = i
				}
			}

			_, hasAB2 := columnMap["ab2"]
			_, hasMN2 := columnMap["mn2"]

			if !hasAB2 || !hasMN2 {

				http.Error(
					w,
					"excel file must contain AB2 and MN2 columns",
					http.StatusBadRequest,
				)

				return
			}

			// =========================================
			// PARSE ROWS
			// =========================================

			for _, row := range rows[1:] {

				m := models.Measurement{}

				// AB2
				if idx, ok := columnMap["ab2"]; ok &&
					idx < len(row) {

					m.AB2, _ = strconv.ParseFloat(
						strings.TrimSpace(row[idx]),
						64,
					)
				}

				// MN2
				if idx, ok := columnMap["mn2"]; ok &&
					idx < len(row) {

					m.MN2, _ = strconv.ParseFloat(
						strings.TrimSpace(row[idx]),
						64,
					)
				}

				// Resistance
				if idx, ok := columnMap["resistance"]; ok &&
					idx < len(row) {

					m.Resistance, _ = strconv.ParseFloat(
						strings.TrimSpace(row[idx]),
						64,
					)
				}

				// Apparent Resistivity
				if idx, ok := columnMap["rhoa"]; ok &&
					idx < len(row) {

					m.ApparentResistivity, _ =
						strconv.ParseFloat(
							strings.TrimSpace(row[idx]),
							64,
						)
				}

				// AUTO COMPUTE RHOA
				if m.ApparentResistivity == 0 &&
					m.Resistance != 0 {

					k := utils.CalculateK(
						m.AB2,
						m.MN2,
					)

					m.ApparentResistivity =
						utils.CalculateRhoA(
							k,
							m.Resistance,
						)
				}

				measurements = append(measurements, m)
			}

		default:

			http.Error(
				w,
				"unsupported file type",
				http.StatusBadRequest,
			)

			return
		}

	} else {

		// =====================================================
		// MANUAL INPUT PATH
		// =====================================================

		ab2Vals := r.Form["ab2[]"]

		mn2Vals := r.Form["mn2[]"]

		resistanceVals := r.Form["resistance[]"]

		rhoaVals := r.Form["apparent_resistivity[]"]

		if len(ab2Vals) == 0 {

			http.Error(
				w,
				"provide CSV/Excel upload or manual measurements",
				http.StatusBadRequest,
			)

			return
		}

		for i := range ab2Vals {

			if strings.TrimSpace(ab2Vals[i]) == "" {
				continue
			}

			m := models.Measurement{}

			m.AB2, _ = strconv.ParseFloat(
				strings.TrimSpace(ab2Vals[i]),
				64,
			)

			if i < len(mn2Vals) {

				m.MN2, _ = strconv.ParseFloat(
					strings.TrimSpace(mn2Vals[i]),
					64,
				)
			}

			if i < len(resistanceVals) &&
				strings.TrimSpace(resistanceVals[i]) != "" {

				m.Resistance, _ = strconv.ParseFloat(
					strings.TrimSpace(resistanceVals[i]),
					64,
				)
			}

			if i < len(rhoaVals) &&
				strings.TrimSpace(rhoaVals[i]) != "" {

				m.ApparentResistivity, _ =
					strconv.ParseFloat(
						strings.TrimSpace(rhoaVals[i]),
						64,
					)
			}

			// AUTO COMPUTE RHOA
			if m.ApparentResistivity == 0 &&
				m.Resistance != 0 {

				k := utils.CalculateK(
					m.AB2,
					m.MN2,
				)

				m.ApparentResistivity =
					utils.CalculateRhoA(
						k,
						m.Resistance,
					)
			}

			measurements = append(measurements, m)
		}
		http.Redirect(
			w,
			r,
			"/survey/"+strconv.Itoa(surveyID)+"/results",
			http.StatusSeeOther,
		)

		return
	}
}

func SurveyHandler(w http.ResponseWriter, r *http.Request) {
}

type SurveyResultPage struct {
	Survey       models.Survey
	Measurements []models.Measurement

	GraphJSON template.JS
}

func SurveyResultsHandler(w http.ResponseWriter, r *http.Request) {

	// =========================
	// AUTH USER
	// =========================

	userVal := r.Context().Value(utils.UserContextKey)
	if userVal == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	userID, ok := userVal.(int)
	if !ok {
		http.Error(w, "invalid user context", http.StatusUnauthorized)
		return
	}
	// =========================
	// GET SURVEY ID
	// =========================

	vars := mux.Vars(r)

	idStr := vars["id"]

	if idStr == "" {
		http.Error(w, "missing survey id", http.StatusBadRequest)
		return
	}

	surveyID, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "invalid survey id", http.StatusBadRequest)
		return
	}

	// =========================
	// FETCH SURVEY
	// =========================

	var survey models.Survey

	err = database.DB.QueryRow(`
		SELECT
			id,
			title,
			location,
			terrain_type,
			geologic_history,
			previous_wells,
			date,
			latitude,
			longitude
		FROM surveys
		WHERE id = $1
		AND user_id = $2
	`,
		surveyID,
		userID,
	).Scan(
		&survey.ID,
		&survey.Title,
		&survey.Location,
		&survey.TerrainType,
		&survey.GeologicHistory,
		&survey.PreviousWells,
		&survey.Date,
		&survey.Latitude,
		&survey.Longitude,
	)

	if err != nil {
		http.Error(w, "survey not found", http.StatusNotFound)
		return
	}

	// =========================
	// FETCH MEASUREMENTS
	// =========================
	fmt.Println("Fetching measurements for surveyID:", surveyID)

	rows, err := database.DB.Query(`
		SELECT
			ab2,
			mn2,
			resistance,
			apparent_resistivity
		FROM measurements
		WHERE survey_id = $1
		ORDER BY ab2 ASC
	`, &surveyID)

	fmt.Println("Query error:", err)
	if err != nil {

		http.Error(w, "failed to fetch measurements", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var measurements []models.Measurement

	for rows.Next() {

		var m models.Measurement

		err := rows.Scan(
			&m.AB2,
			&m.MN2,
			&m.Resistance,
			&m.ApparentResistivity,
		)

		if err != nil {

			log.Println("scan error:", err)

			http.Error(w, err.Error(), http.StatusInternalServerError)

			return
		}

		measurements = append(measurements, m)
	}
	log.Println("measurements:", measurements)
	var ab2Values []float64
	var rhoaValues []float64

	for _, m := range measurements {

		ab2Values = append(ab2Values, m.AB2)

		rhoaValues = append(
			rhoaValues,
			m.ApparentResistivity,
		)
	}

	// convert to JSON
	ab2JSON, err := json.Marshal(ab2Values)
	if err != nil {
		http.Error(w, "failed to build graph data", 500)
		return
	}

	rhoaJSON, err := json.Marshal(rhoaValues)
	if err != nil {
		http.Error(w, "failed to build graph data", 500)
		return
	}
	log.Println("AB2 JSON:", string(ab2JSON))
	log.Println("Rhoa JSON:", string(rhoaJSON))
	// =========================
	// PREPARE PAGE DATA
	// =========================
	graphData := map[string]interface{}{
		"ab2":  ab2Values,
		"rhoa": rhoaValues,
	}

	graphJSON, err := json.Marshal(graphData)
	if err != nil {
		http.Error(w, "failed to build graph JSON", 500)
		return
	}

	pageData := SurveyResultPage{
		Survey:       survey,
		Measurements: measurements,

		GraphJSON: template.JS(string(graphJSON)),
	}

	// =========================
	// LOAD TEMPLATE
	// =========================
	// Add this before tmpl.Execute:
	w.Header().Set("Content-Type", "text/html")
	tmpl, err := template.ParseFiles("template/surveyresult.html")
	if err != nil {
		http.Error(w, "failed to load page", http.StatusInternalServerError)
		return
	}

	err = tmpl.Execute(w, pageData)
	if err != nil {
		fmt.Println("Template error:", err)
	}
}

func SurveyEditHandler(w http.ResponseWriter, r *http.Request) {
}

func SurveysHandler(w http.ResponseWriter, r *http.Request) {
}
