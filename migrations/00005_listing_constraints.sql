-- +goose Up
ALTER TABLE book_listing_details
    ADD CONSTRAINT book_listing_details_isbn13_valid_check
        CHECK (
            left(isbn_13, 3) IN ('978', '979')
            AND (
                substring(isbn_13, 1, 1)::integer
                + 3 * substring(isbn_13, 2, 1)::integer
                + substring(isbn_13, 3, 1)::integer
                + 3 * substring(isbn_13, 4, 1)::integer
                + substring(isbn_13, 5, 1)::integer
                + 3 * substring(isbn_13, 6, 1)::integer
                + substring(isbn_13, 7, 1)::integer
                + 3 * substring(isbn_13, 8, 1)::integer
                + substring(isbn_13, 9, 1)::integer
                + 3 * substring(isbn_13, 10, 1)::integer
                + substring(isbn_13, 11, 1)::integer
                + 3 * substring(isbn_13, 12, 1)::integer
                + substring(isbn_13, 13, 1)::integer
            ) % 10 = 0
        ),
    ADD CONSTRAINT book_listing_details_title_canonical_check
        CHECK (title = btrim(title)),
    ADD CONSTRAINT book_listing_details_author_canonical_check
        CHECK (author = btrim(author)),
    ADD CONSTRAINT book_listing_details_cover_image_url_check
        CHECK (
            cover_image_url IS NULL
            OR (
                cover_image_url = btrim(cover_image_url)
                AND cover_image_url ~ '^[A-Za-z][A-Za-z0-9+.-]*:'
            )
        ),
    ADD CONSTRAINT book_listing_details_note_canonical_check
        CHECK (note IS NULL OR (note = btrim(note) AND char_length(note) > 0));

-- +goose Down
ALTER TABLE book_listing_details
    DROP CONSTRAINT book_listing_details_note_canonical_check,
    DROP CONSTRAINT book_listing_details_cover_image_url_check,
    DROP CONSTRAINT book_listing_details_author_canonical_check,
    DROP CONSTRAINT book_listing_details_title_canonical_check,
    DROP CONSTRAINT book_listing_details_isbn13_valid_check;
