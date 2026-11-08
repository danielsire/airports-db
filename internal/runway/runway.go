package runway

import (
	"errors"
	"time"
)

var ErrNotFound = errors.New("Runway not found")

type Runway struct {
	ID                     int       `json:"id"`
	AirportRef             int       `json:"airport_ref"`   // NOT NULL
	AirportIdent           string    `json:"airport_ident"` // NOT NULL
	LengthFt               *float64  `json:"length_ft"`
	WidthFt                *float64  `json:"width_ft"`
	Surface                *string   `json:"surface"`
	Lighted                *bool     `json:"lighted"`
	Closed                 *bool     `json:"closed"`
	LeIdent                *string   `json:"le_ident"`
	LeLatitudeDeg          *float64  `json:"le_latitude_deg"`
	LeLongitudeDeg         *float64  `json:"le_longitude_deg"`
	LeElevationFt          *float64  `json:"le_elevation_ft"`
	LeHeadingDegt          *float64  `json:"le_heading_degt"`
	LeDisplacedThresholdFt *float64  `json:"le_displaced_threshold_ft"`
	HeIdent                *string   `json:"he_ident"`
	HeLatitudeDeg          *float64  `json:"he_latitude_deg"`
	HeLongitudeDeg         *float64  `json:"he_longitude_deg"`
	HeElevationFt          *float64  `json:"he_elevation_ft"`
	HeHeadingDegt          *float64  `json:"he_heading_degt"`
	HeDisplacedThresholdFt *float64  `json:"he_displaced_threshold_ft"`
	CreatedAt              time.Time `json:"-"` // Ignorado no CSV, gerado automaticamente via DEFAULT now()
}
