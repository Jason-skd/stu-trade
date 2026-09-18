-- +goose Up
CREATE TABLE book_listing_details (
    id uuid PRIMARY KEY,
    isbn_13 text NOT NULL CHECK (isbn_13 ~ '^[0-9]{13}$'),
    title text NOT NULL CHECK (length(btrim(title)) > 0),
    author text NOT NULL CHECK (length(btrim(author)) > 0),
    cover_image_url text,
    condition text NOT NULL CHECK (condition IN ('new', 'like_new', 'good', 'fair', 'poor')),
    note text
);

CREATE TABLE listings (
    id uuid PRIMARY KEY REFERENCES book_listing_details (id) ON DELETE CASCADE,
    owner_id uuid NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    resource_ref uuid NOT NULL REFERENCES book_listing_details (id) ON DELETE CASCADE,
    side text NOT NULL CHECK (side IN ('buy', 'sell')),
    price_cents bigint NOT NULL CHECK (price_cents > 0),
    status text NOT NULL CHECK (status IN ('open', 'reserved', 'matched', 'withdrawn')),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK (id = resource_ref),
    CHECK (updated_at >= created_at)
);

CREATE INDEX listings_market_idx ON listings (side, status, created_at DESC, id DESC);
CREATE INDEX listings_owner_idx ON listings (owner_id, side, created_at DESC, id DESC);

-- +goose Down
DROP TABLE listings;
DROP TABLE book_listing_details;
