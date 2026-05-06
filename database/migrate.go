package database

import (
	"log"
)

func Migrate() {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS users (
            id          SERIAL PRIMARY KEY,
            name        VARCHAR(100) NOT NULL,
            email       VARCHAR(100) UNIQUE NOT NULL,
            password    VARCHAR(255) NOT NULL,
            verified    BOOLEAN DEFAULT FALSE,
            created_at  TIMESTAMP DEFAULT NOW()
        )`,

		`CREATE TABLE IF NOT EXISTS surveys (
            id              SERIAL PRIMARY KEY,
            user_id         INTEGER REFERENCES users(id),
            title           VARCHAR(200) NOT NULL,
            location        VARCHAR(200),
            latitude        DECIMAL(10,8),
            longitude       DECIMAL(11,8),
            terrain_type    VARCHAR(20),
            date            DATE,
            status          VARCHAR(20) DEFAULT 'pending',
            created_at      TIMESTAMP DEFAULT NOW()
        )`,

		`CREATE TABLE IF NOT EXISTS measurements (
    		id                   SERIAL PRIMARY KEY,
    		survey_id            INTEGER REFERENCES surveys(id),
   			serial_number        INTEGER,
  			ab2                  DECIMAL(10,4) NOT NULL,
   			mn2                  DECIMAL(10,4) NOT NULL,
    		voltage              DECIMAL(10,4),    
    		electric_current     DECIMAL(10,4),    
   			resistance           DECIMAL(10,4),    
    		geometric_factor     DECIMAL(10,4),    
    		apparent_resistivity DECIMAL(10,4)     
)`,

		`CREATE TABLE IF NOT EXISTS reports (
            id                  SERIAL PRIMARY KEY,
            survey_id           INTEGER REFERENCES surveys(id),
            introduction        TEXT,
            executive_summary   TEXT,
            local_geology       TEXT,
            methodology         TEXT,
            results             TEXT,
            discussion          TEXT,
            recommendations     TEXT,
            drill_depth         DECIMAL(10,2),
            aquifer_detected    BOOLEAN DEFAULT FALSE,
            pdf_url             VARCHAR(500),
            created_at          TIMESTAMP DEFAULT NOW()
        )`,
	}

	for _, query := range queries {
		_, err := DB.Exec(query)
		if err != nil {
			log.Fatal("Migration error:", err)
		}
	}

	log.Println("Database migrated successfully!")
}
