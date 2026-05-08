package models
<<<<<<< HEAD

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
=======
import "time"

type Survey struct{
ID int
UserID int
Title, Location string
Latitude, Longitude float64
TerrainType string
Date time.Time
Status string
CreatedAt time.Time
}
>>>>>>> fd65441d8ad80696c83d40a2f832db49b4ff283e
