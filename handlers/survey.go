package handlers

import (
	"encoding/csv"
	"html/template"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"georesist/database"
	"georesist/utils"
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
	// HANDLE SUBMISSION
	// =========================
	if r.Method == "POST" {

		// =========================
		// 1. GET FORM VALUES
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
		// 2. GET USER ID FROM CONTEXT
		// =========================

		userVal := r.Context().Value(utils.UserContextKey)

		if userVal == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		userID := userVal.(int)

		// =========================
		// 3. SAVE SURVEY
		// =========================

		var surveyID int

		err := database.DB.QueryRow(`
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
			http.Error(w, "failed to save survey", http.StatusInternalServerError)
			return
		}

		// =========================
		// 4. GET OPTIONAL MANUAL DATA
		// =========================

		manualData := r.FormValue("manual_data")

		type Measurement struct {
			AB2        float64
			MN2        float64
			Resistance float64
		}

		var measurements []Measurement

		// =========================
		// OPTION 1: MANUAL ENTRY
		// =========================

		if manualData != "" {

			lines := strings.Split(manualData, "\n")

			for _, line := range lines {

				line = strings.TrimSpace(line)

				if line == "" {
					continue
				}

				parts := strings.Split(line, ",")

				if len(parts) != 3 {
					http.Error(w, "invalid manual data format", http.StatusBadRequest)
					return
				}

				ab2, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
				mn2, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
				resistance, err3 := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)

				if err1 != nil || err2 != nil || err3 != nil {
					http.Error(w, "invalid manual numeric values", http.StatusBadRequest)
					return
				}

				measurements = append(measurements, Measurement{
					AB2:        ab2,
					MN2:        mn2,
					Resistance: resistance,
				})
			}
		}

		// =========================
		// OPTION 2: CSV FILE
		// =========================

		file, header, err := r.FormFile("survey_file")

		if err == nil {

			defer file.Close()

			ext := filepath.Ext(header.Filename)

			if ext != ".csv" {
				http.Error(w, "only CSV supported for now", http.StatusBadRequest)
				return
			}

			reader := csv.NewReader(file)

			// skip header
			_, err = reader.Read()
			if err != nil {
				http.Error(w, "invalid csv file", http.StatusBadRequest)
				return
			}

			for {

				record, err := reader.Read()

				if err == io.EOF {
					break
				}

				if err != nil {
					http.Error(w, "error reading csv", http.StatusInternalServerError)
					return
				}

				ab2, _ := strconv.ParseFloat(record[0], 64)
				mn2, _ := strconv.ParseFloat(record[1], 64)
				resistance, _ := strconv.ParseFloat(record[2], 64)

				measurements = append(measurements, Measurement{
					AB2:        ab2,
					MN2:        mn2,
					Resistance: resistance,
				})
			}
		}

		// =========================
		// VALIDATE INPUT SOURCE
		// =========================

		if len(measurements) == 0 {
			http.Error(w, "provide CSV file or manual data", http.StatusBadRequest)
			return
		}

		// =========================
		// 5. SAVE MEASUREMENTS
		// =========================

		for _, m := range measurements {

			// Schlumberger geometric factor
			k := (3.14159265359 * ((m.AB2 * m.AB2) - (m.MN2 * m.MN2))) / (2 * m.MN2)

			// apparent resistivity
			rhoa := k * m.Resistance

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
				rhoa,
			)

			if err != nil {
				http.Error(w, "failed to save measurements", http.StatusInternalServerError)
				return
			}
		}

		// =========================
		// 6. REDIRECT TO RESULTS
		// =========================

		http.Redirect(
			w,
			r,
			"/survey/results?id="+strconv.Itoa(surveyID),
			http.StatusSeeOther,
		)

		return
	}

	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func SurveyHandler(w http.ResponseWriter, r *http.Request) {
}

func SurveyResultsHandler(w http.ResponseWriter, r *http.Request) {
}

func SurveyEditHandler(w http.ResponseWriter, r *http.Request) {
}

func SurveysHandler(w http.ResponseWriter, r *http.Request) {
}
