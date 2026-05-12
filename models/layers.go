package models

import "time"

type Layers struct {
	Id             int
	Survey_id      int
	Layer_number   int
	Depth_from     float64
	Depth_to       float64
	Resistivity    float64
	Lithology      string
	Interpretation string
	Is_aquifer     bool
	Created_at     time.Time
}
