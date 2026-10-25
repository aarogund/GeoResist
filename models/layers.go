package models

import "time"

type Layer struct {
	ID             int
	SurveyID       int
	LayerNumber    int
	Thickness      float64
	Depth          float64
	Resistivity    float64
	Lithology      string
	Interpretation string
	IsAquifer      bool
	CreatedAt      time.Time
}
