package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"georesist/database"
	"georesist/models"
	"georesist/utils"
	"html/template"
	"log"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

func SurveyNewHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		tmpl, err := template.ParseFiles("template/surveynew.html")
		if err != nil {
			log.Printf("parse survey form: %v", err)
			http.Error(w, "failed to load page", http.StatusInternalServerError)
			return
		}
		if err := tmpl.Execute(w, nil); err != nil {
			log.Printf("render survey form: %v", err)
		}
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit total request size, including multipart overhead.
	r.Body = http.MaxBytesReader(w, r.Body, 25<<20)
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		if err = r.ParseForm(); err != nil {
			http.Error(w, "invalid or oversized form submission", http.StatusBadRequest)
			return
		}
	}

	userID, ok := r.Context().Value(utils.UserContextKey).(int)
	if !ok || userID <= 0 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))
	location := strings.TrimSpace(r.FormValue("location"))
	terrainType := strings.TrimSpace(r.FormValue("terrain_type"))
	geologicHistory := strings.TrimSpace(r.FormValue("geologic_history"))
	previousWells := strings.TrimSpace(r.FormValue("previous_wells"))
	if title == "" || location == "" {
		http.Error(w, "survey title and location are required", http.StatusBadRequest)
		return
	}
	switch strings.ToLower(terrainType) {
	case "basement", "sedimentary", "transition zone", "":
	default:
		http.Error(w, "terrain type must be basement, sedimentary, or transition zone", http.StatusBadRequest)
		return
	}

	var layerCount any
	if raw := strings.TrimSpace(r.FormValue("number_of_layers")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 20 {
			http.Error(w, "number of layers must be between 1 and 20", http.StatusBadRequest)
			return
		}
		layerCount = n
	}

	var surveyDate any
	if raw := strings.TrimSpace(r.FormValue("survey_date")); raw != "" {
		d, err := time.Parse("2006-01-02", raw)
		if err != nil {
			http.Error(w, "survey date must use YYYY-MM-DD format", http.StatusBadRequest)
			return
		}
		surveyDate = d
	}

	latitude, err := optionalCoordinate(r.FormValue("latitude"), -90, 90)
	if err != nil {
		http.Error(w, "latitude must be a number between -90 and 90", http.StatusBadRequest)
		return
	}
	longitude, err := optionalCoordinate(r.FormValue("longitude"), -180, 180)
	if err != nil {
		http.Error(w, "longitude must be a number between -180 and 180", http.StatusBadRequest)
		return
	}

	measurements, err := readSubmittedMeasurements(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(measurements) == 0 {
		http.Error(w, "provide a CSV/Excel file or at least one complete manual measurement", http.StatusBadRequest)
		return
	}
	if len(measurements) > 5000 {
		http.Error(w, "a survey cannot contain more than 5000 measurement rows", http.StatusBadRequest)
		return
	}

	tx, err := database.DB.BeginTx(r.Context(), nil)
	if err != nil {
		log.Printf("begin survey transaction: %v", err)
		http.Error(w, "could not save survey", http.StatusInternalServerError)
		return
	}
	defer func() { _ = tx.Rollback() }()

	var surveyID int
	err = tx.QueryRowContext(r.Context(), `
		INSERT INTO surveys (
			user_id, title, location, terrain_type, number_of_layers,
			geologic_history, previous_wells, date, latitude, longitude
		)
		VALUES ($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,$9,$10)
		RETURNING id`,
		userID, title, location, terrainType, layerCount,
		nullIfEmpty(geologicHistory), nullIfEmpty(previousWells),
		surveyDate, latitude, longitude,
	).Scan(&surveyID)
	if err != nil {
		log.Printf("insert survey: %v", err)
		http.Error(w, "failed to save survey", http.StatusInternalServerError)
		return
	}

	stmt, err := tx.PrepareContext(r.Context(), `
		INSERT INTO measurements (
			survey_id, serial_number, ab2, mn2, voltage, electric_current,
			resistance, geometric_factor, apparent_resistivity
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`)
	if err != nil {
		log.Printf("prepare measurement insert: %v", err)
		http.Error(w, "failed to save measurements", http.StatusInternalServerError)
		return
	}
	defer stmt.Close()

	for i, m := range measurements {
		_, err = stmt.ExecContext(r.Context(), surveyID, nullableInt(m.SerialNumber),
			m.AB2, nullableFloat(m.MN2), nullableFloat(m.Voltage),
			nullableFloat(m.ElectricCurrent), nullableFloat(m.Resistance),
			nullableFloat(m.GeometricFactor), m.ApparentResistivity)
		if err != nil {
			log.Printf("insert measurement row %d for survey %d: %v", i+1, surveyID, err)
			http.Error(w, "failed to save all measurements; no survey changes were committed", http.StatusInternalServerError)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		log.Printf("commit survey %d: %v", surveyID, err)
		http.Error(w, "failed to save survey", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/survey/"+strconv.Itoa(surveyID)+"/results", http.StatusSeeOther)
}

func optionalCoordinate(raw string, min, max float64) (any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < min || v > max {
		return nil, errors.New("invalid coordinate")
	}
	return v, nil
}

func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
func nullableFloat(v float64) any {
	if v == 0 {
		return nil
	}
	return v
}
func nullableInt(v int) any {
	if v == 0 {
		return nil
	}
	return v
}

func SurveyHandler(w http.ResponseWriter, r *http.Request) {
	// Only allow GET requests.
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 1. Get the authenticated user's ID from the request context.
	userID, ok := r.Context().Value(utils.UserContextKey).(int)
	if !ok || userID <= 0 {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// 2. Extract the survey ID from the URL.
	idParam := mux.Vars(r)["id"]

	surveyID, err := strconv.Atoi(idParam)
	if err != nil || surveyID <= 0 {
		http.Error(w, "Invalid survey ID", http.StatusBadRequest)
		return
	}

	// 3. Retrieve the survey, enforcing ownership in the SQL query.
	var survey models.Survey

	err = database.DB.QueryRow(`
		SELECT
			id,
			user_id,
			title,
			location,
			COALESCE(terrain_type, ''),
			COALESCE(number_of_layers, 0),
			COALESCE(geologic_history, ''),
			COALESCE(previous_wells, ''),
			COALESCE(date, created_at::date),
			COALESCE(latitude, 0),
			COALESCE(longitude, 0)
		FROM surveys
		WHERE id = $1 AND user_id = $2
	`, surveyID, userID).Scan(
		&survey.ID,
		&survey.UserID,
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
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "Survey not found", http.StatusNotFound)
			return
		}

		log.Printf("Failed to retrieve survey %d: %v", surveyID, err)
		http.Error(
			w,
			"Failed to retrieve survey",
			http.StatusInternalServerError,
		)
		return
	}

	// 4. Retrieve the survey's measurements.
	rows, err := database.DB.Query(`
		SELECT
			ab2,
			COALESCE(mn2, 0),
			COALESCE(resistance, 0),
			COALESCE(apparent_resistivity, 0)
		FROM measurements
		WHERE survey_id = $1
		ORDER BY ab2 ASC
	`, surveyID)

	if err != nil {
		log.Printf("Failed to retrieve measurements for survey %d: %v",
			surveyID, err)
		http.Error(
			w,
			"Failed to retrieve survey measurements",
			http.StatusInternalServerError,
		)
		return
	}
	defer rows.Close()

	measurements := make([]models.Measurement, 0)

	for rows.Next() {
		var m models.Measurement

		if err := rows.Scan(
			&m.AB2,
			&m.MN2,
			&m.Resistance,
			&m.ApparentResistivity,
		); err != nil {
			log.Printf("Failed to scan measurement: %v", err)
			http.Error(
				w,
				"Failed to read survey measurements",
				http.StatusInternalServerError,
			)
			return
		}

		measurements = append(measurements, m)
	}

	if err := rows.Err(); err != nil {
		log.Printf("Error iterating over measurements: %v", err)
		http.Error(
			w,
			"Failed to read survey measurements",
			http.StatusInternalServerError,
		)
		return
	}

	// 5. Pass the survey and measurements to the template.
	pageData := struct {
		Survey       models.Survey
		Measurements []models.Measurement
	}{
		Survey:       survey,
		Measurements: measurements,
	}

	tmpl, err := template.ParseFiles("template/survey.html")
	if err != nil {
		log.Printf("Failed to parse survey template: %v", err)
		http.Error(
			w,
			"Failed to load survey page",
			http.StatusInternalServerError,
		)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if err := tmpl.Execute(w, pageData); err != nil {
		log.Printf("Failed to render survey %d: %v", surveyID, err)
	}
}

type SurveyResultLayer struct {
	Number      int
	Resistivity float64
	Thickness   float64
	Depth       float64
	Bottom      bool
}

type SurveyResultPage struct {
	Survey            models.Survey
	Measurements      []models.Measurement
	NumberOfLayersInt int
	GraphJSON         template.JS
	CurveType         string
	FitStatus         string
	FitAvailable      bool
	FitMetrics        utils.FitMetrics
	FitLayers         []SurveyResultLayer
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
			COALESCE(terrain_type, ''),
			COALESCE(number_of_layers, 0),
			COALESCE(geologic_history, ''),
			COALESCE(previous_wells, ''),
			COALESCE(date, created_at::date),
			COALESCE(latitude, 0),
			COALESCE(longitude, 0)
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
			COALESCE(mn2, 0),
			COALESCE(resistance, 0),
			COALESCE(apparent_resistivity, 0)
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
	curveTypeName := "Unclassified"
	fitStatus := "No valid fitted model is available for this dataset."
	fitAvailable := false
	fitMetrics := utils.FitMetrics{}
	fitLayers := make([]SurveyResultLayer, 0)

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
	// A non-positive layer count must stop this branch: otherwise the
	// minimum-measurement check can pass with an invalid model request.
	layerCount := survey.NumberOfLayersInt

	if layerCount < 2 {
		log.Printf(
			"Survey %d has invalid saved layer count %d; skipping inversion",
			surveyID,
			layerCount,
		)
	} else {
		minimumMeasurements := 2*layerCount - 1

		if len(measurements) < minimumMeasurements {
			log.Printf(
				"Survey %d: need at least %d measurements to fit %d layers; got %d",
				surveyID,
				minimumMeasurements,
				layerCount,
				len(measurements),
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
				curveTypeName = curveType

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
						fitStatus = "Automatic inversion completed"
						fitLayers = make([]SurveyResultLayer, len(fit.Resistivities))
						cumulativeDepth := 0.0
						for i, rho := range fit.Resistivities {
							row := SurveyResultLayer{Number: i + 1, Resistivity: rho, Bottom: i == len(fit.Resistivities)-1}
							if i < len(fit.Thicknesses) {
								row.Thickness = fit.Thicknesses[i]
								cumulativeDepth += fit.Thicknesses[i]
								row.Depth = cumulativeDepth
							}
							fitLayers[i] = row
						}

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
	}

	// A user-saved interpretation takes precedence over a fresh automatic fit.
	// The saved model is read-only here; a GET request must not write to the DB.
	savedRows, savedErr := database.DB.QueryContext(r.Context(), `
		SELECT COALESCE(resistivity, 0), COALESCE(thickness, 0)
		FROM layers WHERE survey_id = $1 ORDER BY layer_number`, surveyID)
	if savedErr != nil {
		log.Printf("Survey %d: unable to load saved layer model: %v", surveyID, savedErr)
	} else {
		var savedRho, savedThickness []float64
		for savedRows.Next() {
			var rho, thickness float64
			if scanErr := savedRows.Scan(&rho, &thickness); scanErr != nil {
				log.Printf("Survey %d: unable to read saved layer model: %v", surveyID, scanErr)
				savedRho = nil
				break
			}
			savedRho = append(savedRho, rho)
			savedThickness = append(savedThickness, thickness)
		}
		if rowsErr := savedRows.Err(); rowsErr != nil {
			log.Printf("Survey %d: saved layer iteration failed: %v", surveyID, rowsErr)
			savedRho = nil
		}
		_ = savedRows.Close()
		if len(savedRho) > 0 && len(savedRho) == len(savedThickness) {
			modelValid := true
			for i, rho := range savedRho {
				if !positiveFinite(rho) || (i < len(savedRho)-1 && !positiveFinite(savedThickness[i])) {
					modelValid = false
					break
				}
			}
			if modelValid {
				thicknesses := append([]float64(nil), savedThickness[:len(savedThickness)-1]...)
				savedComputed := utils.ForwardModel(ab2Values, savedRho, thicknesses)
				valid := len(savedComputed) == len(ab2Values)
				for _, value := range savedComputed {
					if !positiveFinite(value) {
						valid = false
						break
					}
				}
				if valid {
					computedRhoa = savedComputed
					fitStatus = "Saved interpretation forward response"
					fitLayers = make([]SurveyResultLayer, len(savedRho))
					depth := 0.0
					for i, rho := range savedRho {
						fitLayers[i] = SurveyResultLayer{Number: i + 1, Resistivity: rho, Bottom: i == len(savedRho)-1}
						if i < len(thicknesses) {
							fitLayers[i].Thickness = thicknesses[i]
							depth += thicknesses[i]
							fitLayers[i].Depth = depth
						}
					}
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
			fitMetrics = metrics
			fitAvailable = true
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
		CurveType:         curveTypeName,
		FitStatus:         fitStatus,
		FitAvailable:      fitAvailable,
		FitMetrics:        fitMetrics,
		FitLayers:         fitLayers,
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
			SELECT id, user_id, title, location, COALESCE(terrain_type, ''),
				COALESCE(geologic_history, ''), COALESCE(previous_wells, ''),
				COALESCE(date, created_at::date), COALESCE(latitude, 0), COALESCE(longitude, 0)
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
			SELECT id, survey_id, layer_number, COALESCE(thickness, 0),
				COALESCE(depth, 0), COALESCE(resistivity, 0), COALESCE(lithology, '')
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
			SELECT ab2, COALESCE(mn2,0), COALESCE(resistance,0), COALESCE(apparent_resistivity,0)
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
	// Only allow GET requests.
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 1. Get the authenticated user's ID.
	userID, ok := r.Context().Value(utils.UserContextKey).(int)
	if !ok || userID <= 0 {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// 2. Retrieve only this user's surveys.
	rows, err := database.DB.Query(`
		SELECT
			id,
			title,
			location,
			COALESCE(terrain_type, ''),
			COALESCE(number_of_layers, 0),
			COALESCE(geologic_history, ''),
			COALESCE(previous_wells, ''),
			COALESCE(date, created_at::date),
			COALESCE(latitude, 0),
			COALESCE(longitude, 0)
		FROM surveys
		WHERE user_id = $1
		ORDER BY date DESC, id DESC
	`, userID)

	if err != nil {
		log.Printf("Failed to retrieve surveys for user %d: %v",
			userID, err)
		http.Error(
			w,
			"Failed to retrieve surveys",
			http.StatusInternalServerError,
		)
		return
	}
	defer rows.Close()

	surveys := make([]models.Survey, 0)

	// 3. Scan each row into a Survey model.
	for rows.Next() {
		var survey models.Survey

		if err := rows.Scan(
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
		); err != nil {
			log.Printf("Failed to scan survey: %v", err)
			http.Error(
				w,
				"Failed to read surveys",
				http.StatusInternalServerError,
			)
			return
		}

		surveys = append(surveys, survey)
	}

	if err := rows.Err(); err != nil {
		log.Printf("Error iterating over surveys: %v", err)
		http.Error(
			w,
			"Failed to read surveys",
			http.StatusInternalServerError,
		)
		return
	}

	// 4. Render the survey list.
	tmpl, err := template.ParseFiles("template/surveys.html")
	if err != nil {
		log.Printf("Failed to parse surveys template: %v", err)
		http.Error(
			w,
			"Failed to load surveys page",
			http.StatusInternalServerError,
		)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if err := tmpl.Execute(w, surveys); err != nil {
		log.Printf("Failed to render surveys page: %v", err)
	}
}

// These are view models for resultedit.html.
// Adapt them to your existing models if equivalent types already exist.

type ResultEditPageData struct {
	Survey  ResultEditSurvey
	Model   ResultEditModel
	Results ResultEditMetadata
}

type ResultEditSurvey struct {
	ID       int
	Title    string
	Location string
}

type ResultEditModel struct {
	NumberOfLayers int
	Layers         []ResultEditLayer
}

type ResultEditLayer struct {
	Resistivity   float64
	Thickness     float64
	IsBottomLayer bool
}

type ResultEditMetadata struct {
	InterpretationName string
	Notes              string
	LogRMS             float64
	Status             string
}

// ResultEditHandler reads the saved layer model. If none exists yet, it shows
// a clearly labelled numerical starting model; it does not claim that those
// values are a geological interpretation.
func ResultEditHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
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

	var data ResultEditPageData
	var savedLayerCount sql.NullInt64
	err = database.DB.QueryRowContext(r.Context(), `
		SELECT id, title, location, number_of_layers
		FROM surveys WHERE id = $1 AND user_id = $2`, surveyID, userID,
	).Scan(&data.Survey.ID, &data.Survey.Title, &data.Survey.Location, &savedLayerCount)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		log.Printf("load survey for result editor: %v", err)
		http.Error(w, "Could not load survey", http.StatusInternalServerError)
		return
	}

	rows, err := database.DB.QueryContext(r.Context(), `
		SELECT layer_number, COALESCE(thickness, 0), COALESCE(resistivity, 0),
		       COALESCE(interpretation, '')
		FROM layers WHERE survey_id = $1 ORDER BY layer_number`, surveyID)
	if err != nil {
		log.Printf("load saved layers: %v", err)
		http.Error(w, "Could not load saved layer model", http.StatusInternalServerError)
		return
	}
	var notes string
	for rows.Next() {
		var number int
		var thickness, rho float64
		var interpretation string
		if err := rows.Scan(&number, &thickness, &rho, &interpretation); err != nil {
			_ = rows.Close()
			http.Error(w, "Could not read saved layer model", http.StatusInternalServerError)
			return
		}
		data.Model.Layers = append(data.Model.Layers, ResultEditLayer{
			Resistivity: rho, Thickness: thickness,
		})
		if notes == "" && strings.TrimSpace(interpretation) != "" {
			notes = interpretation
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		http.Error(w, "Could not read saved layer model", http.StatusInternalServerError)
		return
	}
	_ = rows.Close()

	if len(data.Model.Layers) == 0 {
		data.Results.Status = "No saved model — starting values only"
		data.Results.InterpretationName = "Initial model"
		// Use the median observed apparent resistivity as a neutral starting
		// value; do not invent layer boundaries or aquifer depths.
		measurements, loadErr := loadSurveyMeasurements(r.Context(), surveyID)
		if loadErr != nil {
			log.Printf("load measurements for result editor: %v", loadErr)
			http.Error(w, "Could not load measurements", http.StatusInternalServerError)
			return
		}
		valid := make([]float64, 0, len(measurements))
		for _, m := range measurements {
			if positiveFinite(m.ApparentResistivity) {
				valid = append(valid, m.ApparentResistivity)
			}
		}
		if len(valid) == 0 {
			http.Error(w, "No valid apparent-resistivity measurements are available", http.StatusBadRequest)
			return
		}
		sort.Float64s(valid)
		median := valid[len(valid)/2]
		layerCount := 1
		if savedLayerCount.Valid && savedLayerCount.Int64 >= 1 && savedLayerCount.Int64 <= 10 {
			layerCount = int(savedLayerCount.Int64)
		}
		maxAB2 := 0.0
		for _, m := range measurements {
			if positiveFinite(m.AB2) && m.AB2 > maxAB2 {
				maxAB2 = m.AB2
			}
		}
		thicknessGuess := maxAB2 / float64(layerCount+1)
		for i := 0; i < layerCount; i++ {
			layer := ResultEditLayer{Resistivity: median}
			if i < layerCount-1 && positiveFinite(thicknessGuess) {
				layer.Thickness = thicknessGuess
			}
			data.Model.Layers = append(data.Model.Layers, layer)
		}
	} else {
		data.Results.Status = "Saved model"
		data.Results.InterpretationName = "Saved layer model"
		data.Results.Notes = notes
	}
	data.Model.NumberOfLayers = len(data.Model.Layers)
	for i := range data.Model.Layers {
		data.Model.Layers[i].IsBottomLayer = i == len(data.Model.Layers)-1
	}

	// Calculate a real fit score for the current model when possible.
	measurements, loadErr := loadSurveyMeasurements(r.Context(), surveyID)
	if loadErr == nil && len(measurements) >= len(data.Model.Layers) {
		resistivities := make([]float64, len(data.Model.Layers))
		thicknesses := make([]float64, len(data.Model.Layers)-1)
		validModel := true
		for i, layer := range data.Model.Layers {
			if !positiveFinite(layer.Resistivity) {
				validModel = false
				break
			}
			resistivities[i] = layer.Resistivity
			if i < len(thicknesses) {
				if !positiveFinite(layer.Thickness) {
					validModel = false
					break
				}
				thicknesses[i] = layer.Thickness
			}
		}
		if validModel {
			ab2 := make([]float64, 0, len(measurements))
			observed := make([]float64, 0, len(measurements))
			for _, m := range measurements {
				if positiveFinite(m.AB2) && positiveFinite(m.ApparentResistivity) {
					ab2 = append(ab2, m.AB2)
					observed = append(observed, m.ApparentResistivity)
				}
			}
			computed := utils.ForwardModel(ab2, resistivities, thicknesses)
			if metrics, metricErr := utils.CalculateFitMetrics(observed, computed); metricErr == nil {
				data.Results.LogRMS = metrics.LogRMS
				data.Results.Status = "Model fit calculated"
			}
		}
	}

	tmpl, err := template.ParseFiles("template/resultedit.html")
	if err != nil {
		log.Printf("parse result editor: %v", err)
		http.Error(w, "Could not load edit page", http.StatusInternalServerError)
		return
	}
	if err := tmpl.ExecuteTemplate(w, "resultedit.html", data); err != nil {
		log.Printf("render result editor: %v", err)
	}
}

// ResultUpdateHandler validates and transactionally saves the editable model.
// The final layer is a half-space and therefore has no finite thickness.
func ResultUpdateHandler(w http.ResponseWriter, r *http.Request) {
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
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form submission", http.StatusBadRequest)
		return
	}
	action := r.FormValue("action")
	if action != "save" && action != "recalculate" {
		http.Error(w, "Invalid action", http.StatusBadRequest)
		return
	}
	resistivityValues := r.Form["resistivity[]"]
	thicknessValues := r.Form["thickness[]"]
	// The browser disables the bottom half-space thickness input, so standard
	// form submission contains N resistivities and N-1 thicknesses. Accept N
	// thickness fields as well for clients that submit an empty bottom value.
	if len(resistivityValues) < 1 || len(resistivityValues) > 10 ||
		(len(thicknessValues) != len(resistivityValues)-1 && len(thicknessValues) != len(resistivityValues)) {
		http.Error(w, "Provide 1–10 layer resistivities and a positive thickness for each finite layer (all but the bottom half-space).", http.StatusBadRequest)
		return
	}

	resistivities := make([]float64, len(resistivityValues))
	thicknesses := make([]float64, len(resistivityValues)-1)
	for i, raw := range resistivityValues {
		v, parseErr := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if parseErr != nil || !positiveFinite(v) {
			http.Error(w, fmt.Sprintf("Layer %d resistivity must be positive and finite", i+1), http.StatusBadRequest)
			return
		}
		resistivities[i] = v
		if i < len(thicknesses) {
			if i >= len(thicknessValues) {
				http.Error(w, fmt.Sprintf("Layer %d thickness is required", i+1), http.StatusBadRequest)
				return
			}
			h, parseErr := strconv.ParseFloat(strings.TrimSpace(thicknessValues[i]), 64)
			if parseErr != nil || !positiveFinite(h) {
				http.Error(w, fmt.Sprintf("Layer %d thickness must be positive and finite", i+1), http.StatusBadRequest)
				return
			}
			thicknesses[i] = h
		}
	}

	// Ownership is checked inside the same transaction as the layer writes.
	tx, err := database.DB.BeginTx(r.Context(), nil)
	if err != nil {
		log.Printf("begin model update transaction: %v", err)
		http.Error(w, "Could not save model", http.StatusInternalServerError)
		return
	}
	defer func() { _ = tx.Rollback() }()
	var title string
	if err := tx.QueryRowContext(r.Context(), `SELECT title FROM surveys WHERE id=$1 AND user_id=$2 FOR UPDATE`, surveyID, userID).Scan(&title); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		log.Printf("verify model owner: %v", err)
		http.Error(w, "Could not verify survey ownership", http.StatusInternalServerError)
		return
	}

	// If recalculation was requested, validate the forward response before any
	// writes. The same saved parameters drive the results-page fitted curve.
	if action == "recalculate" {
		measurements, loadErr := loadSurveyMeasurements(r.Context(), surveyID)
		if loadErr != nil {
			log.Printf("load measurements for model recalculation: %v", loadErr)
			http.Error(w, "Could not load measurements for recalculation", http.StatusInternalServerError)
			return
		}
		ab2 := make([]float64, 0, len(measurements))
		observed := make([]float64, 0, len(measurements))
		for _, m := range measurements {
			if positiveFinite(m.AB2) && positiveFinite(m.ApparentResistivity) {
				ab2 = append(ab2, m.AB2)
				observed = append(observed, m.ApparentResistivity)
			}
		}
		computed := utils.ForwardModel(ab2, resistivities, thicknesses)
		if _, metricErr := utils.CalculateFitMetrics(observed, computed); metricErr != nil {
			http.Error(w, "Model recalculation failed: check the model and ensure the forward-model filter data is installed", http.StatusUnprocessableEntity)
			return
		}
	}

	if _, err := tx.ExecContext(r.Context(), `DELETE FROM layers WHERE survey_id=$1`, surveyID); err != nil {
		log.Printf("delete previous layers: %v", err)
		http.Error(w, "Could not replace saved model", http.StatusInternalServerError)
		return
	}
	stmt, err := tx.PrepareContext(r.Context(), `
		INSERT INTO layers (survey_id, layer_number, thickness, depth, resistivity, lithology, interpretation, is_aquifer)
		VALUES ($1,$2,$3,$4,$5,NULL,$6,FALSE)`)
	if err != nil {
		log.Printf("prepare layer insert: %v", err)
		http.Error(w, "Could not save model", http.StatusInternalServerError)
		return
	}
	defer stmt.Close()
	name := strings.TrimSpace(r.FormValue("interpretation_name"))
	if name == "" {
		name = "User-edited VES model"
	}
	notes := strings.TrimSpace(r.FormValue("interpretation_notes"))
	interpretation := name
	if notes != "" {
		interpretation += " — " + notes
	}
	depth := 0.0
	for i, rho := range resistivities {
		if i < len(thicknesses) {
			depth += thicknesses[i]
		}
		var thickness any
		if i < len(thicknesses) {
			thickness = thicknesses[i]
		}
		if _, err := stmt.ExecContext(r.Context(), surveyID, i+1, thickness, depth, rho, interpretation); err != nil {
			log.Printf("insert model layer %d: %v", i+1, err)
			http.Error(w, "Could not save all model layers", http.StatusInternalServerError)
			return
		}
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE surveys SET number_of_layers=$1 WHERE id=$2 AND user_id=$3`, len(resistivities), surveyID, userID); err != nil {
		log.Printf("update survey layer count: %v", err)
		http.Error(w, "Could not update survey model metadata", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("commit model update: %v", err)
		http.Error(w, "Could not save model", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/survey/"+strconv.Itoa(surveyID)+"/results", http.StatusSeeOther)
}

func loadSurveyMeasurements(ctx context.Context, surveyID int) ([]models.Measurement, error) {
	rows, err := database.DB.QueryContext(ctx, `
		SELECT ab2, COALESCE(mn2,0), COALESCE(resistance,0), COALESCE(apparent_resistivity,0)
		FROM measurements WHERE survey_id=$1 ORDER BY ab2`, surveyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var measurements []models.Measurement
	for rows.Next() {
		var m models.Measurement
		if err := rows.Scan(&m.AB2, &m.MN2, &m.Resistance, &m.ApparentResistivity); err != nil {
			return nil, err
		}
		measurements = append(measurements, m)
	}
	return measurements, rows.Err()
}
