package utils

import (
	"georesist/models"
	"math"
)

func CalculateK(ab2, mn2 float64) float64 {
	return math.Pi * ((ab2 * ab2) - (mn2 * mn2)) / (2 * mn2)
}

func CalculateRhoA(k, resistance float64) float64 {
	return k * resistance
}
func ProcessMeasurement(
	m *models.Measurement,
	inputType string,
) {

	// =========================
	// GEOMETRIC FACTOR
	// =========================

	if m.GeometricFactor == 0 {

		m.GeometricFactor =
			CalculateK(
				m.AB2,
				m.MN2,
			)
	}

	switch inputType {

	// =========================
	// RAW VOLTAGE MODE
	// =========================

	case "voltage":

		if m.ElectricCurrent != 0 {

			m.Resistance =
				m.Voltage / m.ElectricCurrent
		}

		m.ApparentResistivity =
			m.GeometricFactor *
				m.Resistance

	// =========================
	// RESISTANCE MODE
	// =========================

	case "resistance":

		// User supplied resistance.
		// Calculate apparent resistivity.

		if m.ApparentResistivity == 0 {

			m.ApparentResistivity =
				m.GeometricFactor *
					m.Resistance
		}

		// =========================
		// RESISTIVITY MODE
		// =========================
	case "resistivity":

		// User already supplied apparent resistivity.
		// DO NOT RECALCULATE IT.

		// Optional:
		// derive resistance if needed later
		if m.Resistance == 0 &&
			m.GeometricFactor != 0 {

			m.Resistance =
				m.ApparentResistivity /
					m.GeometricFactor
		}

		return
	// =========================
	// MIXED MODE
	// =========================

	case "mixed":

		// fill only missing fields

		if m.Resistance == 0 {

			if m.ElectricCurrent != 0 {

				m.Resistance =
					m.Voltage /
						m.ElectricCurrent
			}
		}

		if m.ApparentResistivity == 0 {

			m.ApparentResistivity =
				m.GeometricFactor *
					m.Resistance
		}
	}
}
