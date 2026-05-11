package utils

import (
	"github.com/google/uuid"
)

func GenerateVerificationToken() string {
	return uuid.New().String()
}