-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION haversine_km(
    lat1 DOUBLE PRECISION,
    lon1 DOUBLE PRECISION,
    lat_db DOUBLE PRECISION,
    lon_db DOUBLE PRECISION
)
RETURNS DOUBLE PRECISION AS $$
SELECT 2 * 6371 * asin(
    sqrt(
        sin(radians(lat_db - lat1) / 2)^2 +
        cos(radians(lat1)) * cos(radians(lat_db)) *
        sin(radians(lon_db - lon1) / 2)^2
    )
);
$$ LANGUAGE sql IMMUTABLE;

-- +goose StatementEnd

-- +goose Down
DROP FUNCTION IF EXISTS haversine_km(double precision, double precision, double precision, double precision);
