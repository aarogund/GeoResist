package models
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