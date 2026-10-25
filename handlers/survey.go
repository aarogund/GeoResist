package handlers

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"georesist/database"
	"georesist/models"
	"georesist/utils"
	"html/template"
	"log"
	"math"
	"net/http"
	"path/filepath"
	"sort"
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
		fmt.Println("Loading surveynew.html")
		tmpl, err := template.ParseFiles("template/surveynew.html")
		if err != nil {
			fmt.Println("Template error:", err)
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

	numberOfLayers := r.FormValue("number_of_layers")
	numberOfLayersInt, _ := strconv.Atoi(numberOfLayers)

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
			number_of_layers,
			geologic_history,
			previous_wells,
			date,
			latitude,
			longitude
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id
	`,
		userID,
		title,
		location,
		terrainType,
		numberOfLayersInt,
		geologicHistory,
		previousWells,
		surveyDate,
		latitude,
		longitude,
	).Scan(&surveyID)

	if err != nil {
		log.Println("survey save error:", err)
		log.Printf("DEBUG values: userID=%d, title=%s, location=%s, terrain=%s, layers=%d\n",
			userID, title, location, terrainType, numberOfLayersInt)
		http.Error(w, "failed to save survey", http.StatusInternalServerError)
		return
	}

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
		// SAVE ALL MEASUREMENTS TO DATABASE
		fmt.Println("Total measurements to save:", len(measurements))

		for _, m := range measurements {
			_, err = database.DB.Exec(`
        INSERT INTO measurements (
            survey_id,
            ab2,
            mn2,
            resistance,
            apparent_resistivity
        )
        VALUES ($1,$2,$3,$4,$5)
    `,
				surveyID,
				m.AB2,
				m.MN2,
				m.Resistance,
				m.ApparentResistivity,
			)
			if err != nil {
				fmt.Println("Measurement insert error:", err)
				http.Error(w, "failed to save measurements", http.StatusInternalServerError)
				return
			}
		}

		// REDIRECT AFTER SAVING
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

// type SurveyResultPage struct {
// 	Survey            models.Survey
// 	Measurements      []models.Measurement
// 	NumberOfLayersInt int
// 	GraphJSON         template.JS
// }

// func SurveyResultsHandler(w http.ResponseWriter, r *http.Request) {
// 	// 1. Authenticate the user.
// 	userVal := r.Context().Value(utils.UserContextKey)
// 	if userVal == nil {
// 		http.Error(w, "unauthorized", http.StatusUnauthorized)
// 		return
// 	}

// 	userID, ok := userVal.(int)
// 	if !ok {
// 		http.Error(w, "invalid user context", http.StatusUnauthorized)
// 		return
// 	}

// 	// 2. Read the survey ID.
// 	vars := mux.Vars(r)
// 	idStr := vars["id"]

// 	if idStr == "" {
// 		http.Error(w, "missing survey id", http.StatusBadRequest)
// 		return
// 	}

// 	surveyID, err := strconv.Atoi(idStr)
// 	if err != nil {
// 		http.Error(w, "invalid survey id", http.StatusBadRequest)
// 		return
// 	}

// 	// 3. Fetch the survey belonging to this user.
// 	var survey models.Survey

// 	err = database.DB.QueryRow(`
// 		SELECT
// 			id,
// 			title,
// 			location,
// 			terrain_type,
// 			number_of_layers,
// 			geologic_history,
// 			previous_wells,
// 			date,
// 			latitude,
// 			longitude
// 		FROM surveys
// 		WHERE id = $1 AND user_id = $2
// 	`, surveyID, userID).Scan(
// 		&survey.ID,
// 		&survey.Title,
// 		&survey.Location,
// 		&survey.TerrainType,
// 		&survey.NumberOfLayersInt,
// 		&survey.GeologicHistory,
// 		&survey.PreviousWells,
// 		&survey.Date,
// 		&survey.Latitude,
// 		&survey.Longitude,
// 	)

// 	if err != nil {
// 		log.Printf("Failed to fetch survey %d: %v", surveyID, err)
// 		http.Error(w, "survey not found", http.StatusNotFound)
// 		return
// 	}

// 	// 4. Fetch the measurements.
// 	rows, err := database.DB.Query(`
// 		SELECT
// 			ab2,
// 			mn2,
// 			resistance,
// 			apparent_resistivity
// 		FROM measurements
// 		WHERE survey_id = $1
// 		ORDER BY ab2 ASC
// 	`, surveyID)
// 	if err != nil {
// 		log.Printf("Failed to fetch measurements: %v", err)
// 		http.Error(
// 			w,
// 			"failed to fetch measurements",
// 			http.StatusInternalServerError,
// 		)
// 		return
// 	}

// 	var measurements []models.Measurement

// 	for rows.Next() {
// 		var m models.Measurement

// 		if err := rows.Scan(
// 			&m.AB2,
// 			&m.MN2,
// 			&m.Resistance,
// 			&m.ApparentResistivity,
// 		); err != nil {
// 			rows.Close()
// 			log.Printf("Failed to scan measurement: %v", err)
// 			http.Error(
// 				w,
// 				"failed to read measurements",
// 				http.StatusInternalServerError,
// 			)
// 			return
// 		}

// 		measurements = append(measurements, m)
// 	}

// 	if err := rows.Err(); err != nil {
// 		rows.Close()
// 		log.Printf("Measurement iteration failed: %v", err)
// 		http.Error(
// 			w,
// 			"failed to read measurements",
// 			http.StatusInternalServerError,
// 		)
// 		return
// 	}

// 	if err := rows.Close(); err != nil {
// 		log.Printf("Failed to close measurement rows: %v", err)
// 	}

// 	if len(measurements) == 0 {
// 		http.Error(w, "no measurements found for this survey", http.StatusBadRequest)
// 		return
// 	}

// 	log.Printf(
// 		"Survey %d: loaded %d raw measurements",
// 		surveyID,
// 		len(measurements),
// 	)

// 	// 5. Average repeated AB/2 measurements.
// 	// This function should return one measurement per AB/2,
// 	// with the averaged apparent resistivity.
// 	measurements = utils.AverageRepeatedAB2(measurements)

// 	if len(measurements) == 0 {
// 		http.Error(w, "no valid measurements available", http.StatusBadRequest)
// 		return
// 	}

// 	// 6. Build the observed curve data.
// 	ab2Values := make([]float64, 0, len(measurements))
// 	rhoaValues := make([]float64, 0, len(measurements))

// 	for _, m := range measurements {
// 		ab2Values = append(ab2Values, m.AB2)
// 		rhoaValues = append(rhoaValues, m.ApparentResistivity)
// 	}

// 	// Leave computed empty unless a model is successfully fitted.
// 	var computedRhoa []float64

// 	// 7. Use the layer count saved with the survey.
// 	layerCount := survey.NumberOfLayersInt

// 	if layerCount < 1 {
// 		log.Printf(
// 			"Survey %d has invalid saved layer count: %d",
// 			surveyID,
// 			layerCount,
// 		)
// 	} else if len(measurements) < layerCount+1 {
// 		log.Printf(
// 			"Survey %d: %d measurements are insufficient to fit %d layers",
// 			surveyID,
// 			len(measurements),
// 			layerCount,
// 		)
// 	} else {
// 		// 8. Detect candidate boundaries.
// 		boundaries, boundaryErr := utils.DetectLayersN(
// 			measurements,
// 			layerCount,
// 		)

// 		if boundaryErr != nil {
// 			log.Printf(
// 				"Survey %d: boundary detection failed: %v",
// 				surveyID,
// 				boundaryErr,
// 			)
// 		} else {
// 			// 9. Classify the observed curve.
// 			curveType := utils.DetermineCurveType(
// 				boundaries,
// 				measurements,
// 			)

// 			log.Printf(
// 				"Survey %d: observed curve classification = %s",
// 				surveyID,
// 				curveType,
// 			)

// 			// 10. Fit the model against ALL valid measurements.
// 			fit, fitErr := utils.FitLayerModel(
// 				measurements,
// 				boundaries,
// 				layerCount,
// 			)

// 			if fitErr != nil {
// 				log.Printf(
// 					"Survey %d: model fitting failed: %v",
// 					surveyID,
// 					fitErr,
// 				)
// 			} else if len(fit.Computed) != len(ab2Values) {
// 				log.Printf(
// 					"Survey %d: fitted curve length (%d) does not match observations (%d)",
// 					surveyID,
// 					len(fit.Computed),
// 					len(ab2Values),
// 				)
// 			} else {
// 				// Use the fitted forward-model response.
// 				// Do NOT call BuildLayerModel here.
// 				computedRhoa = fit.Computed

// 				log.Printf(
// 					"Survey %d: fitted resistivities = %v",
// 					surveyID,
// 					fit.Resistivities,
// 				)
// 				log.Printf(
// 					"Survey %d: fitted thicknesses = %v",
// 					surveyID,
// 					fit.Thicknesses,
// 				)
// 				log.Printf(
// 					"Survey %d: RMS log-resistivity error = %.6f",
// 					surveyID,
// 					fit.RMSLogError,
// 				)
// 			}
// 		}
// 	}
// 	metrics, err := utils.CalculateFitMetrics(
// 		rhoaValues,
// 		computedRhoa,
// 	)
// 	if err != nil {
// 		log.Printf("Unable to calculate fit metrics: %v", err)
// 	} else {
// 		log.Printf(
// 			"VES fit: points=%d logRMS=%.5f RMS%%=%.2f MAE%%=%.2f MaxAE%%=%.2f",
// 			metrics.Points,
// 			metrics.LogRMS,
// 			metrics.RMSPercent,
// 			metrics.MeanAbsolutePercent,
// 			metrics.MaxAbsolutePercent,
// 		)
// 	}
// 	// 11. Build graph JSON.
// 	graphData := map[string]interface{}{
// 		"ab2":      ab2Values,
// 		"rhoa":     rhoaValues,
// 		"computed": computedRhoa,
// 	}

// 	graphJSON, err := json.Marshal(graphData)
// 	if err != nil {
// 		log.Printf("Failed to build graph JSON: %v", err)
// 		http.Error(
// 			w,
// 			"failed to build graph data",
// 			http.StatusInternalServerError,
// 		)
// 		return
// 	}

// 	// 12. Prepare the template data.
// 	pageData := SurveyResultPage{
// 		Survey:       survey,
// 		Measurements: measurements,
// 		GraphJSON:    template.JS(string(graphJSON)),
// 	}

// 	// 13. Render the results page.
// 	tmpl, err := template.ParseFiles("template/surveyresult.html")
// 	if err != nil {
// 		log.Printf("Failed to load survey results template: %v", err)
// 		http.Error(
// 			w,
// 			"failed to load page",
// 			http.StatusInternalServerError,
// 		)
// 		return
// 	}

// 	w.Header().Set("Content-Type", "text/html; charset=utf-8")

// 	if err := tmpl.Execute(w, pageData); err != nil {
// 		log.Printf("Failed to render survey results: %v", err)
// 	}
// }

type SurveyResultPage struct {
	Survey            models.Survey
	Measurements      []models.Measurement
	NumberOfLayersInt int
	GraphJSON         template.JS
}

func SurveyResultsHandler(w http.ResponseWriter, r *http.Request) {
	// 1. Authenticate the user.
	userVal := r.Context().Value(utils.UserContextKey)
	if userVal == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	userID, ok := userVal.(int)
	if !ok || userID <= 0 {
		http.Error(w, "invalid user context", http.StatusUnauthorized)
		return
	}

	// 2. Read the survey ID.
	vars := mux.Vars(r)
	idStr := vars["id"]

	if idStr == "" {
		http.Error(w, "missing survey id", http.StatusBadRequest)
		return
	}

	surveyID, err := strconv.Atoi(idStr)
	if err != nil || surveyID <= 0 {
		http.Error(w, "invalid survey id", http.StatusBadRequest)
		return
	}

	// 3. Fetch the survey belonging to this user.
	var survey models.Survey

	err = database.DB.QueryRow(`
		SELECT
			id,
			title,
			location,
			terrain_type,
			number_of_layers,
			geologic_history,
			previous_wells,
			date,
			latitude,
			longitude
		FROM surveys
		WHERE id = $1 AND user_id = $2
	`, surveyID, userID).Scan(
		&survey.ID,
		&survey.Title,
		&survey.Location,
		&survey.TerrainType,
		&survey.NumberOfLayersInt,
		&survey.GeologicHistory,
		&survey.PreviousWells,
		&survey.Date,
		&survey.Latitude,
		&survey.Longitude,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "survey not found", http.StatusNotFound)
			return
		}

		log.Printf("Failed to fetch survey %d: %v", surveyID, err)
		http.Error(
			w,
			"failed to fetch survey",
			http.StatusInternalServerError,
		)
		return
	}

	// 4. Fetch the measurements.
	rows, err := database.DB.Query(`
		SELECT
			ab2,
			mn2,
			resistance,
			apparent_resistivity
		FROM measurements
		WHERE survey_id = $1
		ORDER BY ab2 ASC
	`, surveyID)

	if err != nil {
		log.Printf("Failed to fetch measurements: %v", err)
		http.Error(
			w,
			"failed to fetch measurements",
			http.StatusInternalServerError,
		)
		return
	}

	var measurements []models.Measurement

	for rows.Next() {
		var m models.Measurement

		if err := rows.Scan(
			&m.AB2,
			&m.MN2,
			&m.Resistance,
			&m.ApparentResistivity,
		); err != nil {
			_ = rows.Close()

			log.Printf("Failed to scan measurement: %v", err)
			http.Error(
				w,
				"failed to read measurements",
				http.StatusInternalServerError,
			)
			return
		}

		measurements = append(measurements, m)
	}

	if err := rows.Err(); err != nil {
		_ = rows.Close()

		log.Printf("Measurement iteration failed: %v", err)
		http.Error(
			w,
			"failed to read measurements",
			http.StatusInternalServerError,
		)
		return
	}

	if err := rows.Close(); err != nil {
		log.Printf("Failed to close measurement rows: %v", err)
	}

	if len(measurements) == 0 {
		http.Error(
			w,
			"no measurements found for this survey",
			http.StatusBadRequest,
		)
		return
	}

	log.Printf(
		"Survey %d: loaded %d raw measurements",
		surveyID,
		len(measurements),
	)

	// 5. Validate the raw measurements before averaging.
	validMeasurements := make([]models.Measurement, 0, len(measurements))

	for _, m := range measurements {
		if !positiveFinite(m.AB2) ||
			!positiveFinite(m.ApparentResistivity) {
			log.Printf(
				"Survey %d: ignoring invalid measurement AB/2=%v rhoa=%v",
				surveyID,
				m.AB2,
				m.ApparentResistivity,
			)
			continue
		}

		validMeasurements = append(validMeasurements, m)
	}

	if len(validMeasurements) == 0 {
		http.Error(
			w,
			"no valid positive VES measurements available",
			http.StatusBadRequest,
		)
		return
	}

	// Ensure measurements are ordered before averaging.
	sort.Slice(validMeasurements, func(i, j int) bool {
		return validMeasurements[i].AB2 < validMeasurements[j].AB2
	})

	// 6. Average repeated AB/2 measurements.
	measurements = utils.AverageRepeatedAB2(validMeasurements)

	if len(measurements) == 0 {
		http.Error(
			w,
			"no valid measurements available after averaging",
			http.StatusBadRequest,
		)
		return
	}

	// Ensure the averaged values are ordered and valid.
	sort.Slice(measurements, func(i, j int) bool {
		return measurements[i].AB2 < measurements[j].AB2
	})

	for _, m := range measurements {
		if !positiveFinite(m.AB2) ||
			!positiveFinite(m.ApparentResistivity) {
			log.Printf(
				"Survey %d: averaging returned an invalid measurement",
				surveyID,
			)
			http.Error(
				w,
				"averaged measurements contain invalid values",
				http.StatusInternalServerError,
			)
			return
		}
	}

	log.Printf(
		"Survey %d: %d unique AB/2 measurements after averaging",
		surveyID,
		len(measurements),
	)

	// 7. Build the observed curve data.
	// These same averaged measurements are passed to the inversion,
	// so observed and computed values correspond to the same spacings.
	ab2Values := make([]float64, 0, len(measurements))
	rhoaValues := make([]float64, 0, len(measurements))

	for _, m := range measurements {
		ab2Values = append(ab2Values, m.AB2)
		rhoaValues = append(rhoaValues, m.ApparentResistivity)
	}

	// Keep this empty unless a valid fitted curve is produced.
	computedRhoa := make([]float64, 0, len(measurements))

	// 8. Compare two-layer and three-layer models independently
	// of the layer count saved with the survey.
	//
	// CompareLayerModels requires at least 4 valid, averaged measurements.
	if len(measurements) >= 4 {
		getInitialBoundaries := func(
			layers int,
		) []utils.LayerBoundary {
			candidates, detectErr := utils.DetectLayersN(
				measurements,
				layers,
			)

			if detectErr != nil {
				log.Printf(
					"Survey %d: %d-layer boundary detection failed: %v",
					surveyID,
					layers,
					detectErr,
				)

				candidates = initialVESBoundaries(
					measurements,
					layers,
				)
			}

			if len(candidates) != layers-1 {
				log.Printf(
					"Survey %d: cannot initialize %d-layer comparison: have %d boundaries, need %d",
					surveyID,
					layers,
					len(candidates),
					layers-1,
				)
				return nil
			}

			return candidates
		}

		twoLayerBoundaries := getInitialBoundaries(2)
		threeLayerBoundaries := getInitialBoundaries(3)

		if len(twoLayerBoundaries) == 1 &&
			len(threeLayerBoundaries) == 2 {

			comparison, compareErr := utils.CompareLayerModels(
				measurements,
				twoLayerBoundaries,
				threeLayerBoundaries,
			)

			if compareErr != nil {
				log.Printf(
					"Survey %d: model comparison failed: %v",
					surveyID,
					compareErr,
				)
			} else {
				log.Printf(
					"Survey %d: 2-layer fit: LogRMS=%.5f RMS%%=%.2f MAE%%=%.2f MaxAE%%=%.2f",
					surveyID,
					comparison.TwoLayerMetrics.LogRMS,
					comparison.TwoLayerMetrics.RMSPercent,
					comparison.TwoLayerMetrics.MeanAbsolutePercent,
					comparison.TwoLayerMetrics.MaxAbsolutePercent,
				)

				log.Printf(
					"Survey %d: 2-layer resistivities=%v thicknesses=%v",
					surveyID,
					comparison.TwoLayerFit.Resistivities,
					comparison.TwoLayerFit.Thicknesses,
				)

				log.Printf(
					"Survey %d: 3-layer fit: LogRMS=%.5f RMS%%=%.2f MAE%%=%.2f MaxAE%%=%.2f",
					surveyID,
					comparison.ThreeLayerMetrics.LogRMS,
					comparison.ThreeLayerMetrics.RMSPercent,
					comparison.ThreeLayerMetrics.MeanAbsolutePercent,
					comparison.ThreeLayerMetrics.MaxAbsolutePercent,
				)

				log.Printf(
					"Survey %d: 3-layer resistivities=%v thicknesses=%v",
					surveyID,
					comparison.ThreeLayerFit.Resistivities,
					comparison.ThreeLayerFit.Thicknesses,
				)

				log.Printf(
					"Survey %d: lower-error model = %s",
					surveyID,
					comparison.PreferredModel,
				)
			}
		}
	} else {
		log.Printf(
			"Survey %d: model comparison skipped; need at least 4 unique measurements, got %d",
			surveyID,
			len(measurements),
		)
	}

	// 9. Fit the number of layers saved with the survey.
	// This remains the model displayed by the existing results page.
	layerCount := survey.NumberOfLayersInt

	if layerCount < 1 {
		log.Printf(
			"Survey %d has invalid saved layer count: %d",
			surveyID,
			layerCount,
		)
	} else if len(measurements) < layerCount+1 {
		log.Printf(
			"Survey %d: %d unique measurements may be insufficient to fit %d layers",
			surveyID,
			len(measurements),
			layerCount,
		)
	} else {
		boundaries, boundaryErr := utils.DetectLayersN(
			measurements,
			layerCount,
		)

		if boundaryErr != nil {
			log.Printf(
				"Survey %d: automatic boundary detection failed: %v",
				surveyID,
				boundaryErr,
			)

			boundaries = initialVESBoundaries(
				measurements,
				layerCount,
			)

			log.Printf(
				"Survey %d: generated %d fallback initialization boundaries",
				surveyID,
				len(boundaries),
			)
		}

		requiredBoundaries := layerCount - 1

		if len(boundaries) != requiredBoundaries {
			log.Printf(
				"Survey %d: cannot initialize inversion; have %d boundaries, need %d",
				surveyID,
				len(boundaries),
				requiredBoundaries,
			)
		} else {
			curveType := utils.DetermineCurveType(
				boundaries,
				measurements,
			)

			log.Printf(
				"Survey %d: observed curve classification = %s",
				surveyID,
				curveType,
			)

			fit, fitErr := utils.FitLayerModel(
				measurements,
				boundaries,
				layerCount,
			)

			if fitErr != nil {
				log.Printf(
					"Survey %d: model fitting failed: %v",
					surveyID,
					fitErr,
				)
			} else if len(fit.Computed) != len(rhoaValues) {
				log.Printf(
					"Survey %d: fitted curve length (%d) does not match observations (%d)",
					surveyID,
					len(fit.Computed),
					len(rhoaValues),
				)
			} else {
				validFit := true

				for _, value := range fit.Computed {
					if !positiveFinite(value) {
						validFit = false
						break
					}
				}

				if !validFit {
					log.Printf(
						"Survey %d: fitted curve contains non-positive or non-finite values",
						surveyID,
					)
				} else if len(fit.Resistivities) != layerCount ||
					len(fit.Thicknesses) != layerCount-1 {
					log.Printf(
						"Survey %d: fitted model has unexpected parameter counts: resistivities=%d thicknesses=%d",
						surveyID,
						len(fit.Resistivities),
						len(fit.Thicknesses),
					)
				} else {
					computedRhoa = append(
						[]float64(nil),
						fit.Computed...,
					)

					log.Printf(
						"Survey %d: fitted resistivities = %v",
						surveyID,
						fit.Resistivities,
					)

					log.Printf(
						"Survey %d: fitted thicknesses = %v",
						surveyID,
						fit.Thicknesses,
					)

					log.Printf(
						"Survey %d: optimizer RMS log-resistivity error = %.6f",
						surveyID,
						fit.RMSLogError,
					)
				}
			}
		}
	}

	// 12. Calculate fit metrics only if the computed curve is valid
	// and has exactly one value per observed measurement.
	if len(computedRhoa) > 0 &&
		len(computedRhoa) == len(rhoaValues) {

		metrics, metricsErr := utils.CalculateFitMetrics(
			rhoaValues,
			computedRhoa,
		)

		if metricsErr != nil {
			log.Printf(
				"Survey %d: unable to calculate fit metrics: %v",
				surveyID,
				metricsErr,
			)
		} else {
			log.Printf(
				"Survey %d: fit points=%d logRMS=%.5f RMS%%=%.2f MAE%%=%.2f MaxAE%%=%.2f",
				surveyID,
				metrics.Points,
				metrics.LogRMS,
				metrics.RMSPercent,
				metrics.MeanAbsolutePercent,
				metrics.MaxAbsolutePercent,
			)
		}
	} else {
		log.Printf(
			"Survey %d: fit metrics skipped; observed=%d computed=%d",
			surveyID,
			len(rhoaValues),
			len(computedRhoa),
		)
	}

	// 13. Build graph JSON.
	// An empty computed array tells the template not to draw a fitted line.
	graphData := map[string]interface{}{
		"ab2":      ab2Values,
		"rhoa":     rhoaValues,
		"computed": computedRhoa,
	}

	graphJSON, err := json.Marshal(graphData)
	if err != nil {
		log.Printf("Failed to build graph JSON: %v", err)
		http.Error(
			w,
			"failed to build graph data",
			http.StatusInternalServerError,
		)
		return
	}

	// 14. Prepare the template data.
	pageData := SurveyResultPage{
		Survey:            survey,
		Measurements:      measurements,
		NumberOfLayersInt: layerCount,
		GraphJSON:         template.JS(string(graphJSON)),
	}

	// 15. Render the results page.
	tmpl, err := template.ParseFiles("template/surveyresult.html")
	if err != nil {
		log.Printf("Failed to load survey results template: %v", err)
		http.Error(
			w,
			"failed to load page",
			http.StatusInternalServerError,
		)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if err := tmpl.Execute(w, pageData); err != nil {
		log.Printf("Failed to render survey results: %v", err)
	}
}

// positiveFinite reports whether a value is finite and strictly positive.
// This is required for logarithmic resistivity calculations.
func positiveFinite(value float64) bool {
	return value > 0 &&
		!math.IsNaN(value) &&
		!math.IsInf(value, 0)
}

// initialVESBoundaries generates numerical starting guesses when
// automatic boundary detection fails. These are not detected geological
// interfaces and must not be presented as geological conclusions.
func initialVESBoundaries(
	measurements []models.Measurement,
	layerCount int,
) []utils.LayerBoundary {
	if layerCount < 2 || len(measurements) < layerCount {
		return nil
	}

	// Copy and sort to avoid mutating the caller's measurements.
	ordered := append([]models.Measurement(nil), measurements...)

	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].AB2 < ordered[j].AB2
	})

	// Remove duplicate AB/2 spacings if the averaging helper has not
	// consolidated them.
	distinct := make([]models.Measurement, 0, len(ordered))

	for _, m := range ordered {
		if !positiveFinite(m.AB2) ||
			!positiveFinite(m.ApparentResistivity) {
			continue
		}

		if len(distinct) == 0 ||
			m.AB2 > distinct[len(distinct)-1].AB2 {
			distinct = append(distinct, m)
		}
	}

	if len(distinct) < layerCount {
		return nil
	}

	boundaries := make([]utils.LayerBoundary, 0, layerCount-1)
	n := len(distinct)

	for i := 1; i < layerCount; i++ {
		// Select an initial interface position across the available
		// spacing range. The optimizer must adjust the parameters.
		index := int(math.Round(
			float64(i) * float64(n-1) / float64(layerCount),
		))

		if index < 1 {
			index = 1
		}

		if index >= n-1 {
			index = n - 2
		}

		m := distinct[index]

		boundaries = append(
			boundaries,
			utils.LayerBoundary{
				AB2:         m.AB2,
				RhoA:        m.ApparentResistivity,
				FeatureType: "initial",
				SlopeChange: 0,
			},
		)
	}

	return boundaries
}

type SurveyEditPage struct {
	Survey              models.Survey
	Layers              []models.Layer
	Interpretation      utils.VESInterpretation
	Measurements        []models.Measurement
	SuggestedLayersJSON template.JS
}

func SurveyEditHandler(w http.ResponseWriter, r *http.Request) {

	if r.Method == "GET" {
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

		var survey models.Survey
		err = database.DB.QueryRow(`
			SELECT id, user_id, title, location, terrain_type,
				geologic_history, previous_wells, date, latitude, longitude
			FROM surveys
			WHERE id = $1 AND user_id = $2
		`, surveyID, userID).Scan(
			&survey.ID, &survey.UserID, &survey.Title,
			&survey.Location, &survey.TerrainType,
			&survey.GeologicHistory, &survey.PreviousWells,
			&survey.Date, &survey.Latitude, &survey.Longitude,
		)
		if err != nil {
			http.Error(w, "survey not found", http.StatusNotFound)
			return
		}

		// fetch existing layers
		layerRows, err := database.DB.Query(`
			SELECT id, survey_id, layer_number, thickness, depth,
				resistivity, lithology
			FROM layers
			WHERE survey_id = $1
			ORDER BY layer_number ASC
		`, surveyID)
		fmt.Println("Layers query error:", err)
		if err != nil {
			http.Error(w, "failed to fetch layers", http.StatusInternalServerError)
			return
		}
		defer layerRows.Close()

		var layers []models.Layer
		for layerRows.Next() {
			var layer models.Layer
			err := layerRows.Scan(
				&layer.ID, &layer.SurveyID, &layer.LayerNumber,
				&layer.Thickness, &layer.Depth,
				&layer.Resistivity, &layer.Lithology,
			)
			if err != nil {
				http.Error(w, "failed to scan layers", http.StatusInternalServerError)
				return
			}
			layers = append(layers, layer)
		}

		// fetch measurements
		measurementRows, err := database.DB.Query(`
			SELECT ab2, mn2, resistance, apparent_resistivity
			FROM measurements
			WHERE survey_id = $1
			ORDER BY ab2 ASC
		`, surveyID)
		if err != nil {
			http.Error(w, "failed to fetch measurements", http.StatusInternalServerError)
			return
		}
		defer measurementRows.Close()

		var measurements []models.Measurement
		for measurementRows.Next() {
			var m models.Measurement
			err := measurementRows.Scan(
				&m.AB2, &m.MN2, &m.Resistance, &m.ApparentResistivity,
			)
			if err != nil {
				http.Error(w, "failed to scan measurements", http.StatusInternalServerError)
				return
			}
			measurements = append(measurements, m)
		}

		// average repeated AB2
		measurements = utils.AverageRepeatedAB2(measurements)

		// run interpretation
		interpretation := utils.InterpretVES(
			measurements,
			survey.TerrainType,
			survey.GeologicHistory,
			survey.PreviousWells,
		)

		// suggested layers JSON
		suggestedJSON, _ := json.Marshal(interpretation.SuggestedLayers)

		pageData := SurveyEditPage{
			Survey:              survey,
			Layers:              layers,
			Interpretation:      interpretation,
			Measurements:        measurements,
			SuggestedLayersJSON: template.JS(suggestedJSON),
		}

		tmpl, err := template.ParseFiles("template/surveyedit.html")
		if err != nil {
			fmt.Println("Template error:", err)
			http.Error(w, "failed to load template", http.StatusInternalServerError)
			return
		}

		err = tmpl.Execute(w, pageData)
		if err != nil {
			fmt.Println("Execute error:", err)
			http.Error(w, "failed to render page", http.StatusInternalServerError)
			return
		}
		return
	}

	if r.Method == "POST" {
		userVal := r.Context().Value(utils.UserContextKey)
		if userVal == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_, ok := userVal.(int)
		if !ok {
			http.Error(w, "invalid user context", http.StatusUnauthorized)
			return
		}

		vars := mux.Vars(r)
		idStr := vars["id"]
		surveyID, err := strconv.Atoi(idStr)
		if err != nil {
			http.Error(w, "invalid survey id", http.StatusBadRequest)
			return
		}

		err = r.ParseForm()
		if err != nil {
			http.Error(w, "failed to parse form", http.StatusBadRequest)
			return
		}

		// delete old layers
		_, err = database.DB.Exec(`
			DELETE FROM layers WHERE survey_id = $1
		`, surveyID)
		if err != nil {
			http.Error(w, "failed to clear old layers", http.StatusInternalServerError)
			return
		}

		// save new layers
		layerNumbers := r.Form["layer_number[]"]
		resistivities := r.Form["resistivity[]"]
		thicknesses := r.Form["thickness[]"]
		depths := r.Form["depth[]"]
		lithologies := r.Form["lithology[]"]

		for i := range layerNumbers {
			layerNumber, _ := strconv.Atoi(layerNumbers[i])
			resistivity, _ := strconv.ParseFloat(resistivities[i], 64)
			thickness, _ := strconv.ParseFloat(thicknesses[i], 64)
			depth, _ := strconv.ParseFloat(depths[i], 64)
			lithology := lithologies[i]

			_, err = database.DB.Exec(`
				INSERT INTO layers (
					survey_id, layer_number, thickness,
					depth, resistivity, lithology
				)
				VALUES ($1,$2,$3,$4,$5,$6)
			`, surveyID, layerNumber, thickness, depth, resistivity, lithology)
			if err != nil {
				fmt.Println("Layer save error:", err)
				http.Error(w, "failed to save layers", http.StatusInternalServerError)
				return
			}
		}

		http.Redirect(w, r, "/survey/"+strconv.Itoa(surveyID)+"/results", http.StatusSeeOther)
		return
	}

	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func SurveysHandler(w http.ResponseWriter, r *http.Request) {
}
