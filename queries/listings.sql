-- name: CreateBookListingDetail :exec
INSERT INTO book_listing_details (id, isbn_13, title, author, cover_image_url, condition, note)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: CreateListing :exec
INSERT INTO listings (id, owner_id, side, price_cents, status, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ReadBookListingDetails :many
SELECT id, isbn_13, title, author, cover_image_url, condition, note
FROM book_listing_details
WHERE id = ANY($1::uuid[]);

-- name: ReadListings :many
SELECT id, owner_id, side, price_cents, status, created_at, updated_at
FROM listings
WHERE id = ANY($1::uuid[]);

-- name: ReadListingForUpdate :one
SELECT id, owner_id, side, price_cents, status, created_at, updated_at
FROM listings
WHERE id = $1
FOR UPDATE;

-- name: UpdateListing :execrows
UPDATE listings
SET owner_id = $2,
    side = $3,
    price_cents = $4,
    status = $5,
    created_at = $6,
    updated_at = $7
WHERE id = $1;

-- name: FindListingIDs :many
SELECT d.id
FROM book_listing_details d
JOIN listings l ON l.id = d.id
WHERE l.side = sqlc.arg(side)
  AND (
      (sqlc.narg(owner_id)::uuid IS NULL AND l.status = 'open')
      OR l.owner_id = sqlc.narg(owner_id)::uuid
  )
  AND (sqlc.narg(condition)::text IS NULL OR d.condition = sqlc.narg(condition)::text)
  AND (sqlc.narg(isbn_13)::text IS NULL OR d.isbn_13 = sqlc.narg(isbn_13)::text)
  AND (sqlc.narg(title_query)::text IS NULL OR d.title ILIKE '%' || sqlc.narg(title_query)::text || '%' ESCAPE '\')
  AND (
      sqlc.narg(before_created_at)::timestamptz IS NULL
      OR (l.created_at, l.id) < (sqlc.narg(before_created_at)::timestamptz, sqlc.narg(before_id)::uuid)
  )
ORDER BY l.created_at DESC, l.id DESC
LIMIT sqlc.arg(page_size);

-- name: ReadListingOwners :many
SELECT id, nickname
FROM users
WHERE id = ANY($1::uuid[]);
