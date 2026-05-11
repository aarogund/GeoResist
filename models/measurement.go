package models

type Measurement struct {
	ID                  int
	SurveyID            int
	AB2                 float64 // decimal numbers use float64
	SerialNumber        int
	MN2                 float64
	Voltage             float64
	ElectricCurrent     float64
	Resistance          float64
	GeometricFactor     float64
	ApparentResistivity float64
}
