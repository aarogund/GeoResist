package models

import "time"

type Layer struct {
	ID             int
	SurveyID      int
	LayerNumber   int
	DepthFrom     float64
	DepthTo       float64
	Resistivity    float64
	Lithology      string
	Interpretation string
	IsAquifer     bool
	CreatedAt     time.Time
}
