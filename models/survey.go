package models

import "time"

type Survey struct {
	ID          int
	UserID      int
	Title       string
	Location    string
	Latitude    float64
	Longitude   float64
	TerrainType string
	Date        time.Time
	Status      string
	CreatedAt   time.Time
}
