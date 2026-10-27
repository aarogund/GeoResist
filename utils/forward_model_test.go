package utils

import (
	"fmt"
	"georesist/models"
	"math"
	"testing"
)

func TestForwardModelHomogeneousEarth(t *testing.T) {
	const rho = 100.0

	ab2 := []float64{1, 2, 5, 10, 20, 50}

	got := ForwardModel(
		ab2,
		[]float64{rho},
		[]float64{},
	)

	if len(got) != len(ab2) {
		t.Fatalf("got %d values, want %d", len(got), len(ab2))
	}

	for i, value := range got {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			t.Fatalf("result[%d] is not finite: %v", i, value)
		}

		if math.Abs(value-rho) > 1e-6 {
			t.Errorf(
				"AB/2=%v: got apparent resistivity %v, want %v",
				ab2[i], value, rho,
			)
		}
	}
}
func TestComputeKernelReference(t *testing.T) {
	resistivities := []float64{100, 10, 500}
	thicknesses := []float64{5, 15}

	lambdas := []float64{0.001, 0.01, 0.1, 1, 10}
	expected := []float64{
		282.275364403096,
		62.528839051681,
		54.447548061784,
		99.992571196530,
		100.000000000000,
	}

	for i, lambda := range lambdas {
		got := computeKernel(lambda, resistivities, thicknesses)

		t.Logf(
			"lambda=%g, got=%.12f, want=%.12f",
			lambda, got, expected[i],
		)

		if math.Abs(got-expected[i]) > 1e-9 {
			t.Errorf(
				"lambda=%g: got %.12f, want %.12f",
				lambda, got, expected[i],
			)
		}
	}
}

func TestForwardModelIntegrationDebug(t *testing.T) {
	filter, err := loadHankelFilter()
	if err != nil {
		t.Fatal(err)
	}

	resistivities := []float64{100, 10, 500}
	thicknesses := []float64{5, 15}
	spacing := 50.0

	sum := 0.0

	for j, base := range filter.Base {
		lambda := base / spacing
		kernel := computeKernel(lambda, resistivities, thicknesses)
		contribution := base * filter.J1[j] * kernel
		sum += contribution

		if j < 5 {
			t.Logf(
				"j=%d base=%.12e weight=%.12e lambda=%.12e kernel=%.12f contribution=%.12e",
				j,
				base,
				filter.J1[j],
				lambda,
				kernel,
				contribution,
			)
		}
	}

	t.Logf("Total J1 sum at AB/2=50 m: %.12f", sum)
}

func TestForwardModelAgainstErtkitReference(t *testing.T) {
	ab2 := []float64{
		1, 2, 5, 10, 20, 50, 100, 200, 500, 1000,
	}

	resistivities := []float64{100, 10, 500}
	thicknesses := []float64{5, 15}

	expected := []float64{
		99.853032,
		98.878296,
		86.985112,
		52.126238,
		20.577440,
		30.538870,
		57.749802,
		104.919425,
		207.219762,
		306.931660,
	}

	got := ForwardModel(ab2, resistivities, thicknesses)

	if len(got) != len(expected) {
		t.Fatalf(
			"expected %d results, got %d",
			len(expected),
			len(got),
		)
	}

	const tolerance = 0.00001

	for i := range expected {
		if math.IsNaN(got[i]) ||
			math.IsInf(got[i], 0) ||
			math.Abs(got[i]-expected[i]) > tolerance {
			t.Errorf(
				"AB/2=%.0f m: expected %.6f Ω·m, got %.9f Ω·m",
				ab2[i],
				expected[i],
				got[i],
			)
		}
	}
}
func TestLayerFitObjectiveMatchesSyntheticData(t *testing.T) {
	ab2 := []float64{
		1, 2, 5, 10, 20, 50, 100, 200, 500, 1000,
	}

	trueRho := []float64{100, 10, 500}
	trueThickness := []float64{5, 15}

	observed := ForwardModel(
		ab2,
		trueRho,
		trueThickness,
	)

	measurements := make([]models.Measurement, len(ab2))

	for i := range ab2 {
		measurements[i] = models.Measurement{
			AB2:                 ab2[i],
			ApparentResistivity: observed[i],
		}
	}

	score := layerFitObjective(
		measurements,
		trueRho,
		trueThickness,
	)

	if math.IsNaN(score) || math.IsInf(score, 0) {
		t.Fatalf("expected a finite objective score, got %v", score)
	}

	if score > 1e-8 {
		t.Errorf(
			"expected near-zero objective for the true model, got %.12f",
			score,
		)
	}
}

func TestFitLayerModelRecoversSyntheticModel(t *testing.T) {
	// Known three-layer earth model.
	trueResistivities := []float64{100.0, 10.0, 500.0}
	trueThicknesses := []float64{5.0, 15.0}

	// AB/2 electrode spacings in metres.
	ab2Values := []float64{
		1, 2, 5, 10, 20, 50, 100, 200, 500, 1000,
	}

	// Generate synthetic observations using the validated forward model.
	observedRhoa := ForwardModel(
		ab2Values,
		trueResistivities,
		trueThicknesses,
	)

	if len(observedRhoa) != len(ab2Values) {
		t.Fatalf(
			"ForwardModel returned %d values; want %d",
			len(observedRhoa),
			len(ab2Values),
		)
	}

	measurements := make([]models.Measurement, len(ab2Values))

	for i, ab2 := range ab2Values {
		if !isPositiveFinite(observedRhoa[i]) {
			t.Fatalf(
				"invalid synthetic apparent resistivity at index %d: %v",
				i,
				observedRhoa[i],
			)
		}

		measurements[i] = models.Measurement{
			AB2:                 ab2,
			ApparentResistivity: observedRhoa[i],
		}
	}

	// The boundary locations are starting estimates, not fixed constraints.
	boundaries := []LayerBoundary{
		{AB2: 5.0},
		{AB2: 20.0},
	}

	// Verify the starting model's fit score for comparison.
	startingResistivities := initialLayerResistivities(
		measurements,
		boundaries,
	)

	startingThicknesses := make([]float64, len(boundaries))
	for i, boundary := range boundaries {
		startingThicknesses[i] = math.Max(boundary.AB2/2, 1.0)
	}

	startingScore := layerFitObjective(
		measurements,
		startingResistivities,
		startingThicknesses,
	)

	if math.IsNaN(startingScore) || math.IsInf(startingScore, 0) {
		t.Fatalf("starting model has invalid score: %v", startingScore)
	}

	// Run the inversion.
	fit, err := FitLayerModel(measurements, boundaries, 3)
	if err != nil {
		t.Fatalf("FitLayerModel returned an error: %v", err)
	}

	// Check the output structure.
	if len(fit.Resistivities) != 3 {
		t.Fatalf(
			"got %d fitted resistivities; want 3: %v",
			len(fit.Resistivities),
			fit.Resistivities,
		)
	}

	if len(fit.Thicknesses) != 2 {
		t.Fatalf(
			"got %d fitted thicknesses; want 2: %v",
			len(fit.Thicknesses),
			fit.Thicknesses,
		)
	}

	if len(fit.Computed) != len(observedRhoa) {
		t.Fatalf(
			"got %d computed values; want %d",
			len(fit.Computed),
			len(observedRhoa),
		)
	}

	// Validate the fitted parameters.
	for i, rho := range fit.Resistivities {
		if !isPositiveFinite(rho) {
			t.Errorf(
				"fitted resistivity %d is invalid: %v",
				i,
				rho,
			)
		}
	}

	for i, thickness := range fit.Thicknesses {
		if !isPositiveFinite(thickness) {
			t.Errorf(
				"fitted thickness %d is invalid: %v",
				i,
				thickness,
			)
		}
	}

	// Recalculate the objective independently from the returned model.
	fittedScore := layerFitObjective(
		measurements,
		fit.Resistivities,
		fit.Thicknesses,
	)

	if math.IsNaN(fittedScore) || math.IsInf(fittedScore, 0) {
		t.Fatalf("fitted model has invalid score: %v", fittedScore)
	}

	// The optimizer should not make the starting fit worse.
	t.Logf("Starting LogRMS: %.8f", startingScore)
	t.Logf("Fitted LogRMS:   %.8f", fittedScore)
	t.Logf("Fitted resistivities (ohm-m): %v", fit.Resistivities)
	t.Logf("Fitted thicknesses (m):       %v", fit.Thicknesses)

	if fittedScore > startingScore+1e-10 {
		t.Errorf(
			"optimizer worsened the fit: starting LogRMS %.8f, fitted LogRMS %.8f",
			startingScore,
			fittedScore,
		)
	}

	// Confirm the returned score agrees with the independently calculated score.
	if math.Abs(fit.RMSLogError-fittedScore) > 1e-8 {
		t.Errorf(
			"reported RMSLogError %.10f does not match recalculated score %.10f",
			fit.RMSLogError,
			fittedScore,
		)
	}
}

func TestFitLayerModelAcrossCurveTypes(t *testing.T) {
	ab2Values := []float64{
		1, 2, 5, 10, 20, 50, 100, 200, 500, 1000,
	}

	boundaries := []LayerBoundary{
		{AB2: 5},
		{AB2: 20},
	}

	// Multiplicative perturbations simulate small measurement errors.
	// The factors are deterministic so the test is reproducible.
	noiseFactors := []float64{
		0.98, 1.02, 0.99, 1.01, 0.97,
		1.03, 1.01, 0.98, 1.02, 0.99,
	}

	tests := []struct {
		name          string
		curveType     string
		resistivities []float64
		thicknesses   []float64
	}{
		{
			name:          "A_type",
			curveType:     "A",
			resistivities: []float64{100, 200, 500},
			thicknesses:   []float64{5, 15},
		},
		{
			name:          "Q_type",
			curveType:     "Q",
			resistivities: []float64{500, 200, 100},
			thicknesses:   []float64{5, 15},
		},
		{
			name:          "H_type",
			curveType:     "H",
			resistivities: []float64{100, 10, 500},
			thicknesses:   []float64{5, 15},
		},
		{
			name:          "K_type",
			curveType:     "K",
			resistivities: []float64{10, 500, 100},
			thicknesses:   []float64{5, 15},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Generate ideal synthetic apparent resistivity.
			syntheticRhoa := ForwardModel(
				ab2Values,
				tt.resistivities,
				tt.thicknesses,
			)

			if len(syntheticRhoa) != len(ab2Values) {
				t.Fatalf(
					"ForwardModel returned %d values; want %d",
					len(syntheticRhoa),
					len(ab2Values),
				)
			}

			for i, rhoa := range syntheticRhoa {
				if !isPositiveFinite(rhoa) {
					t.Fatalf(
						"invalid synthetic apparent resistivity at index %d: %v",
						i,
						rhoa,
					)
				}
			}

			for _, noisy := range []bool{false, true} {
				scenario := "noise_free"
				if noisy {
					scenario = "with_noise"
				}

				t.Run(scenario, func(t *testing.T) {
					observedRhoa := make([]float64, len(syntheticRhoa))
					measurements := make(
						[]models.Measurement,
						len(ab2Values),
					)

					for i, rhoa := range syntheticRhoa {
						observed := rhoa
						if noisy {
							observed *= noiseFactors[i]
						}

						observedRhoa[i] = observed
						measurements[i] = models.Measurement{
							AB2:                 ab2Values[i],
							ApparentResistivity: observed,
						}
					}

					// Calculate the initial score using the same
					// initialization strategy as FitLayerModel.
					initialRho := initialLayerResistivities(
						measurements,
						boundaries,
					)

					initialThickness := make(
						[]float64,
						len(boundaries),
					)

					for i, boundary := range boundaries {
						initialThickness[i] = math.Max(
							boundary.AB2/2,
							1.0,
						)
					}

					startingScore := layerFitObjective(
						measurements,
						initialRho,
						initialThickness,
					)

					if math.IsNaN(startingScore) ||
						math.IsInf(startingScore, 0) {
						t.Fatalf(
							"invalid starting LogRMS: %v",
							startingScore,
						)
					}

					// Invert the synthetic measurements.
					fit, err := FitLayerModel(
						measurements,
						boundaries,
						3,
					)
					if err != nil {
						t.Fatalf(
							"FitLayerModel failed: %v",
							err,
						)
					}

					// Check the dimensions of the fitted model.
					if len(fit.Resistivities) != 3 {
						t.Fatalf(
							"got %d resistivities; want 3",
							len(fit.Resistivities),
						)
					}

					if len(fit.Thicknesses) != 2 {
						t.Fatalf(
							"got %d thicknesses; want 2",
							len(fit.Thicknesses),
						)
					}

					if len(fit.Computed) != len(observedRhoa) {
						t.Fatalf(
							"got %d computed values; want %d",
							len(fit.Computed),
							len(observedRhoa),
						)
					}

					// Ensure all fitted parameters are valid.
					for i, rho := range fit.Resistivities {
						if !isPositiveFinite(rho) {
							t.Errorf(
								"invalid fitted resistivity[%d]: %v",
								i,
								rho,
							)
						}
					}

					for i, thickness := range fit.Thicknesses {
						if !isPositiveFinite(thickness) {
							t.Errorf(
								"invalid fitted thickness[%d]: %v",
								i,
								thickness,
							)
						}
					}

					// Recalculate the score independently.
					fittedScore := layerFitObjective(
						measurements,
						fit.Resistivities,
						fit.Thicknesses,
					)

					if math.IsNaN(fittedScore) ||
						math.IsInf(fittedScore, 0) {
						t.Fatalf(
							"invalid fitted LogRMS: %v",
							fittedScore,
						)
					}

					// Report the results for each curve type.
					t.Logf("Curve type: %s", tt.curveType)
					t.Logf("Noise added: %t", noisy)
					t.Logf("Starting LogRMS: %.8f", startingScore)
					t.Logf("Fitted LogRMS:   %.8f", fittedScore)
					t.Logf(
						"True resistivities:   %v",
						tt.resistivities,
					)
					t.Logf(
						"Fitted resistivities: %v",
						fit.Resistivities,
					)
					t.Logf(
						"True thicknesses:   %v",
						tt.thicknesses,
					)
					t.Logf(
						"Fitted thicknesses: %v",
						fit.Thicknesses,
					)

					// Calculate relative parameter errors for diagnosis.
					for i, trueRho := range tt.resistivities {
						errorPct := 100 *
							math.Abs(fit.Resistivities[i]-trueRho) /
							trueRho

						t.Logf(
							"Layer %d resistivity error: %.2f%%",
							i+1,
							errorPct,
						)
					}

					for i, trueThickness := range tt.thicknesses {
						errorPct := 100 *
							math.Abs(fit.Thicknesses[i]-trueThickness) /
							trueThickness

						t.Logf(
							"Layer %d thickness error: %.2f%%",
							i+1,
							errorPct,
						)
					}

					// The inversion must not worsen the initial fit.
					if fittedScore > startingScore+1e-10 {
						t.Errorf(
							"fit worsened: initial LogRMS %.8f, fitted LogRMS %.8f",
							startingScore,
							fittedScore,
						)
					}

					// Verify that the returned score is consistent.
					if math.Abs(fit.RMSLogError-fittedScore) > 1e-8 {
						t.Errorf(
							"reported LogRMS %.10f differs from recalculated score %.10f",
							fit.RMSLogError,
							fittedScore,
						)
					}

					if noisy {
						t.Logf(
							"Noise factors: %s",
							fmt.Sprint(noiseFactors),
						)
					}
				})
			}
		})
	}
}

func TestForwardModelHTypeModel(t *testing.T) {
	// Classic H-type model parameters
	// h₁, h₂ (m)
	resistivities := []float64{100, 10, 500}
	thicknesses := []float64{5, 20}
	ab2 := []float64{1, 2, 5, 10, 20, 50, 100, 200, 500, 1000} // AB/2 spacings (m)

	// Compute synthetic curve
	apparentRho := ForwardModel(ab2, resistivities, thicknesses)

	// Verify we got results for all spacings
	if len(apparentRho) != len(ab2) {
		t.Fatalf("Expected %d results, got %d", len(ab2), len(apparentRho))
	}

	// Print results for visual inspection
	fmt.Println("\n=== H-Type Model VES Response ===")
	fmt.Println("Layer 1: ρ₁ = 100 Ω·m")
	fmt.Println("Layer 2: ρ₂ = 10 Ω·m")
	fmt.Println("Layer 3: ρ₃ = 500 Ω·m")
	fmt.Println("\nSynthetic Apparent Resistivity:")
	fmt.Println("AB/2 (m) | ρa (Ω·m)")
	fmt.Println("---------|---------")

	for i, spacing := range ab2 {
		fmt.Printf("%8.1f | %8.2f\n", spacing, apparentRho[i])
	}

	// Basic sanity checks
	for i, rho := range apparentRho {
		if rho <= 0 {
			t.Errorf("Negative or zero resistivity at AB/2=%.1f: %.2f", ab2[i], rho)
		}
	}

	// H-type models typically show: start at layer 1 ρ, dip to layer 2 ρ, rise to layer 3 ρ
	// At small AB/2, we should see ~100 (layer 1)
	if apparentRho[0] < 80 || apparentRho[0] > 120 {
		t.Logf("Warning: First value (%.2f) seems off for 100 Ω·m layer", apparentRho[0])
	}

	// At large AB/2, we should see ~100	 (layer 3)
	if apparentRho[len(apparentRho)-1] < 80 || apparentRho[len(apparentRho)-1] > 120 {
		fmt.Printf(
			"Bottom-layer resistivity: %.2f Ω·m\n",
			resistivities[len(resistivities)-1],
		)
	}

	t.Logf("✓ Forward model computed successfully for %d spacings", len(ab2))
}
func TestForwardModelThreeLayers(t *testing.T) {
	ab2 := []float64{1, 2, 5, 10, 20, 50}

	rho := []float64{100, 20, 500}
	thickness := []float64{5, 15}

	got := ForwardModel(ab2, rho, thickness)

	if len(got) != len(ab2) {
		t.Fatalf("got %d values, want %d", len(got), len(ab2))
	}

	for i, value := range got {
		if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
			t.Errorf("result[%d] is invalid: %v", i, value)
		}
	}
}

func TestForwardModelHomogeneousEarthMultipleLayers(t *testing.T) {
	ab2 := []float64{1, 2, 5, 10, 20, 50}
	const rho = 100.0

	tests := []struct {
		name          string
		resistivities []float64
		thicknesses   []float64
	}{
		{
			name:          "one layer",
			resistivities: []float64{rho},
			thicknesses:   []float64{},
		},
		{
			name:          "two equal-resistivity layers",
			resistivities: []float64{rho, rho},
			thicknesses:   []float64{10},
		},
		{
			name:          "three equal-resistivity layers",
			resistivities: []float64{rho, rho, rho},
			thicknesses:   []float64{5, 15},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ForwardModel(
				ab2,
				tt.resistivities,
				tt.thicknesses,
			)

			if len(got) != len(ab2) {
				t.Fatalf(
					"expected %d results, got %d",
					len(ab2),
					len(got),
				)
			}

			for i, value := range got {
				if math.IsNaN(value) ||
					math.IsInf(value, 0) ||
					value <= 0 {
					t.Errorf(
						"result[%d] is invalid: %v",
						i,
						value,
					)
					continue
				}

				if math.Abs(value-rho) > 1e-6 {
					t.Errorf(
						"AB/2 = %v: expected %.6f, got %.6f",
						ab2[i],
						rho,
						value,
					)
				}
			}
		})
	}
}

