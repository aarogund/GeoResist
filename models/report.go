package models

import (
	"time"
)

type Report struct {
	ID, SurveyID                   int
	Introduction, ExecutiveSummary string
	LocalGeology, Methodology      string
	Results, Discussion            string
	Recommendations                string
	DrillDepth                     float64
	AquiferDetected                bool
	PdfURL                         string
	CreatedAt                      time.Time
}
