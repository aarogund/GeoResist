package database

import (
	"database/sql"
	"fmt"
	_ "github.com/lib/pq"
	"log"
	"os"
)

var DB *sql.DB

func Connect() {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		log.Fatal("DATABASE_URL not set!")
	}
	var err error
	DB, err = sql.Open("postgres", connStr)
	if err != nil {
		log.Fatal("Error connecting:", err)
	}
	err = DB.Ping()
	if err != nil {
		log.Fatal("Database unreachable:", err)
	}
	fmt.Println("Database connected!")
}
