package handlers

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"georesist/models"
	"georesist/utils"

	"github.com/xuri/excelize/v2"
)

const maxMeasurementFileBytes = 20 << 20

func readSubmittedMeasurements(r *http.Request) ([]models.Measurement, error) {
	file, header, err := r.FormFile("survey_file")
	if err == nil {
		defer file.Close()
		if header.Size > maxMeasurementFileBytes {
			return nil, errors.New("measurement file exceeds 20 MB")
		}
		ext := strings.ToLower(filepath.Ext(header.Filename))
		switch ext {
		case ".csv":
			reader := csv.NewReader(io.LimitReader(file, maxMeasurementFileBytes+1))
			reader.FieldsPerRecord = -1
			rows, err := reader.ReadAll()
			if err != nil {
				return nil, fmt.Errorf("invalid CSV: %w", err)
			}
			return parseMeasurementRows(rows)
		case ".xlsx":
			xl, err := excelize.OpenReader(file)
			if err != nil {
				return nil, errors.New("invalid Excel workbook; upload a valid .xlsx file")
			}
			defer func() { _ = xl.Close() }()
			sheet := xl.GetSheetName(0)
			if sheet == "" {
				return nil, errors.New("Excel workbook has no worksheet")
			}
			rows, err := xl.GetRows(sheet)
			if err != nil {
				return nil, fmt.Errorf("could not read Excel worksheet: %w", err)
			}
			return parseMeasurementRows(rows)
		case ".xls":
			return nil, errors.New("legacy .xls files are not supported; save the workbook as .xlsx or CSV")
		default:
			return nil, errors.New("unsupported measurement file type; use .csv or .xlsx")
		}
	}
	if !errors.Is(err, http.ErrMissingFile) {
		// An empty file control is normal in a manual submission. Other multipart
		// errors should not be silently treated as manual input.
		if err != nil && !strings.Contains(strings.ToLower(err.Error()), "no such file") {
			return nil, fmt.Errorf("could not read uploaded file: %w", err)
		}
	}
	return parseManualMeasurements(r)
}

func parseMeasurementRows(rows [][]string) ([]models.Measurement, error) {
	if len(rows) < 2 {
		return nil, errors.New("measurement file must contain a header and at least one data row")
	}
	cols := map[string]int{}
	for i, raw := range rows[0] {
		switch normaliseHeader(raw) {
		case "ab2", "ab/2", "ab2m", "ab/2m":
			cols["ab2"] = i
		case "mn2", "mn/2", "mn2m", "mn/2m":
			cols["mn2"] = i
		case "resistance", "r", "resistanceohm", "r ohm":
			cols["resistance"] = i
		case "apparentresistivity", "rhoa", "ρa", "apparentresistivityohmm", "apparentresistivity(ohmm)":
			cols["rhoa"] = i
		case "serialnumber", "serialno":
			cols["serial"] = i
		case "voltage", "v":
			cols["voltage"] = i
		case "electriccurrent", "current", "i":
			cols["current"] = i
		case "geometricfactor", "k":
			cols["k"] = i
		}
	}
	if _, ok := cols["ab2"]; !ok {
		return nil, errors.New("file must contain an AB2 or AB/2 column")
	}
	out := make([]models.Measurement, 0, len(rows)-1)
	for rowNo, row := range rows[1:] {
		if rowIsBlank(row) {
			continue
		}
		m, err := measurementFromRow(row, cols)
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", rowNo+2, err)
		}
		out = append(out, m)
	}
	if len(out) == 0 {
		return nil, errors.New("file contains no valid measurement rows")
	}
	return out, nil
}

func normaliseHeader(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "-", "")
	return s
}

func rowIsBlank(row []string) bool {
	for _, v := range row {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}

func measurementFromRow(row []string, cols map[string]int) (models.Measurement, error) {
	var m models.Measurement
	var err error
	if m.AB2, err = parseRequiredNumber(row, cols, "ab2"); err != nil {
		return m, err
	}
	m.MN2, err = parseOptionalNumber(row, cols, "mn2")
	if err != nil {
		return m, err
	}
	m.Resistance, err = parseOptionalNumber(row, cols, "resistance")
	if err != nil {
		return m, err
	}
	m.ApparentResistivity, err = parseOptionalNumber(row, cols, "rhoa")
	if err != nil {
		return m, err
	}
	m.Voltage, err = parseOptionalNumber(row, cols, "voltage")
	if err != nil {
		return m, err
	}
	m.ElectricCurrent, err = parseOptionalNumber(row, cols, "current")
	if err != nil {
		return m, err
	}
	m.GeometricFactor, err = parseOptionalNumber(row, cols, "k")
	if err != nil {
		return m, err
	}
	if raw, ok := cell(row, cols, "serial"); ok && strings.TrimSpace(raw) != "" {
		m.SerialNumber, err = strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || m.SerialNumber < 0 {
			return m, errors.New("serial number must be a non-negative integer")
		}
	}
	return validateAndCompleteMeasurement(m)
}

func parseManualMeasurements(r *http.Request) ([]models.Measurement, error) {
	ab2 := r.Form["ab2[]"]
	mn2 := r.Form["mn2[]"]
	res := r.Form["resistance[]"]
	rhoa := r.Form["apparent_resistivity[]"]
	voltage := r.Form["voltage[]"]
	if len(voltage) == 0 {
		voltage = r.Form["v_change[]"]
	}
	current := r.Form["electric_current[]"]
	if len(current) == 0 {
		current = r.Form["current[]"]
	}
	if len(ab2) == 0 {
		return nil, errors.New("provide a CSV/Excel file or enter manual measurements")
	}
	out := make([]models.Measurement, 0, len(ab2))
	for i, raw := range ab2 {
		if strings.TrimSpace(raw) == "" {
			// Blank rows are allowed, but any other populated cell on that row
			// should not be silently discarded.
			if indexedValue(mn2, i) != "" || indexedValue(res, i) != "" || indexedValue(rhoa, i) != "" {
				return nil, fmt.Errorf("measurement row %d has values but no AB/2 spacing", i+1)
			}
			continue
		}
		m := models.Measurement{}
		var err error
		m.AB2, err = parseFloatText(raw, "AB/2")
		if err != nil {
			return nil, fmt.Errorf("measurement row %d: %w", i+1, err)
		}
		m.MN2, err = parseFloatOptional(indexedValue(mn2, i), "MN/2")
		if err != nil {
			return nil, fmt.Errorf("measurement row %d: %w", i+1, err)
		}
		m.Resistance, err = parseFloatOptional(indexedValue(res, i), "resistance")
		if err != nil {
			return nil, fmt.Errorf("measurement row %d: %w", i+1, err)
		}
		m.ApparentResistivity, err = parseFloatOptional(indexedValue(rhoa, i), "apparent resistivity")
		if err != nil {
			return nil, fmt.Errorf("measurement row %d: %w", i+1, err)
		}
		m.Voltage, err = parseFloatOptional(indexedValue(voltage, i), "voltage")
		if err != nil {
			return nil, fmt.Errorf("measurement row %d: %w", i+1, err)
		}
		m.ElectricCurrent, err = parseFloatOptional(indexedValue(current, i), "current")
		if err != nil {
			return nil, fmt.Errorf("measurement row %d: %w", i+1, err)
		}
		m, err = validateAndCompleteMeasurement(m)
		if err != nil {
			return nil, fmt.Errorf("measurement row %d: %w", i+1, err)
		}
		out = append(out, m)
	}
	if len(out) == 0 {
		return nil, errors.New("enter at least one non-empty measurement row")
	}
	return out, nil
}

func indexedValue(values []string, i int) string {
	if i >= len(values) {
		return ""
	}
	return strings.TrimSpace(values[i])
}
func cell(row []string, cols map[string]int, key string) (string, bool) {
	i, ok := cols[key]
	if !ok || i < 0 || i >= len(row) {
		return "", false
	}
	return row[i], true
}
func parseRequiredNumber(row []string, cols map[string]int, key string) (float64, error) {
	raw, ok := cell(row, cols, key)
	if !ok || strings.TrimSpace(raw) == "" {
		return 0, fmt.Errorf("%s is required", key)
	}
	return parseFloatText(raw, key)
}
func parseOptionalNumber(row []string, cols map[string]int, key string) (float64, error) {
	raw, ok := cell(row, cols, key)
	if !ok {
		return 0, nil
	}
	return parseFloatOptional(raw, key)
}
func parseFloatOptional(raw, name string) (float64, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	return parseFloatText(raw, name)
}
func parseFloatText(raw, name string) (float64, error) {
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("%s must be a finite number", name)
	}
	return v, nil
}

func validateAndCompleteMeasurement(m models.Measurement) (models.Measurement, error) {
	if !finite(m.AB2) || m.AB2 <= 0 {
		return m, errors.New("AB/2 must be greater than zero")
	}
	if m.MN2 < 0 || !finite(m.MN2) {
		return m, errors.New("MN/2 must be zero or greater")
	}
	if m.MN2 > 0 && m.AB2 <= m.MN2 {
		return m, errors.New("AB/2 must be greater than MN/2 for Schlumberger geometry")
	}
	if m.Resistance < 0 || !finite(m.Resistance) {
		return m, errors.New("resistance must be non-negative and finite")
	}
	if m.ApparentResistivity < 0 || !finite(m.ApparentResistivity) {
		return m, errors.New("apparent resistivity must be non-negative and finite")
	}
	if m.Voltage < 0 || !finite(m.Voltage) {
		return m, errors.New("voltage must be non-negative and finite")
	}
	if m.ElectricCurrent < 0 || !finite(m.ElectricCurrent) {
		return m, errors.New("current must be non-negative and finite")
	}

	// If the field sheet contains voltage and current rather than resistance,
	// derive R = V/I. Inputs must use consistent units (volts and amperes).
	if m.Resistance == 0 && m.Voltage > 0 && m.ElectricCurrent > 0 {
		m.Resistance = m.Voltage / m.ElectricCurrent
	}
	if m.GeometricFactor <= 0 && m.MN2 > 0 {
		m.GeometricFactor = utils.CalculateK(m.AB2, m.MN2)
	}
	if m.ApparentResistivity == 0 && m.Resistance > 0 {
		if m.GeometricFactor <= 0 {
			return m, errors.New("MN/2 or a valid geometric factor is required to calculate apparent resistivity from resistance")
		}
		m.ApparentResistivity = utils.CalculateRhoA(m.GeometricFactor, m.Resistance)
	}
	if m.ApparentResistivity <= 0 || !finite(m.ApparentResistivity) {
		return m, errors.New("provide a positive apparent resistivity or a positive resistance with valid electrode geometry")
	}
	return m, nil
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
