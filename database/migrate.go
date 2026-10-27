package database

import (
	"fmt"
	"log"
)

func Migrate() {
	// Migrations must never drop application tables: this function may run against
	// a database containing real user data.

	queries := []string{
		`CREATE TABLE IF NOT EXISTS users (
            id                  SERIAL PRIMARY KEY,
            name                VARCHAR(100) NOT NULL,
            email               VARCHAR(100) UNIQUE NOT NULL,
            password            VARCHAR(255) NOT NULL,
            verified            BOOLEAN DEFAULT FALSE,
            verification_token  VARCHAR(64),      
            created_at          TIMESTAMP DEFAULT NOW()
        )`,

		`CREATE TABLE IF NOT EXISTS surveys (
            id              SERIAL PRIMARY KEY,
            user_id         INTEGER REFERENCES users(id),
            title           VARCHAR(200) NOT NULL,
            location        VARCHAR(200) NOT NULL,
            latitude        DECIMAL(10,6),
            longitude       DECIMAL(11,6),
            terrain_type    VARCHAR(20),
            number_of_layers  INTEGER,
            geologic_history  TEXT,
            previous_wells    TEXT,
            date            DATE,
            status          VARCHAR(20) DEFAULT 'pending',
            created_at      TIMESTAMP DEFAULT NOW()
        )`,

		`CREATE TABLE IF NOT EXISTS measurements (
    		id                   SERIAL PRIMARY KEY,
    		survey_id            INTEGER REFERENCES surveys(id),
   			serial_number        INTEGER,
  			ab2                  DECIMAL(10,4) NOT NULL,
   			mn2                  DECIMAL(10,4),
    		voltage              DECIMAL(10,4),    
    		electric_current     DECIMAL(10,4),    
   			resistance           DECIMAL(10,4),    
    		geometric_factor     DECIMAL(10,4),    
    		apparent_resistivity DECIMAL(10,4)     
        )`,
		`CREATE TABLE IF NOT EXISTS layers (
             id              SERIAL PRIMARY KEY,
             survey_id       INTEGER REFERENCES surveys(id),
             layer_number    INTEGER,
             thickness      DECIMAL(10,2),
             depth       DECIMAL(10,2),
             resistivity     DECIMAL(15,4),
             lithology       VARCHAR(200),
             interpretation  TEXT,
             is_aquifer      BOOLEAN DEFAULT FALSE,
             created_at      TIMESTAMP DEFAULT NOW()
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

		`CREATE TABLE IF NOT EXISTS report_share_tokens (
    id BIGSERIAL PRIMARY KEY,
    report_id INTEGER NOT NULL
        REFERENCES reports(id) ON DELETE CASCADE,
    token_hash CHAR(64) NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    CHECK (expires_at > created_at)
)`,

		`CREATE INDEX IF NOT EXISTS idx_report_share_tokens_report_id
    ON report_share_tokens(report_id)`,

		`CREATE INDEX IF NOT EXISTS idx_report_share_tokens_expiry
    ON report_share_tokens(expires_at)`,

		`CREATE TABLE IF NOT EXISTS token_blacklisted (
            jti VARCHAR(36) PRIMARY KEY,
            expires_at TIMESTAMP NOT NULL,
            revoked_at TIMESTAMP NOT NULL DEFAULT NOW()
        )`,
	}

	for i, query := range queries {
		fmt.Println("Running query", i+1)
		_, err := DB.Exec(query)
		if err != nil {
			log.Fatalf("Migration error: %v\nFull Query: %s\n", err, query)
		}
	}

	log.Println("Database migrated successfully!")
}
