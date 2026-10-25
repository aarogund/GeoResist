package utils

import (
	"errors"
	"math"
)

type FitMetrics struct {
	Points              int     `json:"points"`
	LogRMS              float64 `json:"log_rms"`
	RMSPercent          float64 `json:"rms_percent"`
	MeanAbsolutePercent float64 `json:"mean_absolute_percent"`
	MaxAbsolutePercent  float64 `json:"max_absolute_percent"`
}

// CalculateFitMetrics calculates fit statistics from observed and
// computed apparent-resistivity values.
//
// LogRMS is the root mean square of natural-log residuals:
//
//	sqrt(mean(ln(computed / observed)^2))
//
// Percentage metrics use the observed value as the denominator.
func CalculateFitMetrics(
	observed []float64,
	computed []float64,
) (FitMetrics, error) {
	if len(observed) == 0 {
		return FitMetrics{}, errors.New("no observed measurements")
	}

	if len(observed) != len(computed) {
		return FitMetrics{}, errors.New(
			"observed and computed arrays have different lengths",
		)
	}

	var sumLogSquared float64
	var sumPercentSquared float64
	var sumAbsolutePercent float64
	var maxAbsolutePercent float64

	for i := range observed {
		obs := observed[i]
		calc := computed[i]

		if math.IsNaN(obs) || math.IsInf(obs, 0) ||
			math.IsNaN(calc) || math.IsInf(calc, 0) {
			return FitMetrics{}, errors.New(
				"non-finite value in observed or computed data",
			)
		}

		if obs <= 0 || calc <= 0 {
			return FitMetrics{}, errors.New(
				"resistivity values must be positive",
			)
		}

		// Natural-log residual, shared with the optimizer objective.
		logResidual := math.Log(calc) - math.Log(obs)

		// Percentage residual relative to observed resistivity.
		percentResidual := 100 * (calc - obs) / obs
		absolutePercent := math.Abs(percentResidual)

		sumLogSquared += logResidual * logResidual
		sumPercentSquared += percentResidual * percentResidual
		sumAbsolutePercent += absolutePercent

		if absolutePercent > maxAbsolutePercent {
			maxAbsolutePercent = absolutePercent
		}
	}

	n := float64(len(observed))

	return FitMetrics{
		Points:              len(observed),
		LogRMS:              math.Sqrt(sumLogSquared / n),
		RMSPercent:          math.Sqrt(sumPercentSquared / n),
		MeanAbsolutePercent: sumAbsolutePercent / n,
		MaxAbsolutePercent:  maxAbsolutePercent,
	}, nil
}
