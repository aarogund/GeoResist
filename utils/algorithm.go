package utils

import (
	"fmt"
	"log"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"georesist/models"
)

// =====================================================
// TYPES
// =====================================================

type LayerBoundary struct {
	AB2         float64
	RhoA        float64
	FeatureType string
	LayerIndex  int
	SlopeChange float64
	PrevSlope   float64
	CurrSlope   float64
}

type OverburdenInfo struct {
	Thickness    float64
	Description  string
	Significance string
}

type GeologicContext struct {
	Warnings    []string
	Suggestions []string
	DepthAdjust float64
}

type VESInterpretation struct {
	Boundaries            []LayerBoundary
	CurveType             string
	Geology               string
	Depths                []float64
	DrillRecommended      bool
	DrillDepth            float64
	AquiferLikely         bool
	AquiferProbability    float64
	AquiferProbabilityPct float64
	SuggestedLayers       []models.Layer
	GeologicContext       GeologicContext
	OverburdenInfo        OverburdenInfo
}

// =====================================================
// AVERAGE REPEATED AB2
// =====================================================

func AverageRepeatedAB2(measurements []models.Measurement) []models.Measurement {
	grouped := make(map[float64][]models.Measurement)
	for _, m := range measurements {
		grouped[m.AB2] = append(grouped[m.AB2], m)
	}

	var averaged []models.Measurement
	for ab2, group := range grouped {
		var rhoaSum, resistanceSum, mn2Sum float64
		for _, m := range group {
			rhoaSum += m.ApparentResistivity
			resistanceSum += m.Resistance
			mn2Sum += m.MN2
		}
		count := float64(len(group))
		averaged = append(averaged, models.Measurement{
			AB2:                 ab2,
			MN2:                 mn2Sum / count,
			Resistance:          resistanceSum / count,
			ApparentResistivity: rhoaSum / count,
		})
	}

	sort.Slice(averaged, func(i, j int) bool {
		return averaged[i].AB2 < averaged[j].AB2
	})

	return averaged
}

// =====================================================
// SMOOTH MEASUREMENTS
// =====================================================

func SmoothMeasurements(measurements []models.Measurement) []models.Measurement {
	if len(measurements) < 3 {
		return measurements
	}

	smoothed := make([]models.Measurement, len(measurements))
	smoothed[0] = measurements[0]
	smoothed[len(measurements)-1] = measurements[len(measurements)-1]

	for i := 1; i < len(measurements)-1; i++ {
		smoothed[i] = measurements[i]
		smoothed[i].ApparentResistivity = (measurements[i-1].ApparentResistivity +
			measurements[i].ApparentResistivity +
			measurements[i+1].ApparentResistivity) / 3.0
	}

	return smoothed
}

// =====================================================
// CALCULATE K (SCHLUMBERGER)
// =====================================================

// CalculateK returns the Schlumberger geometric factor when AB2 and MN2
// are the half-spacings AB/2 and MN/2, expressed in the same distance unit.
// K = pi * ((AB/2)^2 - (MN/2)^2) / (2 * (MN/2)).
func CalculateK(ab2, mn2 float64) float64 {
	if !finitePositive(ab2) || !finitePositive(mn2) || ab2 <= mn2 {
		return 0
	}
	return math.Pi * (ab2*ab2 - mn2*mn2) / (2 * mn2)
}

// CalculateRhoA calculates apparent resistivity (ohm metres when spacings
// are in metres and resistance is in ohms). Invalid inputs return zero;
// callers should validate inputs before treating the result as a measurement.
func CalculateRhoA(k, resistance float64) float64 {
	if !finitePositive(k) || !finiteNonNegative(resistance) {
		return 0
	}
	return k * resistance
}

func finitePositive(v float64) bool {
	return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0)
}
func finiteNonNegative(v float64) bool {
	return v >= 0 && !math.IsNaN(v) && !math.IsInf(v, 0)
}

const (
	MinBoundaryRatio = 1.5
	MinSlopeChange   = 0.18
)

// prepareVESData sorts measurements and removes invalid values.
// Logarithms require both AB2 and apparent resistivity to be positive.
func prepareVESData(
	measurements []models.Measurement,
) []models.Measurement {
	valid := make([]models.Measurement, 0, len(measurements))

	for _, m := range measurements {
		if m.AB2 <= 0 ||
			m.ApparentResistivity <= 0 ||
			math.IsNaN(m.AB2) ||
			math.IsNaN(m.ApparentResistivity) ||
			math.IsInf(m.AB2, 0) ||
			math.IsInf(m.ApparentResistivity, 0) {
			continue
		}

		valid = append(valid, m)
	}

	sort.Slice(valid, func(i, j int) bool {
		return valid[i].AB2 < valid[j].AB2
	})

	// Combine repeated AB/2 values using the geometric mean
	// of apparent resistivity. This is appropriate for log-space
	// analysis and prevents duplicate x-values from distorting slopes.
	combined := make([]models.Measurement, 0, len(valid))

	for i := 0; i < len(valid); {
		j := i + 1
		sumLogRho := math.Log(valid[i].ApparentResistivity)

		for j < len(valid) && valid[j].AB2 == valid[i].AB2 {
			sumLogRho += math.Log(valid[j].ApparentResistivity)
			j++
		}

		m := valid[i]
		m.ApparentResistivity = math.Exp(
			sumLogRho / float64(j-i),
		)
		combined = append(combined, m)

		i = j
	}

	return combined
}

// smoothLogValues applies a small weighted moving average.
// The calculation is performed in log space, appropriate for VES curves.
func smoothLogValues(values []float64) []float64 {
	if len(values) < 3 {
		return append([]float64(nil), values...)
	}

	result := make([]float64, len(values))
	result[0] = values[0]
	result[len(values)-1] = values[len(values)-1]

	for i := 1; i < len(values)-1; i++ {
		result[i] = (values[i-1] +
			2*values[i] +
			values[i+1]) / 4
	}

	return result
}

// DetectLayers identifies candidate curve features.
// A candidate feature is not necessarily a geological boundary.
func DetectLayers(
	measurements []models.Measurement,
) []LayerBoundary {
	data := prepareVESData(measurements)

	if len(data) < 4 {
		return nil
	}

	n := len(data)
	logAB2 := make([]float64, n)
	logRho := make([]float64, n)

	for i, m := range data {
		logAB2[i] = math.Log10(m.AB2)
		logRho[i] = math.Log10(m.ApparentResistivity)
	}

	logRho = smoothLogValues(logRho)

	slopes := make([]float64, n-1)

	for i := range slopes {
		dx := logAB2[i+1] - logAB2[i]
		if dx <= 0 {
			continue
		}

		slopes[i] = (logRho[i+1] - logRho[i]) / dx
	}

	var boundaries []LayerBoundary

	for i := 1; i < len(slopes); i++ {
		prev := slopes[i-1]
		curr := slopes[i]
		change := math.Abs(curr - prev)
		log.Printf(
			"DetectLayers diagnostic: i=%d AB/2=%.3f "+
				"prevSlope=%.4f currSlope=%.4f "+
				"change=%.4f minSlopeChange=%.4f",
			i,
			data[i].AB2,
			prev,
			curr,
			change,
			MinSlopeChange,
		)

		feature := ""

		// A local maximum: positive slope changes to negative.
		if prev > 0.05 && curr < -0.05 {
			feature = "peak"
		} else if prev < -0.05 && curr > 0.05 {
			// A local minimum: negative slope changes to positive.
			feature = "trough"
		} else if change >= MinSlopeChange {
			feature = "inflection"
		} else if math.Abs(curr) < 0.05 &&
			math.Abs(prev) >= 0.25 {
			feature = "flatten"
		}

		if feature == "" {
			continue
		}

		// Slopes[i-1] spans points i-1 and i.
		// Slopes[i] spans points i and i+1.
		// Thus, point i is the feature's candidate location.
		boundaries = append(boundaries, LayerBoundary{
			AB2:         data[i].AB2,
			RhoA:        data[i].ApparentResistivity,
			FeatureType: feature,
			SlopeChange: change,
		})
	}

	log.Printf("DetectLayers: %d candidate features", len(boundaries))

	return boundaries
}

// DetectLayersN selects n-1 separated candidate boundaries.
// It ranks candidates by slope change, validates separation,
// and returns the selected boundaries in ascending AB/2 order.
func DetectLayersN(
	measurements []models.Measurement,
	n int,
) ([]LayerBoundary, error) {
	if n < 1 {
		return nil, fmt.Errorf("number of layers must be at least 1")
	}

	data := prepareVESData(measurements)
	if len(data) < 3 && n > 1 {
		return nil, fmt.Errorf("insufficient valid measurements")
	}

	required := n - 1
	if required == 0 {
		return []LayerBoundary{}, nil
	}

	candidates := DetectLayers(data)

	// Do not impose a fixed AB/2 >= 5 m cutoff here.
	// A shallow feature may be genuine; noise rejection should
	// depend on feature strength and data quality.
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].SlopeChange > candidates[j].SlopeChange
	})

	selected := make([]LayerBoundary, 0, required)

	for _, candidate := range candidates {
		separated := true

		for _, existing := range selected {
			ratio := candidate.AB2 / existing.AB2
			if ratio < 1 {
				ratio = 1 / ratio
			}

			if ratio < MinBoundaryRatio {
				separated = false
				break
			}
		}

		if separated {
			selected = append(selected, candidate)
		}

		if len(selected) == required {
			break
		}
	}

	if len(selected) < required {
		return nil, fmt.Errorf(
			"only %d sufficiently separated candidate boundaries found; %d required for %d layers",
			len(selected), required, n,
		)
	}

	sort.Slice(selected, func(i, j int) bool {
		return selected[i].AB2 < selected[j].AB2
	})

	for i, b := range selected {
		log.Printf(
			"Selected boundary %d: AB/2=%.2f m, type=%s, slope change=%.4f",
			i+1, b.AB2, b.FeatureType, b.SlopeChange,
		)
	}

	return selected, nil
}

// DetermineCurveType classifies the broad shape of a VES response.
// It uses the smoothed log-log curve, not just its first and last points.
func DetermineCurveType(
	boundaries []LayerBoundary,
	measurements []models.Measurement,
) string {
	_ = boundaries // Classification is based on observed curve shape.

	data := prepareVESData(measurements)
	if len(data) < 4 {
		return "undetermined"
	}

	logRho := make([]float64, len(data))
	for i, m := range data {
		logRho[i] = math.Log10(m.ApparentResistivity)
	}
	logRho = smoothLogValues(logRho)

	positive := 0
	negative := 0
	slopes := make([]float64, len(data)-1)

	for i := 0; i < len(data)-1; i++ {
		dx := math.Log10(data[i+1].AB2) -
			math.Log10(data[i].AB2)

		if dx <= 0 {
			continue
		}

		slopes[i] = (logRho[i+1] - logRho[i]) / dx

		if slopes[i] > 0.05 {
			positive++
		} else if slopes[i] < -0.05 {
			negative++
		}
	}

	// Find the strongest internal minimum and maximum.
	minIndex, maxIndex := 0, 0
	for i := 1; i < len(logRho)-1; i++ {
		if logRho[i] < logRho[minIndex] {
			minIndex = i
		}
		if logRho[i] > logRho[maxIndex] {
			maxIndex = i
		}
	}

	// A meaningful internal minimum with a decreasing approach
	// and increasing departure suggests an H-type response.
	if minIndex > 0 && minIndex < len(logRho)-1 {
		leftSlope := slopes[minIndex-1]
		rightSlope := slopes[minIndex]

		if leftSlope < -0.05 && rightSlope > 0.05 {
			return "H"
		}
	}

	// A meaningful internal maximum with an increasing approach
	// and decreasing departure suggests a K-type response.
	if maxIndex > 0 && maxIndex < len(logRho)-1 {
		leftSlope := slopes[maxIndex-1]
		rightSlope := slopes[maxIndex]

		if leftSlope > 0.05 && rightSlope < -0.05 {
			return "K"
		}
	}

	if positive > 0 && negative == 0 {
		return "A"
	}
	if negative > 0 && positive == 0 {
		return "Q"
	}

	if positive > negative {
		return "A-like mixed"
	}
	if negative > positive {
		return "Q-like mixed"
	}

	return "mixed"
}

type LayerFitResult struct {
	Resistivities []float64
	Thicknesses   []float64
	Computed      []float64
	RMSLogError   float64
	// Sensitivity records the change in LogRMS after a +/-10% perturbation
	// of each fitted parameter. It is a local diagnostic, not a confidence interval.
	Sensitivity []LayerParameterSensitivity
}

// LayerParameterSensitivity describes local objective sensitivity.
// Small score changes indicate that the parameter is weakly constrained by
// the available sounding data.
type LayerParameterSensitivity struct {
	Parameter        string
	Minus10PctLogRMS float64
	Plus10PctLogRMS  float64
	MinDeltaLogRMS   float64
}

// initialLayerResistivities estimates starting resistivities from
// geometric means of measurements divided into AB/2 intervals.
// These are starting values only, not final layer resistivities.
func initialLayerResistivities(
	measurements []models.Measurement,
	boundaries []LayerBoundary,
) []float64 {
	n := len(boundaries) + 1
	logSums := make([]float64, n)
	counts := make([]int, n)

	for _, m := range measurements {
		layer := 0

		for _, b := range boundaries {
			if m.AB2 > b.AB2 {
				layer++
			}
		}

		if layer >= n {
			layer = n - 1
		}

		if m.ApparentResistivity > 0 {
			logSums[layer] += math.Log(m.ApparentResistivity)
			counts[layer]++
		}
	}

	rho := make([]float64, n)
	for i := range rho {
		if counts[i] > 0 {
			rho[i] = math.Exp(logSums[i] / float64(counts[i]))
		} else if i > 0 {
			rho[i] = rho[i-1]
		} else {
			rho[i] = 100
		}
	}

	return rho
}

func layerFitObjective(
	measurements []models.Measurement,
	resistivities []float64,
	thicknesses []float64,
) float64 {
	if len(measurements) == 0 {
		return math.Inf(1)
	}

	ab2 := make([]float64, len(measurements))
	observed := make([]float64, len(measurements))

	for i, m := range measurements {
		if !isPositiveFinite(m.AB2) ||
			!isPositiveFinite(m.ApparentResistivity) {
			return math.Inf(1)
		}
		ab2[i] = m.AB2
		observed[i] = m.ApparentResistivity
	}

	computed := ForwardModel(ab2, resistivities, thicknesses)

	metrics, err := CalculateFitMetrics(observed, computed)
	if err != nil {
		return math.Inf(1)
	}

	return metrics.LogRMS
}

// FitLayerModel fits a fixed number of layers to the complete observed
// apparent-resistivity curve. The supplied boundaries are initial estimates.

func isPositiveFinite(value float64) bool {
	return value > 0 &&
		!math.IsNaN(value) &&
		!math.IsInf(value, 0)
}

func fitLayerModelFromStart(
	data []models.Measurement,
	boundaries []LayerBoundary,
	layerCount int,
	initialRho []float64,
	initialThickness []float64,
) (LayerFitResult, error) {

	if layerCount < 1 {
		return LayerFitResult{}, fmt.Errorf("layer count must be at least 1")
	}

	if len(data) < 2*layerCount-1 {
		return LayerFitResult{}, fmt.Errorf(
			"need at least %d valid measurements to fit %d layers; got %d",
			2*layerCount-1,
			layerCount,
			len(data),
		)
	}

	if len(boundaries) != layerCount-1 {
		return LayerFitResult{}, fmt.Errorf(
			"expected %d initial boundaries, got %d",
			layerCount-1, len(boundaries),
		)
	}

	sort.Slice(boundaries, func(i, j int) bool {
		return boundaries[i].AB2 < boundaries[j].AB2
	})

	for _, b := range boundaries {
		if b.AB2 <= 0 {
			return LayerFitResult{}, fmt.Errorf(
				"boundary AB/2 must be positive",
			)
		}
	}

	rho := append([]float64(nil), initialRho...)
	thickness := append([]float64(nil), initialThickness...)

	if len(rho) != layerCount {
		return LayerFitResult{}, fmt.Errorf(
			"expected %d initial resistivities, got %d",
			layerCount, len(rho),
		)
	}

	if len(thickness) != layerCount-1 {
		return LayerFitResult{}, fmt.Errorf(
			"expected %d initial thicknesses, got %d",
			layerCount-1, len(thickness),
		)
	}

	for _, value := range rho {
		if !isPositiveFinite(value) {
			return LayerFitResult{}, fmt.Errorf(
				"initial resistivities must be positive and finite",
			)
		}
	}

	for _, value := range thickness {
		if !isPositiveFinite(value) {
			return LayerFitResult{}, fmt.Errorf(
				"initial thicknesses must be positive and finite",
			)
		}
	}
	best := layerFitObjective(data, rho, thickness)
	if math.IsInf(best, 0) || math.IsNaN(best) {
		return LayerFitResult{}, fmt.Errorf(
			"initial model produced an invalid forward response",
		)
	}

	// Optimize log-parameters with damped least squares (Levenberg-Marquardt).
	// A coordinate-search pass is retained as a fallback if LM cannot improve
	// the starting model, since the forward response can be locally ill-conditioned.
	parameters := make([]float64, 0, len(rho)+len(thickness))
	for _, value := range rho {
		parameters = append(parameters, math.Log(value))
	}
	for _, value := range thickness {
		parameters = append(parameters, math.Log(value))
	}
	lower := make([]float64, len(parameters))
	upper := make([]float64, len(parameters))
	for i := range rho {
		lower[i], upper[i] = math.Log(1), math.Log(100000)
	}
	for i := range thickness {
		j := len(rho) + i
		lower[j], upper[j] = math.Log(0.5), math.Log(500)
	}
	parameters, best = optimizeVESLevenbergMarquardt(data, parameters, layerCount, lower, upper, best)
	for i := range parameters {
		parameters[i] = math.Exp(parameters[i])
	}

	rho = append([]float64(nil), parameters[:layerCount]...)
	thickness = append([]float64(nil), parameters[layerCount:]...)

	ab2 := make([]float64, len(data))
	for i, m := range data {
		ab2[i] = m.AB2
	}

	computed := ForwardModel(ab2, rho, thickness)

	return LayerFitResult{
		Resistivities: rho,
		Thicknesses:   thickness,
		Computed:      computed,
		RMSLogError:   best,
	}, nil
}

// optimizeVESLevenbergMarquardt minimizes log-space residuals using numerical
// derivatives and a damped Gauss-Newton step. Parameters are supplied in log space.
func optimizeVESLevenbergMarquardt(data []models.Measurement, x []float64, layerCount int, lower, upper []float64, initialScore float64) ([]float64, float64) {
	bestX := append([]float64(nil), x...)
	residuals := vesLogResiduals(data, bestX, layerCount)
	score := residualRMS(residuals)
	if !isPositiveFinite(score) && score != 0 {
		return bestX, initialScore
	}
	if initialScore < score {
		score = initialScore
	}
	lambda := 1e-2
	for iter := 0; iter < 100; iter++ {
		n, m := len(bestX), len(residuals)
		jac := make([][]float64, m)
		for i := range jac {
			jac[i] = make([]float64, n)
		}
		for j := 0; j < n; j++ {
			xp := append([]float64(nil), bestX...)
			h := 1e-4
			xp[j] = math.Min(upper[j], bestX[j]+h)
			xm := append([]float64(nil), bestX...)
			xm[j] = math.Max(lower[j], bestX[j]-h)
			rp, rm := vesLogResiduals(data, xp, layerCount), vesLogResiduals(data, xm, layerCount)
			denom := xp[j] - xm[j]
			if denom == 0 {
				continue
			}
			for i := 0; i < m; i++ {
				jac[i][j] = (rp[i] - rm[i]) / denom
			}
		}
		normal := make([][]float64, n)
		rhs := make([]float64, n)
		for i := 0; i < n; i++ {
			normal[i] = make([]float64, n)
			for j := 0; j < n; j++ {
				for k := 0; k < m; k++ {
					normal[i][j] += jac[k][i] * jac[k][j]
				}
			}
			for k := 0; k < m; k++ {
				rhs[i] -= jac[k][i] * residuals[k]
			}
			normal[i][i] += lambda * math.Max(normal[i][i], 1e-8)
		}
		delta, ok := solveDenseSystem(normal, rhs)
		if !ok {
			lambda *= 10
			if lambda > 1e12 {
				break
			}
			continue
		}
		candidate := append([]float64(nil), bestX...)
		for i := range candidate {
			candidate[i] = math.Max(lower[i], math.Min(upper[i], candidate[i]+delta[i]))
		}
		candidateResiduals := vesLogResiduals(data, candidate, layerCount)
		candidateScore := residualRMS(candidateResiduals)
		if isFinite(candidateScore) && candidateScore < score {
			bestX, residuals, score = candidate, candidateResiduals, candidateScore
			lambda = math.Max(lambda/3, 1e-12)
			if maxAbs(delta) < 1e-6 {
				break
			}
		} else {
			lambda *= 10
			if lambda > 1e12 {
				break
			}
		}
	}
	// Never return a worse score than the original coordinate-search start.
	if score > initialScore {
		return x, initialScore
	}
	return bestX, score
}

func vesLogResiduals(data []models.Measurement, logParams []float64, layerCount int) []float64 {
	rho := make([]float64, layerCount)
	thickness := make([]float64, len(logParams)-layerCount)
	for i := range rho {
		rho[i] = math.Exp(logParams[i])
	}
	for i := range thickness {
		thickness[i] = math.Exp(logParams[layerCount+i])
	}
	ab2 := make([]float64, len(data))
	observed := make([]float64, len(data))
	for i, d := range data {
		ab2[i], observed[i] = d.AB2, d.ApparentResistivity
	}
	computed := ForwardModel(ab2, rho, thickness)
	result := make([]float64, len(data))
	for i := range data {
		if !isPositiveFinite(computed[i]) || !isPositiveFinite(observed[i]) {
			result[i] = math.Inf(1)
			continue
		}
		result[i] = math.Log(computed[i]) - math.Log(observed[i])
	}
	return result
}

func residualRMS(r []float64) float64 {
	if len(r) == 0 {
		return math.Inf(1)
	}
	sum := 0.0
	for _, v := range r {
		if !isFinite(v) {
			return math.Inf(1)
		}
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(r)))
}

func solveDenseSystem(a [][]float64, b []float64) ([]float64, bool) {
	n := len(b)
	for i := 0; i < n; i++ {
		pivot := i
		for r := i + 1; r < n; r++ {
			if math.Abs(a[r][i]) > math.Abs(a[pivot][i]) {
				pivot = r
			}
		}
		if math.Abs(a[pivot][i]) < 1e-18 {
			return nil, false
		}
		a[i], a[pivot], b[i], b[pivot] = a[pivot], a[i], b[pivot], b[i]
		d := a[i][i]
		for j := i; j < n; j++ {
			a[i][j] /= d
		}
		b[i] /= d
		for r := 0; r < n; r++ {
			if r == i {
				continue
			}
			f := a[r][i]
			for j := i; j < n; j++ {
				a[r][j] -= f * a[i][j]
			}
			b[r] -= f * b[i]
		}
	}
	return b, true
}

func maxAbs(v []float64) float64 {
	m := 0.0
	for _, x := range v {
		if math.Abs(x) > m {
			m = math.Abs(x)
		}
	}
	return m
}
func isFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func FitLayerModel(
	measurements []models.Measurement,
	boundaries []LayerBoundary,
	layerCount int,
) (LayerFitResult, error) {
	data := prepareVESData(measurements)

	if layerCount < 1 {
		return LayerFitResult{}, fmt.Errorf(
			"layer count must be at least 1",
		)
	}

	if len(data) < 2*layerCount-1 {
		return LayerFitResult{}, fmt.Errorf(
			"need at least %d valid measurements to fit %d layers; got %d",
			2*layerCount-1,
			layerCount,
			len(data),
		)
	}

	if len(boundaries) != layerCount-1 {
		return LayerFitResult{}, fmt.Errorf(
			"expected %d initial boundaries, got %d",
			layerCount-1, len(boundaries),
		)
	}

	boundaries = append([]LayerBoundary(nil), boundaries...)

	sort.Slice(boundaries, func(i, j int) bool {
		return boundaries[i].AB2 < boundaries[j].AB2
	})

	for _, b := range boundaries {
		if !isPositiveFinite(b.AB2) {
			return LayerFitResult{}, fmt.Errorf(
				"boundary AB/2 must be positive and finite",
			)
		}
	}

	baseRho := initialLayerResistivities(data, boundaries)

	baseThickness := make([]float64, layerCount-1)
	for i, b := range boundaries {
		baseThickness[i] = math.Max(b.AB2/2, 1.0)
	}

	// Deterministic multistart search. Uniform scales alone can leave the
	// optimizer trapped when one layer (especially the middle layer of a
	// K-type curve) needs a very different initial resistivity.
	type fitStart struct {
		rhoScale       []float64
		thicknessScale []float64
	}
	starts := []fitStart{
		{[]float64{1, 1, 1}, []float64{1, 1}},
		{[]float64{0.5, 1, 2}, []float64{0.7, 1.4}},
		{[]float64{2, 1, 0.5}, []float64{1.4, 0.7}},
		{[]float64{1, 0.25, 1}, []float64{1, 1}},
		{[]float64{1, 4, 1}, []float64{1, 1}},
		{[]float64{0.25, 1, 4}, []float64{0.5, 2}},
		{[]float64{4, 1, 0.25}, []float64{2, 0.5}},
		{[]float64{0.5, 2, 0.5}, []float64{0.5, 2}},
		{[]float64{2, 0.5, 2}, []float64{2, 0.5}},
	}
	// Extend the scale templates to arbitrary layer counts by applying the
	// nearest available template factor cyclically.

	var bestFit LayerFitResult
	bestScore := math.Inf(1)
	var lastErr error

	for _, start := range starts {
		initialRho := make([]float64, len(baseRho))
		for i, value := range baseRho {
			scale := start.rhoScale[i%len(start.rhoScale)]
			initialRho[i] = math.Max(1, math.Min(100000, value*scale))
		}

		initialThickness := make([]float64, len(baseThickness))
		for i, value := range baseThickness {
			scale := start.thicknessScale[i%len(start.thicknessScale)]
			initialThickness[i] = math.Max(0.5, math.Min(500, value*scale))
		}

		fit, err := fitLayerModelFromStart(
			data,
			boundaries,
			layerCount,
			initialRho,
			initialThickness,
		)
		if err != nil {
			lastErr = err
			continue
		}

		// Reject NaN, infinity, and negative scores.
		// A score of zero is valid.
		if math.IsNaN(fit.RMSLogError) ||
			math.IsInf(fit.RMSLogError, 0) ||
			fit.RMSLogError < 0 {
			continue
		}

		if fit.RMSLogError < bestScore {
			bestScore = fit.RMSLogError
			bestFit = fit
		}
	}

	if math.IsInf(bestScore, 1) {
		if lastErr != nil {
			return LayerFitResult{}, fmt.Errorf(
				"all optimization starts failed: %w", lastErr,
			)
		}

		return LayerFitResult{}, fmt.Errorf(
			"all optimization starts produced invalid fit scores",
		)
	}

	bestFit.Sensitivity = calculateLayerFitSensitivity(data, bestFit)
	return bestFit, nil
}

func calculateLayerFitSensitivity(
	data []models.Measurement,
	fit LayerFitResult,
) []LayerParameterSensitivity {
	result := make([]LayerParameterSensitivity, 0,
		len(fit.Resistivities)+len(fit.Thicknesses))

	for i, value := range fit.Resistivities {
		minusRho := append([]float64(nil), fit.Resistivities...)
		plusRho := append([]float64(nil), fit.Resistivities...)
		minusRho[i] = value * 0.9
		plusRho[i] = value * 1.1
		minusScore := layerFitObjective(data, minusRho, fit.Thicknesses)
		plusScore := layerFitObjective(data, plusRho, fit.Thicknesses)
		result = append(result, LayerParameterSensitivity{
			Parameter:        fmt.Sprintf("resistivity_layer_%d", i+1),
			Minus10PctLogRMS: minusScore,
			Plus10PctLogRMS:  plusScore,
			MinDeltaLogRMS:   math.Min(minusScore-fit.RMSLogError, plusScore-fit.RMSLogError),
		})
	}
	for i, value := range fit.Thicknesses {
		minusThickness := append([]float64(nil), fit.Thicknesses...)
		plusThickness := append([]float64(nil), fit.Thicknesses...)
		minusThickness[i] = value * 0.9
		plusThickness[i] = value * 1.1
		minusScore := layerFitObjective(data, fit.Resistivities, minusThickness)
		plusScore := layerFitObjective(data, fit.Resistivities, plusThickness)
		result = append(result, LayerParameterSensitivity{
			Parameter:        fmt.Sprintf("thickness_layer_%d", i+1),
			Minus10PctLogRMS: minusScore,
			Plus10PctLogRMS:  plusScore,
			MinDeltaLogRMS:   math.Min(minusScore-fit.RMSLogError, plusScore-fit.RMSLogError),
		})
	}
	return result
}

// =====================================================
// DETECT LAYERS
// =====================================================
// func DetectLayers(measurements []models.Measurement) []LayerBoundary {
// 	if len(measurements) < 3 {
// 		return nil
// 	}

// 	n := len(measurements)
// 	logAB2 := make([]float64, n)
// 	logRhoA := make([]float64, n)

// 	for i, m := range measurements {
// 		logAB2[i] = math.Log10(m.AB2)
// 		logRhoA[i] = math.Log10(m.ApparentResistivity)
// 	}

// 	slopes := make([]float64, n-1)
// 	for i := 0; i < n-1; i++ {
// 		dX := logAB2[i+1] - logAB2[i]
// 		if dX == 0 {
// 			slopes[i] = 0
// 			continue
// 		}
// 		slopes[i] = (logRhoA[i+1] - logRhoA[i]) / dX
// 	}

// 	log.Println("=== SLOPE ANALYSIS ===")
// 	for i, s := range slopes {
// 		log.Printf("Point %d: AB2=%.1f, slope=%.4f\n", i, measurements[i].AB2, s)
// 	}
// 	log.Println("slopes:", slopes)

// 	var boundaries []LayerBoundary

// 	for i := 1; i < len(slopes); i++ {
// 		if math.IsInf(slopes[i], 0) || math.IsInf(slopes[i-1], 0) {
// 			continue
// 		}

// 		PrevSlope := slopes[i-1]
// 		CurrSlope := slopes[i]
// 		feature := ""
// 		slopeChange := math.Abs(CurrSlope - PrevSlope)

// 		// General inflection — significant slope change
// 		if math.Abs(CurrSlope-PrevSlope) > 0.3 && feature == "" {
// 			feature = "inflection"
// 		}
// 		// peak (K curve)
// 		if PrevSlope > 0.2 && CurrSlope < -0.1 {
// 			feature = "peak"
// 		}

// 		// trough (H curve)
// 		if PrevSlope < -0.1 && CurrSlope > 0.05 {
// 			feature = "trough"
// 		}

// 		// flattening (Q curve)
// 		if math.Abs(CurrSlope) < 0.1 && PrevSlope > 0.2 {
// 			feature = "flatten"
// 		}

// 		if feature != "" {
// 			boundaries = append(boundaries, LayerBoundary{
// 				AB2:         measurements[i].AB2,
// 				RhoA:        measurements[i].ApparentResistivity,
// 				FeatureType: feature,
// 				SlopeChange: slopeChange,
// 			})
// 		}
// 	}

// 	log.Println("boundaries found:", len(boundaries))
// 	return boundaries
// }

// // utils/layers.go

// // DetectLayersN detects exactly n layers from measurements.
// // It wraps DetectLayers and applies filtering:
// // 1. Requires at least n-1 boundaries (one per layer transition)
// // 2. Selects the n-1 boundaries with largest absolute slope changes
// // 3. Returns them sorted by depth (AB/2 ascending)
// // utils/detect_layers.go

// const MinBoundaryRatio = 1.5

// func DetectLayersN(measurements []models.Measurement, n int) ([]LayerBoundary, error) {

// 	if n < 1 {
// 		return nil, fmt.Errorf("n must be at least 1, got %d", n)
// 	}

// 	allBoundaries := DetectLayers(measurements)
// 	requiredBoundaries := n - 1
// 	// Filter out very shallow boundaries — likely surface noise
// 	minAB2 := 5.0
// 	filtered := []LayerBoundary{}
// 	for _, b := range allBoundaries {
// 		if b.AB2 >= minAB2 {
// 			filtered = append(filtered, b)
// 		}
// 	}
// 	allBoundaries = filtered
// 	// Separate by type, sort each by AB/2 ascending
// 	var troughs, peaks, inflections []LayerBoundary
// 	for _, b := range allBoundaries {
// 		switch b.FeatureType {
// 		case "trough":
// 			troughs = append(troughs, b)
// 		case "peak":
// 			peaks = append(peaks, b)
// 		default:
// 			inflections = append(inflections, b)
// 		}
// 	}

// 	sortByAB2 := func(s []LayerBoundary) {
// 		sort.Slice(s, func(i, j int) bool { return s[i].AB2 < s[j].AB2 })
// 	}
// 	sortByAB2(troughs)
// 	sortByAB2(peaks)
// 	sortByAB2(inflections)

// 	prioritised := append(append(troughs, peaks...), inflections...)

// 	for i, b := range prioritised {
// 		log.Printf("  prioritised[%d]: AB2=%.1f type=%s\n", i, b.AB2, b.FeatureType)
// 	}

// 	// Select with spacing validation during selection
// 	selected := []LayerBoundary{}
// 	for _, b := range prioritised {
// 		if len(selected) == 0 {
// 			selected = append(selected, b)
// 			continue
// 		}
// 		last := selected[len(selected)-1]
// 		ratio := b.AB2 / last.AB2
// 		if ratio < 1.0 {
// 			ratio = last.AB2 / b.AB2
// 		}
// 		if ratio >= MinBoundaryRatio {
// 			selected = append(selected, b)
// 		}
// 		if len(selected) == requiredBoundaries {
// 			break
// 		}
// 	}

// 	if len(selected) < requiredBoundaries {
// 		return nil, fmt.Errorf(
// 			"insufficient well-separated boundaries: found %d, need %d for %d layers — reduce layer count or check data quality",
// 			len(selected), requiredBoundaries, n,
// 		)
// 	}

// 	// Sort final selection by AB/2 ascending
// 	sort.Slice(selected, func(i, j int) bool { return selected[i].AB2 < selected[j].AB2 })

// 	log.Printf("DEBUG DetectLayersN: n=%d, requiredBoundaries=%d, selected=%d boundaries\n",
// 		n, requiredBoundaries, len(selected))

// 	return selected, nil
// }

// =====================================================
// DETERMINE CURVE TYPE
// =====================================================

// func determineTrend(measurements []models.Measurement) string {
// 	if len(measurements) < 2 {
// 		return "A"
// 	}
// 	first := measurements[0].ApparentResistivity
// 	last := measurements[len(measurements)-1].ApparentResistivity
// 	if last > first {
// 		return "A"
// 	}
// 	return "Q"
// }

// func isNoisy(measurements []models.Measurement) bool {
// 	changes := 0
// 	for i := 1; i < len(measurements)-1; i++ {
// 		prev := measurements[i].ApparentResistivity - measurements[i-1].ApparentResistivity
// 		next := measurements[i+1].ApparentResistivity - measurements[i].ApparentResistivity
// 		if (prev > 0 && next < 0) || (prev < 0 && next > 0) {
// 			changes++
// 		}
// 	}
// 	return float64(changes)/float64(len(measurements)) > 0.3
// }

// func DetermineCurveType(
// 	boundaries []LayerBoundary,
// 	measurements []models.Measurement,
// ) string {
// 	pattern := ""
// 	for _, b := range boundaries {
// 		switch b.FeatureType {
// 		case "peak":
// 			pattern += "K"
// 		case "trough":
// 			pattern += "H"
// 		case "flatten":
// 			pattern += "Q"
// 		}
// 	}

// 	if pattern == "" {
// 		trend := determineTrend(measurements)
// 		if isNoisy(measurements) {
// 			return trend + "(noisy)"
// 		}
// 		return trend
// 	}

// 	return pattern
// }

// =====================================================
// INTERPRET GEOLOGY
// =====================================================

func InterpretGeology(curveType, terrainType string) string {
	basement := map[string]string{
		"A":   "Fresh basement near surface. Resistivity increases downward. Competent fresh granite likely.",
		"H":   "Saturated saprolite. Conductive middle layer — potential aquifer zone.",
		"K":   "Resistive laterite over clay or dry fractured basement.",
		"Q":   "Clayey overburden. Resistivity decreases downward.",
		"KH":  "Fractured water-filled basement. Good borehole prospect.",
		"HK":  "Fractured water-filled basement. Alternating transitions.",
		"HKH": "Multiple fracture systems. Complex alternations.",
		"QH":  "Progressive conductivity then resistive basement.",
	}

	sedimentary := map[string]string{
		"A":  "Sandy/gravelly formation with increasing resistivity. Prolific aquifer zone likely. Excellent borehole prospect.",
		"H":  "Clay-bound water in conductive middle layer. Moderate aquifer potential.",
		"K":  "Resistive sand layer sandwiched between conductive layers.",
		"Q":  "Clayey sedimentary sequence. Resistivity decreases with depth.",
		"KH": "Complex sedimentary sequence. Possible confined aquifer.",
		"HK": "Alternating clay-sand sequence. Good aquifer potential.",
	}

	transition := map[string]string{
		"A":  "Mixed basement-sedimentary sequence. Increasing resistivity with depth.",
		"H":  "Conductive middle layer in transition zone. Possible weathered zone aquifer.",
		"K":  "Resistive layer in transition zone.",
		"Q":  "Decreasing resistivity. Possible deep clay sequence.",
		"KH": "Complex transition zone. Fractures and sediments present.",
	}

	var selected map[string]string
	switch strings.ToLower(terrainType) {
	case "basement":
		selected = basement
	case "sedimentary":
		selected = sedimentary
	case "transition":
		selected = transition
	default:
		selected = basement
	}

	// check noisy variant
	cleanType := strings.Replace(curveType, "(noisy)", "", 1)
	if interpretation, ok := selected[cleanType]; ok {
		if strings.Contains(curveType, "noisy") {
			return interpretation + " Note: Noisy data detected — field conditions may have affected measurements."
		}
		return interpretation
	}

	return "Complex curve. Manual interpretation recommended."
}

// =====================================================
// ESTIMATE DEPTHS
// =====================================================

func EstimateDepths(boundaries []LayerBoundary) []float64 {
	depths := []float64{}
	for _, b := range boundaries {
		depth := b.AB2 * 0.25
		depths = append(depths, depth)
	}
	return depths
}

// =====================================================
// AQUIFER PROBABILITY
// =====================================================

func AquiferProbability(curveType, terrainType string) float64 {
	cleanType := strings.Replace(curveType, "(noisy)", "", 1)

	basement := map[string]float64{
		"H": 0.85, "KH": 0.80, "HKH": 0.75,
		"KHK": 0.70, "HK": 0.78, "QH": 0.65,
		"K": 0.40, "A": 0.25, "Q": 0.30,
	}

	sedimentary := map[string]float64{
		"A": 0.85, "H": 0.75, "KH": 0.70,
		"K": 0.55, "Q": 0.45, "HK": 0.65,
	}

	switch strings.ToLower(terrainType) {
	case "basement":
		if prob, ok := basement[cleanType]; ok {
			return prob
		}
	case "sedimentary":
		if prob, ok := sedimentary[cleanType]; ok {
			return prob
		}
	case "transition":
		bProb := basement[cleanType]
		sProb := sedimentary[cleanType]
		if bProb > 0 && sProb > 0 {
			return (bProb + sProb) / 2
		}
		if bProb > 0 {
			return bProb * 0.8
		}
		if sProb > 0 {
			return sProb * 0.8
		}
	}
	return 0.50
}

// =====================================================
// RECOMMEND DRILL DEPTH
// =====================================================

func RecommendDrillDepth(
	curveType string,
	terrainType string,
	depths []float64,
	measurements []models.Measurement,
	prob float64,
) (float64, bool) {

	if prob < 0.65 {
		return 0, false
	}

	cleanType := strings.Replace(curveType, "(noisy)", "", 1)

	if len(depths) == 0 {
		if len(measurements) == 0 {
			return 0, true
		}
		lastAB2 := measurements[len(measurements)-1].AB2
		switch strings.ToLower(terrainType) {
		case "sedimentary":
			return lastAB2 * 0.6, true
		case "basement":
			return lastAB2 * 0.4, true
		default:
			return lastAB2 * 0.5, true
		}
	}

	lastDepth := depths[len(depths)-1]

	switch strings.ToLower(terrainType) {
	case "basement":
		switch {
		case strings.Contains(cleanType, "HKH"):
			return lastDepth + 25, true
		case strings.Contains(cleanType, "KH"):
			return lastDepth + 20, true
		case strings.Contains(cleanType, "H"):
			return lastDepth + 15, true
		case cleanType == "K":
			return lastDepth + 10, true
		default:
			return lastDepth + 12, true
		}
	case "sedimentary":
		switch {
		case cleanType == "A":
			return lastDepth + 20, true
		case strings.Contains(cleanType, "H"):
			return lastDepth + 15, true
		default:
			return lastDepth + 15, true
		}
	default:
		return lastDepth + 18, true
	}
}

// =====================================================
// ESTIMATE OVERBURDEN
// =====================================================

func EstimateOverburden(
	boundaries []LayerBoundary,
	terrainType string,
) OverburdenInfo {

	if strings.ToLower(terrainType) != "basement" {
		return OverburdenInfo{}
	}

	if len(boundaries) == 0 {
		return OverburdenInfo{
			Description:  "Indeterminate",
			Significance: "Insufficient data to estimate overburden",
		}
	}

	thickness := boundaries[0].AB2 * 0.25

	significance := ""
	switch {
	case thickness < 5:
		significance = "Thin overburden — basement very shallow. Low borehole prospect."
	case thickness < 15:
		significance = "Moderate overburden — possible weathered zone."
	case thickness < 30:
		significance = "Thick overburden — good weathering profile. Better aquifer prospect."
	default:
		significance = "Very thick overburden — deep weathering. Excellent aquifer prospect."
	}

	return OverburdenInfo{
		Thickness:    thickness,
		Description:  fmt.Sprintf("%.1fm", thickness),
		Significance: significance,
	}
}

// =====================================================
// SUGGEST LAYERS
// =====================================================

func SuggestLayers(curveType, terrainType string) []models.Layer {
	cleanType := strings.Replace(curveType, "(noisy)", "", 1)

	switch strings.ToLower(terrainType) {
	case "sedimentary":
		switch cleanType {
		case "A":
			return []models.Layer{
				{LayerNumber: 1, Lithology: "Topsoil"},
				{LayerNumber: 2, Lithology: "Sandy Clay"},
				{LayerNumber: 3, Lithology: "Sand/Gravel (Aquifer)"},
				{LayerNumber: 4, Lithology: "Clayey Sand"},
				{LayerNumber: 5, Lithology: "Sand Aquifer"},
			}
		case "H":
			return []models.Layer{
				{LayerNumber: 1, Lithology: "Topsoil"},
				{LayerNumber: 2, Lithology: "Clay"},
				{LayerNumber: 3, Lithology: "Sand Aquifer"},
			}
		}
	case "basement":
		switch cleanType {
		case "H":
			return []models.Layer{
				{LayerNumber: 1, Lithology: "Topsoil"},
				{LayerNumber: 2, Lithology: "Weathered Basement"},
				{LayerNumber: 3, Lithology: "Fractured Basement"},
				{LayerNumber: 4, Lithology: "Fresh Basement"},
			}
		case "KH":
			return []models.Layer{
				{LayerNumber: 1, Lithology: "Topsoil"},
				{LayerNumber: 2, Lithology: "Laterite"},
				{LayerNumber: 3, Lithology: "Weathered Basement"},
				{LayerNumber: 4, Lithology: "Fractured Basement"},
			}
		case "A":
			return []models.Layer{
				{LayerNumber: 1, Lithology: "Topsoil"},
				{LayerNumber: 2, Lithology: "Weathered Basement"},
				{LayerNumber: 3, Lithology: "Fresh Basement"},
			}
		}
	}
	return []models.Layer{}
}

// =====================================================
// ANALYZE GEOLOGIC HISTORY
// =====================================================

func AnalyzeGeologicHistory(
	history string,
	previousWells string,
	curveType string,
	terrainType string,
	suggestedDepth float64,
) GeologicContext {

	ctx := GeologicContext{}
	history = strings.ToLower(history)
	wells := strings.ToLower(previousWells)

	if strings.Contains(history, "hill") ||
		strings.Contains(history, "rocky") {
		ctx.Warnings = append(ctx.Warnings,
			"⚠️ Hilly/rocky terrain — basement likely shallow")
		ctx.DepthAdjust -= 5
	}

	if strings.Contains(history, "valley") ||
		strings.Contains(history, "river") ||
		strings.Contains(history, "stream") {
		ctx.Suggestions = append(ctx.Suggestions,
			"✅ Valley/river proximity — deeper weathering likely. Good aquifer prospect.")
		ctx.DepthAdjust += 8
	}

	if strings.Contains(history, "clay") {
		ctx.Suggestions = append(ctx.Suggestions,
			"ℹ️ Clay zones noted — expect low resistivity layers")
	}

	if strings.Contains(history, "fracture") ||
		strings.Contains(history, "fractured") {
		ctx.Suggestions = append(ctx.Suggestions,
			"✅ Fractured zones noted — enhanced aquifer potential")
		ctx.DepthAdjust += 5
	}

	depthPattern := regexp.MustCompile(`(\d+)\s*m`)
	matches := depthPattern.FindAllStringSubmatch(wells, -1)

	for _, match := range matches {
		wellDepth, _ := strconv.ParseFloat(match[1], 64)

		if wellDepth > suggestedDepth {
			ctx.Warnings = append(ctx.Warnings,
				fmt.Sprintf("⚠️ Nearby well at %.0fm — consider drilling to similar depth", wellDepth))
			ctx.DepthAdjust = wellDepth - suggestedDepth
		}

		if strings.Contains(wells, "failed") ||
			strings.Contains(wells, "dry") {
			ctx.Warnings = append(ctx.Warnings,
				"⚠️ Failed/dry wells nearby — exercise caution")
		}

		if strings.Contains(wells, "moderate") ||
			strings.Contains(wells, "good") {
			ctx.Suggestions = append(ctx.Suggestions,
				fmt.Sprintf("✅ Productive well nearby at %.0fm — good indicator", wellDepth))
		}
	}

	return ctx
}

// =====================================================
// INTERPRET VES
// =====================================================

func InterpretVES(
	measurements []models.Measurement,
	terrainType string,
	geologicHistory string,
	previousWells string,
) VESInterpretation {

	measurements = AverageRepeatedAB2(measurements)
	measurements = SmoothMeasurements(measurements)

	boundaries := DetectLayers(measurements)
	curveType := DetermineCurveType(boundaries, measurements)
	geology := InterpretGeology(curveType, terrainType)
	depths := EstimateDepths(boundaries)
	prob := AquiferProbability(curveType, terrainType)

	drillDepth, drillRecommended := RecommendDrillDepth(
		curveType,
		terrainType,
		depths,
		measurements,
		prob,
	)

	geoCtx := AnalyzeGeologicHistory(
		geologicHistory,
		previousWells,
		curveType,
		terrainType,
		drillDepth,
	)

	if drillRecommended && geoCtx.DepthAdjust != 0 {
		drillDepth += geoCtx.DepthAdjust
		if drillDepth < 0 {
			drillDepth = 0
		}
	}

	overburden := EstimateOverburden(boundaries, terrainType)

	return VESInterpretation{
		Boundaries:            boundaries,
		CurveType:             curveType,
		Geology:               geology,
		Depths:                depths,
		DrillRecommended:      drillRecommended,
		DrillDepth:            drillDepth,
		AquiferLikely:         prob >= 0.65,
		AquiferProbability:    prob,
		AquiferProbabilityPct: prob * 100,
		SuggestedLayers:       SuggestLayers(curveType, terrainType),
		GeologicContext:       geoCtx,
		OverburdenInfo:        overburden,
	}
}
