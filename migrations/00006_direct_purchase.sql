-- +goose Up
ALTER TABLE listings
    DROP COLUMN resource_ref;

CREATE TABLE match_requests (
    id uuid PRIMARY KEY,
    buyer_id uuid NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    seller_id uuid NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    resource_ref uuid NOT NULL REFERENCES book_listing_details (id) ON DELETE RESTRICT,
    buy_listing_id uuid REFERENCES listings (id) ON DELETE RESTRICT,
    sell_listing_id uuid REFERENCES listings (id) ON DELETE RESTRICT,
    trade_price_cents bigint NOT NULL CHECK (trade_price_cents > 0),
    source text NOT NULL CHECK (source IN ('automatic', 'seller_accept', 'buyer_purchase')),
    buyer_confirmed_at timestamptz,
    seller_confirmed_at timestamptz,
    status text NOT NULL CHECK (status IN ('pending', 'completed', 'declined', 'cancelled')),
    order_ref uuid,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT match_requests_distinct_participants_check CHECK (buyer_id <> seller_id),
    CONSTRAINT match_requests_distinct_listings_check CHECK (
        buy_listing_id IS NULL OR sell_listing_id IS NULL OR buy_listing_id <> sell_listing_id
    ),
    CONSTRAINT match_requests_source_listings_check CHECK (
        (source = 'automatic' AND buy_listing_id IS NOT NULL AND sell_listing_id IS NOT NULL)
        OR (source = 'seller_accept' AND buy_listing_id IS NOT NULL AND sell_listing_id IS NULL)
        OR (source = 'buyer_purchase' AND buy_listing_id IS NULL AND sell_listing_id IS NOT NULL)
    ),
    CONSTRAINT match_requests_direct_confirmations_check CHECK (
        source = 'automatic'
        OR (buyer_confirmed_at IS NOT NULL AND seller_confirmed_at IS NOT NULL)
    ),
    CONSTRAINT match_requests_completion_check CHECK (
        (status = 'completed' AND order_ref IS NOT NULL AND buyer_confirmed_at IS NOT NULL AND seller_confirmed_at IS NOT NULL)
        OR (status <> 'completed' AND order_ref IS NULL)
    ),
    CONSTRAINT match_requests_buyer_confirmation_time_check CHECK (
        buyer_confirmed_at IS NULL OR buyer_confirmed_at BETWEEN created_at AND updated_at
    ),
    CONSTRAINT match_requests_seller_confirmation_time_check CHECK (
        seller_confirmed_at IS NULL OR seller_confirmed_at BETWEEN created_at AND updated_at
    ),
    CONSTRAINT match_requests_updated_time_check CHECK (updated_at >= created_at)
);

CREATE INDEX match_requests_buyer_idx ON match_requests (buyer_id, created_at DESC, id DESC);
CREATE INDEX match_requests_seller_idx ON match_requests (seller_id, created_at DESC, id DESC);

CREATE TABLE orders (
    id uuid PRIMARY KEY,
    match_request_id uuid NOT NULL UNIQUE REFERENCES match_requests (id) ON DELETE RESTRICT,
    status text NOT NULL CHECK (status IN ('pending_delivery', 'completed')),
    seller_confirmed_completion_at timestamptz,
    buyer_confirmed_completion_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT orders_match_pair_key UNIQUE (id, match_request_id),
    CONSTRAINT orders_completion_check CHECK (
        status <> 'completed'
        OR (seller_confirmed_completion_at IS NOT NULL AND buyer_confirmed_completion_at IS NOT NULL)
    ),
    CONSTRAINT orders_seller_confirmation_time_check CHECK (
        seller_confirmed_completion_at IS NULL OR seller_confirmed_completion_at BETWEEN created_at AND updated_at
    ),
    CONSTRAINT orders_buyer_confirmation_time_check CHECK (
        buyer_confirmed_completion_at IS NULL OR buyer_confirmed_completion_at BETWEEN created_at AND updated_at
    ),
    CONSTRAINT orders_updated_time_check CHECK (updated_at >= created_at)
);

ALTER TABLE match_requests
    ADD CONSTRAINT match_requests_order_pair_fkey
        FOREIGN KEY (order_ref, id) REFERENCES orders (id, match_request_id) ON DELETE RESTRICT;

CREATE INDEX orders_created_idx ON orders (created_at DESC, id DESC);

-- +goose Down
ALTER TABLE match_requests
    DROP CONSTRAINT match_requests_order_pair_fkey;

DROP TABLE orders;
DROP TABLE match_requests;

ALTER TABLE listings
    ADD COLUMN resource_ref uuid;

UPDATE listings
SET resource_ref = id;

ALTER TABLE listings
    ALTER COLUMN resource_ref SET NOT NULL,
    ADD CONSTRAINT listings_resource_ref_fkey
        FOREIGN KEY (resource_ref) REFERENCES book_listing_details (id) ON DELETE CASCADE,
    ADD CONSTRAINT listings_id_resource_ref_check CHECK (id = resource_ref);
