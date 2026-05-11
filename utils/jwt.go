package utils

import (
<<<<<<< HEAD
=======
	"fmt"
	"os"
>>>>>>> ec7d6fb3ac126ba9fa48818abe6ada4bc5b8a551
	"time"

	"github.com/golang-jwt/jwt/v5"
)

<<<<<<< HEAD
var jwtKey = []byte("secret_key")

type Claims struct {
	UserID int `json:"user_id"`
	jwt.RegisteredClaims
}

func GenerateToken(userID int) (string, error) {

	expiration := time.Now().Add(24 * time.Hour)

	claims := &Claims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiration),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	return token.SignedString(jwtKey)
}
=======
type Claims struct {
	UserID float64 `json:"user_id"`
	jwt.RegisteredClaims
}

func VerifyToken(tokenStr string) (*Claims, error) {
	secret := os.Getenv("JWT_SECRET")

	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok {
		return nil, fmt.Errorf("invalid claims")
	}
	return claims, nil
}

func GenerateToken(userID int) (string, error) {
	secret := os.Getenv("JWT_SECRET")

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		UserID: float64(userID),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
		},
	})
	return token.SignedString([]byte(secret))
}
>>>>>>> ec7d6fb3ac126ba9fa48818abe6ada4bc5b8a551
