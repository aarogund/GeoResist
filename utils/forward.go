package utils

import (
	"georesist/models"
	"log"
	"math"
)

func computeKernel(
	lambda float64,
	resistivities []float64,
	thicknesses []float64,
) float64 {
	if len(resistivities) == 0 ||
		len(resistivities) != len(thicknesses)+1 ||
		lambda < 0 ||
		math.IsNaN(lambda) ||
		math.IsInf(lambda, 0) {
		return math.NaN()
	}

	for _, rho := range resistivities {
		if !isPositiveFinite(rho) {
			return math.NaN()
		}
	}

	for _, h := range thicknesses {
		if !isPositiveFinite(h) {
			return math.NaN()
		}
	}

	// The bottom layer is a half-space.
	kernel := resistivities[len(resistivities)-1]

	// Recursively calculate the transform from bottom to top.
	for i := len(resistivities) - 2; i >= 0; i-- {
		rho := resistivities[i]
		tanhU := math.Tanh(lambda * thicknesses[i])

		numerator := rho * (kernel + rho*tanhU)
		denominator := rho + kernel*tanhU

		if math.Abs(denominator) < 1e-12 {
			return math.NaN()
		}

		kernel = numerator / denominator

		if !isPositiveFinite(kernel) {
			return math.NaN()
		}
	}

	return kernel
}

func ForwardModel(
	ab2 []float64,
	resistivities []float64,
	thicknesses []float64,
) []float64 {
	if len(ab2) == 0 ||
		len(resistivities) == 0 ||
		len(resistivities) != len(thicknesses)+1 {
		return []float64{}
	}

	for _, rho := range resistivities {
		if !isPositiveFinite(rho) {
			return []float64{}
		}
	}

	for _, h := range thicknesses {
		if !isPositiveFinite(h) {
			return []float64{}
		}
	}

	filter, err := loadHankelFilter()
	result := make([]float64, len(ab2))

	if err != nil {
		for i := range result {
			result[i] = math.NaN()
		}
		return result
	}

	for i, spacing := range ab2 {
		if !isPositiveFinite(spacing) {
			result[i] = math.NaN()
			continue
		}

		sum := 0.0
		valid := true

		for j, base := range filter.Base {
			lambda := base / spacing

			kernel := computeKernel(
				lambda,
				resistivities,
				thicknesses,
			)

			if !isPositiveFinite(kernel) {
				valid = false
				break
			}

			sum += base * filter.J1[j] * kernel
		}

		if !valid || !isPositiveFinite(sum) {
			result[i] = math.NaN()
			continue
		}

		result[i] = sum
	}

	return result
}

// utils/layer_model.go

const SchlurmbergerDepthFactor = 0.42

func BuildLayerModel(
	measurements []models.Measurement,
	boundaries []LayerBoundary,
) (resistivities []float64, thicknesses []float64) {
	log.Printf("DEBUG BuildLayerModel: %d boundaries, %d measurements\n", len(boundaries), len(measurements))
	for i, b := range boundaries {
		log.Printf("  boundary[%d]: AB2=%.1f\n", i, b.AB2)
	}
	if len(measurements) == 0 || len(boundaries) == 0 {
		return []float64{}, []float64{}
	}

	minAB2 := measurements[0].AB2
	maxAB2 := measurements[len(measurements)-1].AB2

	// Layer 1
	rho1 := averageResistivity(measurements, minAB2, boundaries[0].AB2)
	resistivities = append(resistivities, rho1)
	h1 := boundaries[0].AB2 * SchlurmbergerDepthFactor
	thicknesses = append(thicknesses, h1)

	// Intermediate layers
	prevDepth := h1
	for i := 1; i < len(boundaries); i++ {
		rho := averageResistivity(measurements, boundaries[i-1].AB2, boundaries[i].AB2)
		resistivities = append(resistivities, rho)
		currentDepth := boundaries[i].AB2 * SchlurmbergerDepthFactor
		thicknesses = append(thicknesses, currentDepth-prevDepth)
		prevDepth = currentDepth
	}

	// Halfspace
	rhoLast := averageResistivity(measurements, boundaries[len(boundaries)-1].AB2, maxAB2)
	resistivities = append(resistivities, rhoLast)

	return resistivities, thicknesses
}

// averageResistivity computes the mean apparent resistivity within an AB2 range
func averageResistivity(measurements []models.Measurement, minAB2, maxAB2 float64) float64 {
	var sum float64
	var count int

	for _, m := range measurements {
		if m.AB2 >= minAB2 && m.AB2 <= maxAB2 {
			sum += m.ApparentResistivity
			count++
		}
	}

	if count == 0 {
		return 0
	}

	return sum / float64(count)
}
