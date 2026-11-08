-- +goose Up

CREATE TABLE airport (

    id INT4 PRIMARY KEY,

    ident VARCHAR(250) NOT NULL,
    type VARCHAR(250) NOT NULL,
    name VARCHAR(500) NOT NULL,

    latitude_deg DOUBLE PRECISION NOT NULL,
    longitude_deg DOUBLE PRECISION NOT NULL,
    elevation_ft DOUBLE PRECISION,

    continent VARCHAR(500),

    iso_country VARCHAR(10),
    iso_region VARCHAR(10),
    municipality VARCHAR(500),

    scheduled_service VARCHAR(250),

    icao_code VARCHAR(10),
    iata_code VARCHAR(10),
    gps_code VARCHAR(250),
    local_code VARCHAR(250),
    home_link VARCHAR(500),
    wikipedia_link VARCHAR(500),
    keywords  VARCHAR(500),

    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_airport_ident ON airport (ident);
CREATE INDEX idx_airport_icao ON airport (icao_code);
CREATE INDEX idx_airport_type ON airport (type);

-- +goose Down
DROP TABLE IF EXISTS airport;
