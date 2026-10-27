package handlers

import (
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
			mn2,
			resistance,
			apparent_resistivity
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
			terrain_type,
			number_of_layers,
			geologic_history,
			previous_wells,
			date,
			latitude,
			longitude
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

func ResultEditHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Assumes your authentication middleware stores an int user ID.
	userID, ok := r.Context().Value(utils.UserContextKey).(int)
	if !ok || userID <= 0 {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	idText := mux.Vars(r)["id"]
	surveyID, err := strconv.Atoi(idText)
	if err != nil || surveyID <= 0 {
		http.Error(w, "Invalid survey ID", http.StatusBadRequest)
		return
	}

	// Replace database.DB with your actual *sql.DB variable.
	// This query assumes an interpretations table linked to surveys.
	var data ResultEditPageData

	err = database.DB.QueryRowContext(r.Context(), `
		SELECT
			s.id,
			s.title,
			s.location,
			i.id,
			i.interpretation_name,
			COALESCE(i.notes, ''),
			COALESCE(i.log_rms, 0),
			COALESCE(i.status, 'saved')
		FROM surveys s
		JOIN interpretations i ON i.survey_id = s.id
		WHERE s.id = $1
		  AND s.user_id = $2
		ORDER BY i.id DESC
		LIMIT 1
	`, surveyID, userID).Scan(
		&data.Survey.ID,
		&data.Survey.Title,
		&data.Survey.Location,
		new(int), // Interpretation ID; use a field if needed by your update handler.
		&data.Results.InterpretationName,
		&data.Results.Notes,
		&data.Results.LogRMS,
		&data.Results.Status,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "Survey or interpretation not found", http.StatusNotFound)
			return
		}

		http.Error(w, "Could not load interpretation", http.StatusInternalServerError)
		return
	}

	// Load the layer parameters for the selected interpretation.
	// The query assumes interpretation_layers has an interpretation_id column.
	// In production, retain the interpretation ID from the previous query
	// and use it here instead of querying by survey alone.
	//
	// See the note below before using this example unchanged.

	tmpl, err := template.ParseFiles("template/resultedit.html")
	if err != nil {
		http.Error(w, "Could not load edit page", http.StatusInternalServerError)
		return
	}

	if err := tmpl.Execute(w, data); err != nil {
		// Log the error server-side; avoid writing a second HTTP response.
		return
	}
}
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

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form submission", http.StatusBadRequest)
		return
	}

	action := r.FormValue("action")
	if action != "save" && action != "recalculate" {
		http.Error(w, "Invalid action", http.StatusBadRequest)
		return
	}

	// Do not pretend that saving parameters recalculates the model.
	// Implement recalculation through your actual forward-model/inversion
	// service before accepting the "recalculate" action.
	if action == "recalculate" {
		http.Error(
			w,
			"Recalculation service must be implemented before this action is available",
			http.StatusNotImplemented,
		)
		return
	}

	resistivities := r.Form["resistivity[]"]
	thicknesses := r.Form["thickness[]"]

	if len(resistivities) == 0 || len(resistivities) != len(thicknesses) {
		http.Error(w, "Invalid layer data", http.StatusBadRequest)
		return
	}

	type LayerInput struct {
		Resistivity float64
		Thickness   float64
	}

	layers := make([]LayerInput, len(resistivities))

	for i := range resistivities {
		rho, err := strconv.ParseFloat(resistivities[i], 64)
		if err != nil || rho <= 0 {
			http.Error(
				w,
				"Each layer resistivity must be a positive number",
				http.StatusBadRequest,
			)
			return
		}

		layers[i].Resistivity = rho

		// The final layer represents a half-space, so its thickness
		// should not be submitted as a finite value.
		if i == len(resistivities)-1 {
			continue
		}

		thickness, err := strconv.ParseFloat(thicknesses[i], 64)
		if err != nil || thickness <= 0 {
			http.Error(
				w,
				"Finite-layer thicknesses must be positive numbers",
				http.StatusBadRequest,
			)
			return
		}

		layers[i].Thickness = thickness
	}

	// TODO: persist the validated parameters in a database transaction.
	// First verify that surveyID belongs to userID and identify the exact
	// interpretation being edited. Update its layer rows only after that
	// ownership check succeeds.
	//
	// Also persist the interpretation name and notes if those fields are
	// editable in your template.

	http.Redirect(
		w,
		r,
		"/survey/"+strconv.Itoa(surveyID)+"/results",
		http.StatusSeeOther,
	)
}
