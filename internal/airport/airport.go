package airport

import (
	"errors"
	"time"
)

var ErrNotFound = errors.New("Airport not found")

type Airport struct {
	ID               int       `json:"id"`
	Ident            string    `json:"ident"`
	Type             string    `json:"type"`
	Name             string    `json:"name"`
	LatitudeDeg      float64   `json:"latitude_deg"`
	LongitudeDeg     float64   `json:"longitude_deg"`
	ElevationFt      *float64  `json:"elevation_ft"`
	Continent        *string   `json:"continent"`
	ISOCountry       *string   `json:"iso_country"`
	ISORegion        *string   `json:"iso_region"`
	Municipality     *string   `json:"municipality"`
	ScheduledService *string   `json:"scheduled_service"`
	ICAOCode         *string   `json:"icao_code"`
	IATACode         *string   `json:"iata_code"`
	GPSCode          *string   `json:"gps_code"`
	LocalCode        *string   `json:"local_code"`
	HomeLink         *string   `json:"home_link"`
	WikipediaLink    *string   `json:"wikipedia_link"`
	Keywords         *string   `json:"keywords"`
	CreatedAt        time.Time `json:"-"` // Ignorado no CSV, gerado pelo banco via DEFAULT now()
}
