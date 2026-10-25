package utils

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"regexp"
	"unicode"
)

func GenerateVerificationToken() string {
	bytes := make([]byte, 32)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

func ValidateRegistration(name, email, password string) error {
	// NAME
	if len(name) < 2 {
		return fmt.Errorf("name must be at least 2 characters")
	}
	for _, char := range name {
		if !unicode.IsLetter(char) && char != ' ' && char != '-' {
			return fmt.Errorf("invalid name")
		}
	}
	// EMAIL
	pattern := regexp.MustCompile(`^[\w._%+-]+@[\w.-]+\.[a-zA-Z]{2,}$`)
	if !pattern.MatchString(email) {
		return fmt.Errorf("invalid email")
	}
	// PASSWORD
	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	hasUpper := false
	hasNumber := false
	hasSpecial := false
	for _, char := range password {
		if unicode.IsUpper(char) {
			hasUpper = true
		}
		if unicode.IsNumber(char) {
			hasNumber = true
		}
		if !unicode.IsLetter(char) && !unicode.IsNumber(char) {
			hasSpecial = true
		}
	}
	if !hasUpper || !hasNumber || !hasSpecial {
		return fmt.Errorf("password must include uppercase, number, and special character")
	}
	return nil
}
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}
func CheckPassword(password, hash string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}
