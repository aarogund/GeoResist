package models

import "time"

type User struct {
	ID        int
	Name      string
	Email     string
	Password  string
	Verified  bool
	CreatedAt time.Time
}
