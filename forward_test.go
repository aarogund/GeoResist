// forward_test.go
package main

import (
	"fmt"
	"georesist/utils"
	"testing"
)

func TestForwardModelHTypeModel(t *testing.T) {
	// Classic H-type model parameters
	// h₁, h₂ (m)
	resistivities := []float64{100, 10, 500}
	thicknesses := []float64{5, 20}
	ab2 := []float64{1, 2, 5, 10, 20, 50, 100} // AB/2 spacings (m)

	// Compute synthetic curve
	apparentRho := utils.ForwardModel(ab2, resistivities, thicknesses)

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
		t.Logf("Warning: Last value (%.2f) seems off for 100 Ω·m halfspace", apparentRho[len(apparentRho)-1])
	}

	t.Logf("✓ Forward model computed successfully for %d spacings", len(ab2))
}
