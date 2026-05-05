package utils

import (
	"fmt"
	"math"
)

func CalculateK(ab2, mn2 float64) float64 {
	return math.Pi * ((ab2 * ab2) - (mn2 * mn2)) / mn2
}

func CalculateRhoA(k, resistance float64) float64 {
	return k * resistance
}
func ProcessMeasurement(m *models.Measurement) error {
	// step 1 — calculate R if missing
	if m.Resistance == 0 {
		if m.Voltage != 0 && m.Current != 0 {
			m.Resistance = m.Voltage / m.Current
		} else if m.ApparentResistivity == 0 {
			return fmt.Errorf("insufficient data: need R or V and I")
		}
	}

	// step 2 — calculate K if missing
	if m.GeometricFactor == 0 {
		m.GeometricFactor = CalculateK(m.AB2, m.MN2)
	}

	// step 3 — calculate ρa if missing
	if m.ApparentResistivity == 0 {
		m.ApparentResistivity = m.GeometricFactor * m.Resistance
	}

	return nil
}
