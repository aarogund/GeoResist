package utils

import (
	"fmt"
	"math"

	"georesist/models"
)

type LayerModelComparison struct {
	TwoLayerFit       LayerFitResult `json:"two_layer_fit"`
	TwoLayerMetrics   FitMetrics     `json:"two_layer_metrics"`
	ThreeLayerFit     LayerFitResult `json:"three_layer_fit"`
	ThreeLayerMetrics FitMetrics     `json:"three_layer_metrics"`
	PreferredModel    string         `json:"preferred_model"`
}

func CompareLayerModels(
	measurements []models.Measurement,
	twoLayerBoundaries []LayerBoundary,
	threeLayerBoundaries []LayerBoundary,
) (LayerModelComparison, error) {
	var result LayerModelComparison

	data := prepareVESData(measurements)
	if len(data) < 4 {
		return result, fmt.Errorf(
			"need at least 4 valid measurements for model comparison; got %d",
			len(data),
		)
	}

	if len(twoLayerBoundaries) != 1 {
		return result, fmt.Errorf(
			"two-layer model requires 1 boundary; got %d",
			len(twoLayerBoundaries),
		)
	}

	if len(threeLayerBoundaries) != 2 {
		return result, fmt.Errorf(
			"three-layer model requires 2 boundaries; got %d",
			len(threeLayerBoundaries),
		)
	}

	// Copy boundaries because FitLayerModel sorts them in place.
	twoBoundaries := append(
		[]LayerBoundary(nil),
		twoLayerBoundaries...,
	)
	threeBoundaries := append(
		[]LayerBoundary(nil),
		threeLayerBoundaries...,
	)

	twoFit, err := FitLayerModel(data, twoBoundaries, 2)
	if err != nil {
		return result, fmt.Errorf("fit two-layer model: %w", err)
	}

	threeFit, err := FitLayerModel(data, threeBoundaries, 3)
	if err != nil {
		return result, fmt.Errorf("fit three-layer model: %w", err)
	}

	observed := make([]float64, len(data))
	for i, m := range data {
		observed[i] = m.ApparentResistivity
	}

	twoMetrics, err := CalculateFitMetrics(
		observed,
		twoFit.Computed,
	)
	if err != nil {
		return result, fmt.Errorf(
			"calculate two-layer metrics: %w", err,
		)
	}

	threeMetrics, err := CalculateFitMetrics(
		observed,
		threeFit.Computed,
	)
	if err != nil {
		return result, fmt.Errorf(
			"calculate three-layer metrics: %w", err,
		)
	}

	result = LayerModelComparison{
		TwoLayerFit:       twoFit,
		TwoLayerMetrics:   twoMetrics,
		ThreeLayerFit:     threeFit,
		ThreeLayerMetrics: threeMetrics,
	}

	switch {
	case math.Abs(twoMetrics.LogRMS-threeMetrics.LogRMS) < 1e-9:
		result.PreferredModel = "equal-fit"
	case twoMetrics.LogRMS < threeMetrics.LogRMS:
		result.PreferredModel = "two-layer"
	default:
		result.PreferredModel = "three-layer"
	}

	return result, nil
}
