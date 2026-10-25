package utils

import (
	"georesist/models"
	"log"
	"math"
)

func computeKernel(lambda float64, resistivities []float64, thicknesses []float64) float64 {
	if len(resistivities) == 0 {
		return 0
	}

	// Start from the bottom (last layer) with infinite thickness
	// Kernel for the bottom layer (halfspace)
	kernel := resistivities[len(resistivities)-1]

	// Work upward through layers using recursive filter
	// This implements Ghosh's method for kernel computation
	for i := len(resistivities) - 2; i >= 0; i-- {
		rho := resistivities[i]
		h := thicknesses[i]

		// Compute the filter coefficient based on lambda and layer thickness
		// u = 2 * lambda * h (dimensionless parameter)
		u := lambda * h

		// Compute tanh(u) for the recursive step
		tanhU := math.Tanh(u)

		// Apply recursive relation: K_new = (rho - kernel) * tanh(u) / (rho + kernel * tanh(u))
		// This is a standard transform in VES kernel computation
		numerator := rho * (kernel + rho*tanhU)
		denominator := rho + kernel*tanhU

		// Avoid division by zero
		if math.Abs(denominator) < 1e-10 {
			kernel = rho
		} else {
			kernel = numerator / denominator
		}
	}

	return kernel
}

func ForwardModel(ab2 []float64, resistivities []float64, thicknesses []float64) []float64 {
	// Validate inputs
	if len(ab2) == 0 || len(resistivities) == 0 {
		return []float64{}
	}

	if len(resistivities) != len(thicknesses)+1 {
		// Last layer has no thickness (halfspace)
		return []float64{}
	}

	apparentRho := make([]float64, len(ab2))

	// Ghosh filter parameters
	coeffs := []float64{
		0.0225,
		-0.0499,
		0.1064,
		0.1854,
		1.9720,
		-1.5716,
		0.4018,
		-0.0814,
		0.0148,
	}
	shifts := []int{-3, -2, -1, 0, 1, 2, 3, 4, 5}
	// s := math.Log(10) / 3

	coeffSum := 0.0
	for _, c := range coeffs {
		coeffSum += c
	}

	// For each electrode spacing (AB/2)
	for i, spacing := range ab2 {
		if spacing <= 0 {
			apparentRho[i] = 0
			continue
		}

		// Compute sum of 9 weighted kernel evaluations
		sum := 0.0
		for j := 0; j < len(coeffs); j++ {
			lambda := math.Pow(10, float64(shifts[j])/3) / spacing
			kernel := computeKernel(lambda, resistivities, thicknesses)
			contrib := coeffs[j] * kernel
			sum += contrib
		}

		// Apply Ghosh filter formula: ρa = (1/AB2) * Σ(c_j * T)
		apparentRho[i] = sum
	}

	return apparentRho
}

// utils/layer_model.go

const SchlurmbergerDepthFactor = 0.42

// BuildLayerModel constructs a layer model from detected boundaries
// by computing average resistivity per layer and thickness using the 0.42 rule
//
// Process:
// 1. For each layer segment (between boundaries), average the ρa values
// 2. Estimate thickness as AB2 / 0.42 for each boundary
// 3. Halfspace (last layer) gets no thickness entry
//
// Returns two slices: resistivities and thicknesses (both length = n_layers)
// Note: thicknesses[n-1] is omitted for the halfspace
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
