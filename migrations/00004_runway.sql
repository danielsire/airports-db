-- +goose Up
--
CREATE TABLE runway (

    id INT4 PRIMARY KEY,

    airport_ref INT4 NOT NULL,
    airport_ident VARCHAR(20) NOT NULL,

    length_ft DOUBLE PRECISION,
    width_ft DOUBLE PRECISION,

    surface VARCHAR(100),

    lighted BOOLEAN,
    closed BOOLEAN          NOT NULL DEFAULT false,

    le_ident VARCHAR(10),
    le_latitude_deg DOUBLE PRECISION,
    le_longitude_deg DOUBLE PRECISION,
    le_elevation_ft DOUBLE PRECISION,
    le_heading_degt DOUBLE PRECISION,
    le_displaced_threshold_ft DOUBLE PRECISION,

    he_ident VARCHAR(10),
    he_latitude_deg DOUBLE PRECISION,
    he_longitude_deg DOUBLE PRECISION,
    he_elevation_ft DOUBLE PRECISION,
    he_heading_degt DOUBLE PRECISION,
    he_displaced_threshold_ft DOUBLE PRECISION,


    created_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT fk_runway_airport
        FOREIGN KEY (airport_ref)
        REFERENCES airport (id)
        ON DELETE CASCADE
);

CREATE INDEX idx_runway_airport_id ON runway (airport_ref);
CREATE INDEX idx_runway_airport_ident ON runway (airport_ident);

-- +goose Down
DROP TABLE IF EXISTS runway;
