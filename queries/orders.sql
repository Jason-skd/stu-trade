-- name: CreateMatchRequest :exec
INSERT INTO match_requests (
    id, buyer_id, seller_id, resource_ref, buy_listing_id, sell_listing_id,
    trade_price_cents, source, buyer_confirmed_at, seller_confirmed_at,
    status, order_ref, created_at, updated_at
)
VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10,
    $11, $12, $13, $14
);

-- name: UpdateMatchRequest :execrows
UPDATE match_requests
SET buyer_id = $2,
    seller_id = $3,
    resource_ref = $4,
    buy_listing_id = $5,
    sell_listing_id = $6,
    trade_price_cents = $7,
    source = $8,
    buyer_confirmed_at = $9,
    seller_confirmed_at = $10,
    status = $11,
    order_ref = $12,
    created_at = $13,
    updated_at = $14
WHERE id = $1;

-- name: ReadMatchRequests :many
SELECT id, buyer_id, seller_id, resource_ref, buy_listing_id, sell_listing_id,
       trade_price_cents, source, buyer_confirmed_at, seller_confirmed_at,
       status, order_ref, created_at, updated_at
FROM match_requests
WHERE id = ANY($1::uuid[]);

-- name: CreateOrder :exec
INSERT INTO orders (
    id, match_request_id, status, seller_confirmed_completion_at,
    buyer_confirmed_completion_at, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: FindOrderIDs :many
SELECT o.id
FROM orders o
JOIN match_requests m ON m.id = o.match_request_id
WHERE (m.buyer_id = sqlc.arg(participant_id) OR m.seller_id = sqlc.arg(participant_id))
  AND (sqlc.narg(status)::text IS NULL OR o.status = sqlc.narg(status)::text)
  AND (
      sqlc.narg(before_created_at)::timestamptz IS NULL
      OR (o.created_at, o.id) < (sqlc.narg(before_created_at)::timestamptz, sqlc.narg(before_id)::uuid)
  )
ORDER BY o.created_at DESC, o.id DESC
LIMIT sqlc.arg(page_size);

-- name: ReadOrders :many
SELECT id, match_request_id, status, seller_confirmed_completion_at,
       buyer_confirmed_completion_at, created_at, updated_at
FROM orders
WHERE id = ANY($1::uuid[]);

-- name: ReadOrderUsers :many
SELECT id, email, nickname
FROM users
WHERE id = ANY($1::uuid[]);
